package approval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/nikicat/secrets-dispatcher/internal/procutil"
	"github.com/nikicat/secrets-dispatcher/internal/sender"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// Returns a sender whose invoker has the given comm and exe, as the daemon
// resolves a live caller. Auto-approve rules match only on the exe.
func testSender(comm string, exe procutil.Path) SenderInfo {
	const pid = 4242
	return SenderInfo{
		PID:          pid,
		InvokerName:  comm,
		ProcessChain: []procutil.Process{{Name: comm, PID: pid, Exe: &exe}},
	}
}

// Returns the Secret Service path of the named item in the named collection.
func itemPath(collection, name string) string {
	return "/org/freedesktop/secrets/collection/" + collection + "/" + name
}

// Returns an ItemInfo whose path places it in the named collection.
func itemInCollection(collection, name string) ItemInfo {
	return ItemInfo{Path: itemPath(collection, name)}
}

// Adds an auto-approve rule made from req and returns its ID. Fails the test
// when req is not AutoApprovable.
func mustAddRule(t *testing.T, mgr *Manager, req *Request) AutoApproveRuleID {
	t.Helper()
	scope, err := req.AutoApproveScope()
	if err != nil {
		t.Fatalf("AutoApproveScope: %v", err)
	}
	return mgr.AddAutoApproveRule(scope, req.SenderInfo.InvokerName)
}

func newRuleManager() *Manager {
	return NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
}

func waitPendingID(t *testing.T, mgr *Manager) RequestID {
	t.Helper()
	var id RequestID
	require.Eventually(t, func() bool {
		if reqs := mgr.Pending(); len(reqs) > 0 {
			id = reqs[0].ID
		}
		return id != ""
	}, time.Second, 10*time.Millisecond, "request did not appear in pending list")
	return id
}

func TestRequestAutoApproveScope_RefusesRequestWithoutExe(t *testing.T) {
	req := &Request{
		ID:         "remote-1",
		Type:       RequestTypeGetSecret,
		SenderInfo: SenderInfo{Client: "remote:laptop", Info: sender.Info{InvokerName: "gh"}},
	}

	_, err := req.AutoApproveScope()
	if !errors.Is(err, ErrNotAutoApprovable) {
		t.Fatalf("err = %v, want ErrNotAutoApprovable", err)
	}
}

func TestApproveAndAutoApprove_LeavesRequestWithoutExePending(t *testing.T) {
	mgr := newRuleManager()

	var wg sync.WaitGroup
	var approvalErr error
	wg.Go(func() {
		_, approvalErr = mgr.RequireApproval(context.Background(), RequestInfo{
			Session:    "session",
			Type:       RequestTypeGetSecret,
			SenderInfo: SenderInfo{InvokerName: "gh"},
		})
	})

	id := waitPendingID(t, mgr)

	if err := mgr.ApproveAndAutoApprove(id); !errors.Is(err, ErrNotAutoApprovable) {
		t.Fatalf("err = %v, want ErrNotAutoApprovable", err)
	}
	if _, ok := mgr.GetPending(id); !ok {
		t.Fatal("request was resolved, want it still pending")
	}
	if rules := mgr.ListAutoApproveRules(); len(rules) != 0 {
		t.Errorf("rules = %v, want none", rules)
	}

	if err := mgr.Approve(id); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	wg.Wait()
	if approvalErr != nil {
		t.Errorf("RequireApproval = %v, want nil", approvalErr)
	}
}

func TestAddRuleFromCancelled_OnlyCancelledRequestsEarnARule(t *testing.T) {
	mgr := newRuleManager()
	end := func(resolve func(RequestID) error) RequestID {
		var wg sync.WaitGroup
		wg.Go(func() {
			_, _ = mgr.RequireApproval(context.Background(), RequestInfo{
				Session:    "session",
				Type:       RequestTypeGetSecret,
				SenderInfo: testSender("gh", "/usr/bin/gh"),
			})
		})
		id := waitPendingID(t, mgr)
		require.NoError(t, resolve(id))
		wg.Wait()
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
}

func TestAutoApproveRule_MatchesRetry(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})

	// Simulate a cancelled request
	req := &Request{
		ID:   "req-1",
		Type: RequestTypeWrite,
		Items: []ItemInfo{{
			Path:       itemPath("default", "i1"),
			Label:      "gh:github.com",
			Attributes: map[string]string{"service": "gh:github.com"},
		}},
		SenderInfo: testSender("gh", "/usr/bin/gh"),
	}
	mustAddRule(t, mgr, req)

	// A retry with the same invoker, type, collection and attributes matches.
	_, ok := mgr.CheckAutoApproveRules(RequestInfo{
		Items: []ItemInfo{{
			Path:       itemPath("default", "i99"),
			Attributes: map[string]string{"service": "gh:github.com", "extra": "val"},
		}},
		Type:       RequestTypeWrite,
		SenderInfo: testSender("gh", "/usr/bin/gh"),
	})
	if !ok {
		t.Fatal("expected auto-approve rule to match retry request")
	}
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
			mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
			genuine := RequestInfo{Type: tt.typ, Items: tt.items, SenderInfo: testSender(tt.comm, tt.exe)}
			// The user chose "always allow" for the real binary.
			mustAddRule(t, mgr, &Request{RequestInfo: genuine})

			// Malware sets its comm to the trusted name but runs /tmp/malware.
			spoofed := genuine
			spoofed.SenderInfo = testSender(tt.comm, "/tmp/malware")
			_, ok := mgr.CheckAutoApproveRules(spoofed)
			assert.False(t, ok, "comm-spoofed caller with a different exe must not match the auto-approve rule")

			_, ok = mgr.CheckAutoApproveRules(genuine)
			assert.True(t, ok, "the genuine invoker exe should still match")
		})
	}
}

func TestAutoApproveRule_DifferentInvokerNoMatch(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})

	req := &Request{
		ID:         "req-1",
		Type:       RequestTypeGetSecret,
		Items:      []ItemInfo{itemInCollection("default", "i1")},
		SenderInfo: testSender("gh", "/usr/bin/gh"),
	}
	mustAddRule(t, mgr, req)

	_, ok := mgr.CheckAutoApproveRules(RequestInfo{
		Items:      []ItemInfo{itemInCollection("default", "i1")},
		Type:       RequestTypeGetSecret,
		SenderInfo: testSender("seahorse", "/usr/bin/seahorse"),
	})
	if ok {
		t.Fatal("expected no match for different invoker")
	}
}

func TestAutoApproveRule_Expiry(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100, AutoApproveDuration: 50 * time.Millisecond})

	req := &Request{
		ID:         "req-1",
		Type:       RequestTypeGetSecret,
		Items:      []ItemInfo{itemInCollection("default", "i1")},
		SenderInfo: testSender("gh", "/usr/bin/gh"),
	}
	mustAddRule(t, mgr, req)

	time.Sleep(60 * time.Millisecond)

	_, ok := mgr.CheckAutoApproveRules(RequestInfo{
		Items:      []ItemInfo{itemInCollection("default", "i1")},
		Type:       RequestTypeGetSecret,
		SenderInfo: testSender("gh", "/usr/bin/gh"),
	})
	if ok {
		t.Fatal("expected expired rule not to match")
	}

	// Expired rules should be cleaned up
	if len(mgr.ListAutoApproveRules()) != 0 {
		t.Fatal("expected expired rules to be cleaned up")
	}
}

func TestAutoApproveRule_IntegrationWithRequireApproval(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})

	// Add a rule
	req := &Request{
		ID:         "req-1",
		Type:       RequestTypeGetSecret,
		Items:      []ItemInfo{{Path: itemPath("default", "i1"), Attributes: map[string]string{"service": "gh:github.com"}}},
		SenderInfo: testSender("gh", "/usr/bin/gh"),
	}
	mustAddRule(t, mgr, req)

	// RequireApproval should return nil immediately (auto-approved)
	_, err := mgr.RequireApproval(context.Background(), RequestInfo{
		Items:      []ItemInfo{{Path: itemPath("default", "i99"), Attributes: map[string]string{"service": "gh:github.com"}}},
		Session:    "session",
		Type:       RequestTypeGetSecret,
		SenderInfo: testSender("gh", "/usr/bin/gh"),
	})
	if err != nil {
		t.Fatalf("expected auto-approved, got: %v", err)
	}
}

func TestAutoApproveRule_ListAndRemove(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})

	req := &Request{
		ID:         "req-1",
		Type:       RequestTypeGetSecret,
		SenderInfo: testSender("gh", "/usr/bin/gh"),
	}
	ruleID := mustAddRule(t, mgr, req)

	rules := mgr.ListAutoApproveRules()
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}

	if err := mgr.RemoveAutoApproveRule(ruleID); err != nil {
		t.Fatalf("RemoveAutoApproveRule failed: %v", err)
	}

	rules = mgr.ListAutoApproveRules()
	if len(rules) != 0 {
		t.Fatalf("expected 0 rules after removal, got %d", len(rules))
	}
}

func TestAutoApproveRule_AttributeSubsetMatch(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})

	// Rule has specific attributes
	req := &Request{
		ID:   "req-1",
		Type: RequestTypeGetSecret,
		Items: []ItemInfo{{
			Path:       itemPath("default", "i1"),
			Attributes: map[string]string{"service": "gh:github.com"},
		}},
		SenderInfo: testSender("gh", "/usr/bin/gh"),
	}
	mustAddRule(t, mgr, req)

	// A request with more attributes than the rule names still matches.
	_, ok := mgr.CheckAutoApproveRules(RequestInfo{
		Items: []ItemInfo{{
			Path:       itemPath("default", "i2"),
			Attributes: map[string]string{"service": "gh:github.com", "user": "nb"},
		}},
		Type:       RequestTypeGetSecret,
		SenderInfo: testSender("gh", "/usr/bin/gh"),
	})
	if !ok {
		t.Fatal("expected subset attribute match")
	}

	// A request with a different attribute value does not match.
	_, ok = mgr.CheckAutoApproveRules(RequestInfo{
		Items: []ItemInfo{{
			Path:       itemPath("default", "i2"),
			Attributes: map[string]string{"service": "other:example.com"},
		}},
		Type:       RequestTypeGetSecret,
		SenderInfo: testSender("gh", "/usr/bin/gh"),
	})
	if ok {
		t.Fatal("expected no match for different attribute value")
	}
}

func TestAutoApproveRule_AttributeValuesAreLiteral(t *testing.T) {
	path := itemPath("default", "i1")
	covers := func(made, asked string) bool {
		mgr := newRuleManager()
		mustAddRule(t, mgr, &Request{
			ID:         "req-1",
			Type:       RequestTypeGetSecret,
			Items:      []ItemInfo{{Path: path, Attributes: map[string]string{"service": made}}},
			SenderInfo: testSender("gh", "/usr/bin/gh"),
		})
		_, ok := mgr.CheckAutoApproveRules(RequestInfo{
			Items:      []ItemInfo{{Path: path, Attributes: map[string]string{"service": asked}}},
			Type:       RequestTypeGetSecret,
			SenderInfo: testSender("gh", "/usr/bin/gh"),
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
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})

	req := &Request{
		ID:         "req-1",
		Type:       RequestTypeGetSecret,
		Items:      []ItemInfo{itemInCollection("default", "i1")},
		SenderInfo: testSender("gh", "/usr/bin/gh"),
	}

	id1 := mustAddRule(t, mgr, req)

	rules := mgr.ListAutoApproveRules()
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	expiry1 := rules[0].ExpiresAt

	// Adding the same rule again should dedup
	time.Sleep(10 * time.Millisecond)
	id2 := mustAddRule(t, mgr, req)

	if id1 != id2 {
		t.Errorf("expected same rule ID %s, got %s", id1, id2)
	}

	rules = mgr.ListAutoApproveRules()
	if len(rules) != 1 {
		t.Fatalf("expected still 1 rule after dedup, got %d", len(rules))
	}
	if !rules[0].ExpiresAt.After(expiry1) {
		t.Error("expected expiry to be refreshed")
	}
}

func TestAutoApproveRule_DifferentAttributesNotDeduped(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})

	req1 := &Request{
		ID:   "req-1",
		Type: RequestTypeWrite,
		Items: []ItemInfo{{
			Path:       itemPath("default", "i1"),
			Attributes: map[string]string{"service": "gh:github.com", "username": "nikicat"},
		}},
		SenderInfo: testSender("gh", "/usr/bin/gh"),
	}
	req2 := &Request{
		ID:   "req-2",
		Type: RequestTypeWrite,
		Items: []ItemInfo{{
			Path:       itemPath("default", "i2"),
			Attributes: map[string]string{"service": "gh:github.com", "username": ""},
		}},
		SenderInfo: testSender("gh", "/usr/bin/gh"),
	}

	id1 := mustAddRule(t, mgr, req1)
	id2 := mustAddRule(t, mgr, req2)

	if id1 == id2 {
		t.Error("expected different rule IDs for different attributes")
	}

	rules := mgr.ListAutoApproveRules()
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}
}

// Attribute maps of the same size but different keys are different scopes,
// even when the missing key of one would read as the other's empty value.
func TestAutoApproveRule_DifferentAttributeKeysNotDeduped(t *testing.T) {
	mgr := newRuleManager()

	writeRequest := func(id RequestID, attrs map[string]string) *Request {
		return &Request{
			ID:         id,
			Type:       RequestTypeWrite,
			Items:      []ItemInfo{{Path: itemPath("default", string(id)), Attributes: attrs}},
			SenderInfo: testSender("gh", "/usr/bin/gh"),
		}
	}
	scope1, err := writeRequest("i1", map[string]string{"a": ""}).AutoApproveScope()
	if err != nil {
		t.Fatalf("AutoApproveScope: %v", err)
	}
	scope2, err := writeRequest("i2", map[string]string{"b": "x"}).AutoApproveScope()
	if err != nil {
		t.Fatalf("AutoApproveScope: %v", err)
	}

	if scope1.Equal(scope2) {
		t.Error("scopes with attributes {a: \"\"} and {b: x} are equal")
	}
	mgr.AddAutoApproveRule(scope1, "gh")
	mgr.AddAutoApproveRule(scope2, "gh")
	if rules := mgr.ListAutoApproveRules(); len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}
}

// AddAutoApproveRule refreshing a rule and CheckAutoApproveRules share the
// rule list without a race. Run with -race.
func TestAutoApproveRule_RefreshRacesCheck(t *testing.T) {
	mgr := newRuleManager()
	req := &Request{
		ID:         "req-1",
		Type:       RequestTypeGetSecret,
		SenderInfo: testSender("gh", "/usr/bin/gh"),
	}
	scope, err := req.AutoApproveScope()
	if err != nil {
		t.Fatalf("AutoApproveScope: %v", err)
	}
	mgr.AddAutoApproveRule(scope, "gh")

	var wg sync.WaitGroup
	wg.Go(func() {
		for range 200 {
			mgr.AddAutoApproveRule(scope, "gh")
		}
	})
	wg.Go(func() {
		for range 200 {
			mgr.CheckAutoApproveRules(RequestInfo{Type: RequestTypeGetSecret, SenderInfo: testSender("gh", "/usr/bin/gh")})
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
