package approval

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/nikicat/secrets-dispatcher/internal/procutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"log/slog"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// Returns the auto-approve scope of the given request, failing the test when
// Request.AutoApprovable rejects it.
func mustScope(t *testing.T, req *Request) AutoApproveScope {
	t.Helper()
	scope, err := req.AutoApproveScope()
	require.NoError(t, err, "AutoApproveScope")
	return scope
}

// Adds an auto-approve rule made from the given request and returns its ID,
// failing the test as mustScope does.
func mustAddRule(t *testing.T, mgr *Manager, req *Request) AutoApproveRuleID {
	t.Helper()
	return mgr.AddAutoApproveRule(mustScope(t, req), req.SenderInfo.InvokerName)
}

func TestRequestAutoApproveScope_RefusesRequestWithoutExe(t *testing.T) {
	req := &Request{
		ID:         "remote-1",
		Type:       RequestTypeGetSecret,
		SenderInfo: RemoteSenderForTest("gh"),
	}

	_, err := req.AutoApproveScope()
	assert.ErrorIs(t, err, ErrNotAutoApprovable)
}

func TestApproveAndAutoApprove_LeavesRequestWithoutExePending(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManagerForTest(ManagerConfig{})

		call := startApproval(context.Background(), mgr, RequestInfo{
			Session:    "session",
			Type:       RequestTypeGetSecret,
			SenderInfo: RemoteSenderForTest("gh"),
		})

		id := waitPendingID(t, mgr)

		require.ErrorIs(t, mgr.ApproveAndAutoApprove(id), ErrNotAutoApprovable)
		_, ok := mgr.GetPending(id)
		require.True(t, ok, "request was resolved, want it still pending")
		assert.Empty(t, mgr.ListAutoApproveRules())

		require.NoError(t, mgr.Approve(id))
		_, err := call.wait()
		assert.NoError(t, err, "RequireApproval")
	})
}

func TestAddRuleFromCancelled_OnlyCancelledRequestsEarnARule(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManagerForTest(ManagerConfig{})
		end := func(resolve func(RequestID) error) RequestID {
			call := startApproval(context.Background(), mgr, RequestInfo{
				Session:    "session",
				Type:       RequestTypeGetSecret,
				SenderInfo: ghSender,
			})
			id := waitPendingID(t, mgr)
			require.NoError(t, resolve(id))
			call.wait()
			return id
		}

		denied := end(mgr.Deny)
		assert.ErrorIs(t, mgr.AddRuleFromCancelled(denied), ErrNotFound)
		assert.ErrorIs(t, mgr.AddRuleFromCancelled("unknown"), ErrNotFound)
		assert.Empty(t, mgr.ListAutoApproveRules())

		cancelled := end(mgr.Cancel)
		require.NoError(t, mgr.AddRuleFromCancelled(cancelled))
		rules := mgr.ListAutoApproveRules()
		require.Len(t, rules, 1)
		assert.Equal(t, RequestTypeGetSecret, rules[0].RequestType)
	})
}

func TestAutoApproveRule_MatchesRetry(t *testing.T) {
	mgr := NewManagerForTest(ManagerConfig{})

	req := &Request{
		ID:   "req-1",
		Type: RequestTypeWrite,
		Items: []ItemInfo{{
			Path:       itemPath("default", "i1"),
			Label:      "gh:github.com",
			Attributes: map[string]string{"service": "gh:github.com"},
		}},
		SenderInfo: ghSender,
	}
	mustAddRule(t, mgr, req)

	// A retry with the same invoker, type, collection and attributes matches.
	_, ok := mgr.CheckAutoApproveRules(RequestInfo{
		Items: []ItemInfo{{
			Path:       itemPath("default", "i99"),
			Attributes: map[string]string{"service": "gh:github.com", "extra": "val"},
		}},
		Type:       RequestTypeWrite,
		SenderInfo: ghSender,
	})
	assert.True(t, ok, "expected auto-approve rule to match retry request")
}

// A process that sets its comm to a trusted app's name but runs another binary
// is not auto-approved, since rules match on the exe (ADR 0005).
func TestAutoApproveRule_CommSpoofDoesNotMatch(t *testing.T) {
	tests := []struct {
		name  string
		typ   RequestType
		items []ItemInfo
		comm  string
		exe   procutil.Path
	}{
		{
			name:  "get_secret",
			typ:   RequestTypeGetSecret,
			items: []ItemInfo{itemInCollection("login", "github-token")},
			comm:  "node",
			exe:   "/usr/bin/node",
		},
		{name: "gpg_sign", typ: RequestTypeGPGSign, comm: "git", exe: "/usr/bin/git"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr := NewManagerForTest(ManagerConfig{})
			genuine := RequestInfo{Type: tt.typ, Items: tt.items, SenderInfo: LocalSenderForTest(tt.comm, tt.exe)}
			// The user chose "always allow" for the real binary.
			mustAddRule(t, mgr, &Request{RequestInfo: genuine})

			// Malware sets its comm to the trusted name but runs /tmp/malware.
			spoofed := genuine
			spoofed.SenderInfo = LocalSenderForTest(tt.comm, "/tmp/malware")
			_, ok := mgr.CheckAutoApproveRules(spoofed)
			assert.False(t, ok, "comm-spoofed caller with a different exe must not match the auto-approve rule")

			_, ok = mgr.CheckAutoApproveRules(genuine)
			assert.True(t, ok, "the genuine invoker exe should still match")
		})
	}
}

func TestAutoApproveRule_DifferentInvokerNoMatch(t *testing.T) {
	mgr := NewManagerForTest(ManagerConfig{})

	req := &Request{
		ID:         "req-1",
		Type:       RequestTypeGetSecret,
		Items:      []ItemInfo{itemInCollection("default", "i1")},
		SenderInfo: ghSender,
	}
	mustAddRule(t, mgr, req)

	_, ok := mgr.CheckAutoApproveRules(RequestInfo{
		Items:      []ItemInfo{itemInCollection("default", "i1")},
		Type:       RequestTypeGetSecret,
		SenderInfo: LocalSenderForTest("seahorse", "/usr/bin/seahorse"),
	})
	assert.False(t, ok, "expected no match for different invoker")
}

func TestAutoApproveRule_Expiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManagerForTest(ManagerConfig{AutoApproveDuration: 50 * time.Millisecond})

		req := &Request{
			ID:         "req-1",
			Type:       RequestTypeGetSecret,
			Items:      []ItemInfo{itemInCollection("default", "i1")},
			SenderInfo: ghSender,
		}
		mustAddRule(t, mgr, req)

		time.Sleep(60 * time.Millisecond)

		_, ok := mgr.CheckAutoApproveRules(RequestInfo{
			Items:      []ItemInfo{itemInCollection("default", "i1")},
			Type:       RequestTypeGetSecret,
			SenderInfo: ghSender,
		})
		require.False(t, ok, "expected expired rule not to match")

		assert.Empty(t, mgr.ListAutoApproveRules(), "expected expired rules to be cleaned up")
	})
}

func TestAutoApproveRule_IntegrationWithRequireApproval(t *testing.T) {
	mgr := NewManagerForTest(ManagerConfig{})

	req := &Request{
		ID:         "req-1",
		Type:       RequestTypeGetSecret,
		Items:      []ItemInfo{{Path: itemPath("default", "i1"), Attributes: map[string]string{"service": "gh:github.com"}}},
		SenderInfo: ghSender,
	}
	mustAddRule(t, mgr, req)

	_, err := mgr.RequireApproval(context.Background(), RequestInfo{
		Items:      []ItemInfo{{Path: itemPath("default", "i99"), Attributes: map[string]string{"service": "gh:github.com"}}},
		Session:    "session",
		Type:       RequestTypeGetSecret,
		SenderInfo: ghSender,
	})
	assert.NoError(t, err, "expected auto-approved")
}

func TestAutoApproveRule_ListAndRemove(t *testing.T) {
	mgr := NewManagerForTest(ManagerConfig{})

	req := &Request{
		ID:         "req-1",
		Type:       RequestTypeGetSecret,
		SenderInfo: ghSender,
	}
	ruleID := mustAddRule(t, mgr, req)

	require.Len(t, mgr.ListAutoApproveRules(), 1)

	require.NoError(t, mgr.RemoveAutoApproveRule(ruleID))

	assert.Empty(t, mgr.ListAutoApproveRules(), "rules after removal")
}

func TestAutoApproveRule_AttributeSubsetMatch(t *testing.T) {
	mgr := NewManagerForTest(ManagerConfig{})

	req := &Request{
		ID:   "req-1",
		Type: RequestTypeGetSecret,
		Items: []ItemInfo{{
			Path:       itemPath("default", "i1"),
			Attributes: map[string]string{"service": "gh:github.com"},
		}},
		SenderInfo: ghSender,
	}
	mustAddRule(t, mgr, req)

	// A request with more attributes than the rule names still matches.
	_, ok := mgr.CheckAutoApproveRules(RequestInfo{
		Items: []ItemInfo{{
			Path:       itemPath("default", "i2"),
			Attributes: map[string]string{"service": "gh:github.com", "user": "nb"},
		}},
		Type:       RequestTypeGetSecret,
		SenderInfo: ghSender,
	})
	assert.True(t, ok, "expected subset attribute match")

	// A request with a different attribute value does not match.
	_, ok = mgr.CheckAutoApproveRules(RequestInfo{
		Items: []ItemInfo{{
			Path:       itemPath("default", "i2"),
			Attributes: map[string]string{"service": "other:example.com"},
		}},
		Type:       RequestTypeGetSecret,
		SenderInfo: ghSender,
	})
	assert.False(t, ok, "expected no match for different attribute value")
}

func TestAutoApproveRule_AttributeValuesAreLiteral(t *testing.T) {
	path := itemPath("default", "i1")
	covers := func(made, asked string) bool {
		mgr := NewManagerForTest(ManagerConfig{})
		mustAddRule(t, mgr, &Request{
			ID:         "req-1",
			Type:       RequestTypeGetSecret,
			Items:      []ItemInfo{{Path: path, Attributes: map[string]string{"service": made}}},
			SenderInfo: ghSender,
		})
		_, ok := mgr.CheckAutoApproveRules(RequestInfo{
			Items:      []ItemInfo{{Path: path, Attributes: map[string]string{"service": asked}}},
			Type:       RequestTypeGetSecret,
			SenderInfo: ghSender,
		})
		return ok
	}

	assert.False(t, covers("*", "other"), "a rule made from the value * covers another value")
	assert.False(t, covers("a*", "abc"), "a rule made from the value a* covers another value")
	assert.False(t, covers("[ab]", "a"), "a rule made from the value [ab] covers another value")
	assert.True(t, covers("*", "*"), "a rule made from the value * does not cover itself")
	assert.True(t, covers(`a\b`, `a\b`), "a rule made from a value with a backslash does not cover itself")
	assert.True(t, covers("[a", "[a"), "a rule made from a malformed class does not cover itself")
}

func TestAutoApproveRule_DedupRefreshesExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManagerForTest(ManagerConfig{})

		req := &Request{
			ID:         "req-1",
			Type:       RequestTypeGetSecret,
			Items:      []ItemInfo{itemInCollection("default", "i1")},
			SenderInfo: ghSender,
		}

		id1 := mustAddRule(t, mgr, req)

		rules := mgr.ListAutoApproveRules()
		require.Len(t, rules, 1)
		expiry1 := rules[0].ExpiresAt

		time.Sleep(10 * time.Millisecond)
		id2 := mustAddRule(t, mgr, req)

		assert.Equal(t, id1, id2, "expected same rule ID")

		rules = mgr.ListAutoApproveRules()
		require.Len(t, rules, 1, "rules after dedup")
		assert.True(t, rules[0].ExpiresAt.After(expiry1), "expected expiry to be refreshed")
	})
}

func TestAutoApproveRule_DifferentAttributesNotDeduped(t *testing.T) {
	mgr := NewManagerForTest(ManagerConfig{})

	req1 := &Request{
		ID:   "req-1",
		Type: RequestTypeWrite,
		Items: []ItemInfo{{
			Path:       itemPath("default", "i1"),
			Attributes: map[string]string{"service": "gh:github.com", "username": "nikicat"},
		}},
		SenderInfo: ghSender,
	}
	req2 := &Request{
		ID:   "req-2",
		Type: RequestTypeWrite,
		Items: []ItemInfo{{
			Path:       itemPath("default", "i2"),
			Attributes: map[string]string{"service": "gh:github.com", "username": ""},
		}},
		SenderInfo: ghSender,
	}

	id1 := mustAddRule(t, mgr, req1)
	id2 := mustAddRule(t, mgr, req2)

	assert.NotEqual(t, id1, id2, "expected different rule IDs for different attributes")
	assert.Len(t, mgr.ListAutoApproveRules(), 2)
}

// Attribute maps of the same size but different keys are different scopes,
// even when the missing key of one would read as the other's empty value.
func TestAutoApproveRule_DifferentAttributeKeysNotDeduped(t *testing.T) {
	mgr := NewManagerForTest(ManagerConfig{})

	writeRequest := func(id RequestID, attrs map[string]string) *Request {
		return &Request{
			ID:         id,
			Type:       RequestTypeWrite,
			Items:      []ItemInfo{{Path: itemPath("default", string(id)), Attributes: attrs}},
			SenderInfo: ghSender,
		}
	}
	scope1 := mustScope(t, writeRequest("i1", map[string]string{"a": ""}))
	scope2 := mustScope(t, writeRequest("i2", map[string]string{"b": "x"}))

	assert.False(t, scope1.Equal(scope2), "scopes with attributes {a: \"\"} and {b: x} are equal")
	mgr.AddAutoApproveRule(scope1, "gh")
	mgr.AddAutoApproveRule(scope2, "gh")
	assert.Len(t, mgr.ListAutoApproveRules(), 2)
}

// AddAutoApproveRule refreshing a rule and CheckAutoApproveRules share the
// rule list without a race. Run with -race.
func TestAutoApproveRule_RefreshRacesCheck(t *testing.T) {
	mgr := NewManagerForTest(ManagerConfig{})
	req := &Request{
		ID:         "req-1",
		Type:       RequestTypeGetSecret,
		SenderInfo: ghSender,
	}
	scope := mustScope(t, req)
	mgr.AddAutoApproveRule(scope, "gh")

	var wg sync.WaitGroup
	wg.Go(func() {
		for range 200 {
			mgr.AddAutoApproveRule(scope, "gh")
		}
	})
	wg.Go(func() {
		for range 200 {
			mgr.CheckAutoApproveRules(RequestInfo{Type: RequestTypeGetSecret, SenderInfo: ghSender})
		}
	})
	wg.Wait()
}

// A rule for any collection leaves the collection key out of its JSON and its
// log record, and both scopes survive a JSON round trip.
func TestCollectionScope_JSONAndLog(t *testing.T) {
	for _, scope := range []CollectionScope{AnyCollection, OnlyCollection("login")} {
		rule := AutoApproveRule{ID: "r", InvokerExe: "/usr/bin/gh", RequestType: RequestTypeGetSecret, Collection: scope}
		data, err := json.Marshal(rule)
		require.NoError(t, err)
		var log bytes.Buffer
		slog.New(slog.NewTextHandler(&log, nil)).Info("rule", "rule", rule)
		if scope == AnyCollection {
			assert.NotContains(t, string(data), "collection")
			assert.NotContains(t, log.String(), "collection")
		} else {
			assert.Contains(t, string(data), `"collection":"login"`)
			assert.Contains(t, log.String(), "rule.collection=login")
		}
		var got AutoApproveRule
		require.NoError(t, json.Unmarshal(data, &got))
		assert.Equal(t, scope, got.Collection)
	}
}
