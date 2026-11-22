package approval

import (
	"context"
	"errors"
	"github.com/nikicat/secrets-dispatcher/internal/procutil"
	"github.com/nikicat/secrets-dispatcher/internal/sender"
	"github.com/stretchr/testify/assert"
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
func mustAddRule(t *testing.T, mgr *Manager, req *Request) string {
	t.Helper()
	rule, err := req.AutoApproveRule()
	if err != nil {
		t.Fatalf("AutoApproveRule: %v", err)
	}
	return mgr.AddAutoApproveRule(rule)
}

func newRuleManager() *Manager {
	return NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
}

func waitPendingID(t *testing.T, mgr *Manager) RequestID {
	t.Helper()
	for range 100 {
		if reqs := mgr.List(); len(reqs) > 0 {
			return reqs[0].ID
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("request did not appear in pending list")
	return ""
}

func TestRequestAutoApproveRule_RefusesRequestWithoutExe(t *testing.T) {
	req := &Request{
		ID:         "remote-1",
		Type:       RequestTypeGetSecret,
		SenderInfo: SenderInfo{Client: "remote:laptop", Info: sender.Info{InvokerName: "gh"}},
	}

	_, err := req.AutoApproveRule()
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
	if mgr.GetPending(id) == nil {
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
	rule := mgr.CheckAutoApproveRules(RequestInfo{
		Items: []ItemInfo{{
			Path:       itemPath("default", "i99"),
			Attributes: map[string]string{"service": "gh:github.com", "extra": "val"},
		}},
		Type:       RequestTypeWrite,
		SenderInfo: testSender("gh", "/usr/bin/gh"),
	})
	if rule == nil {
		t.Fatal("expected auto-approve rule to match retry request")
	}
}

// A process that sets its comm to a trusted app's name but runs another binary
// is not auto-approved, since rules match on the exe (ADR 0005).
func TestAutoApproveRule_CommSpoofDoesNotMatch(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})

	// User approved "always allow" for the real node binary reading login secrets.
	mustAddRule(t, mgr, &Request{
		Type:       RequestTypeGetSecret,
		Items:      []ItemInfo{itemInCollection("login", "github-token")},
		SenderInfo: testSender("node", "/usr/bin/node"),
	})

	// Malware sets comm to "node" but runs /tmp/malware.
	spoofed := testSender("node", "/tmp/malware")
	rule := mgr.CheckAutoApproveRules(RequestInfo{
		Items:      []ItemInfo{itemInCollection("login", "github-token")},
		Type:       RequestTypeGetSecret,
		SenderInfo: spoofed,
	})
	if rule != nil {
		t.Fatal("comm-spoofed caller with a different exe must not match the auto-approve rule")
	}

	// The genuine binary still matches.
	rule = mgr.CheckAutoApproveRules(RequestInfo{
		Items:      []ItemInfo{itemInCollection("login", "github-token")},
		Type:       RequestTypeGetSecret,
		SenderInfo: testSender("node", "/usr/bin/node"),
	})
	if rule == nil {
		t.Fatal("the genuine invoker exe should still match")
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

	rule := mgr.CheckAutoApproveRules(RequestInfo{
		Items:      []ItemInfo{itemInCollection("default", "i1")},
		Type:       RequestTypeGetSecret,
		SenderInfo: testSender("seahorse", "/usr/bin/seahorse"),
	})
	if rule != nil {
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

	rule := mgr.CheckAutoApproveRules(RequestInfo{
		Items:      []ItemInfo{itemInCollection("default", "i1")},
		Type:       RequestTypeGetSecret,
		SenderInfo: testSender("gh", "/usr/bin/gh"),
	})
	if rule != nil {
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
	rule := mgr.CheckAutoApproveRules(RequestInfo{
		Items: []ItemInfo{{
			Path:       itemPath("default", "i2"),
			Attributes: map[string]string{"service": "gh:github.com", "user": "nb"},
		}},
		Type:       RequestTypeGetSecret,
		SenderInfo: testSender("gh", "/usr/bin/gh"),
	})
	if rule == nil {
		t.Fatal("expected subset attribute match")
	}

	// A request with a different attribute value does not match.
	rule = mgr.CheckAutoApproveRules(RequestInfo{
		Items: []ItemInfo{{
			Path:       itemPath("default", "i2"),
			Attributes: map[string]string{"service": "other:example.com"},
		}},
		Type:       RequestTypeGetSecret,
		SenderInfo: testSender("gh", "/usr/bin/gh"),
	})
	if rule != nil {
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
		return mgr.CheckAutoApproveRules(RequestInfo{
			Items:      []ItemInfo{{Path: path, Attributes: map[string]string{"service": asked}}},
			Type:       RequestTypeGetSecret,
			SenderInfo: testSender("gh", "/usr/bin/gh"),
		}) != nil
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
	rule1, err := writeRequest("i1", map[string]string{"a": ""}).AutoApproveRule()
	if err != nil {
		t.Fatalf("AutoApproveRule: %v", err)
	}
	rule2, err := writeRequest("i2", map[string]string{"b": "x"}).AutoApproveRule()
	if err != nil {
		t.Fatalf("AutoApproveRule: %v", err)
	}

	if rule1.sameScope(rule2) {
		t.Error("rules with attributes {a: \"\"} and {b: x} share a scope")
	}
	mgr.AddAutoApproveRule(rule1)
	mgr.AddAutoApproveRule(rule2)
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
	rule, err := req.AutoApproveRule()
	if err != nil {
		t.Fatalf("AutoApproveRule: %v", err)
	}
	mgr.AddAutoApproveRule(rule)

	var wg sync.WaitGroup
	wg.Go(func() {
		for range 200 {
			mgr.AddAutoApproveRule(rule)
		}
	})
	wg.Go(func() {
		for range 200 {
			mgr.CheckAutoApproveRules(RequestInfo{Type: RequestTypeGetSecret, SenderInfo: testSender("gh", "/usr/bin/gh")})
		}
	})
	wg.Wait()
}
