package approval

import (
	"context"
	"errors"
	"github.com/nikicat/secrets-dispatcher/internal/procutil"
	"github.com/nikicat/secrets-dispatcher/internal/sender"
	"sync"
	"testing"
	"time"
)

// Returns a sender whose invoker carries comm and exe, as the daemon resolves a
// live caller. Auto-approve rules match only on the exe.
func testSender(comm string, exe procutil.Path) SenderInfo {
	const pid = 4242
	return SenderInfo{
		PID:          pid,
		InvokerName:  comm,
		ProcessChain: []procutil.Process{{Name: comm, PID: pid, Exe: &exe}},
	}
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
	return NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100, AutoApproveDuration: 2 * time.Minute})
}

func waitPendingID(t *testing.T, mgr *Manager) string {
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
		Client:     "remote:laptop",
		Type:       RequestTypeGetSecret,
		SenderInfo: SenderInfo{Info: sender.Info{InvokerName: "gh"}},
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
		_, approvalErr = mgr.RequireApproval(context.Background(), nil, "session",
			RequestTypeGetSecret, nil, SenderInfo{InvokerName: "gh"})
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
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100, AutoApproveDuration: 2 * time.Minute})

	// Simulate a cancelled request
	req := &Request{
		ID:     "req-1",
		Client: "test",
		Type:   RequestTypeWrite,
		Items: []ItemInfo{{
			Path:       "/org/freedesktop/secrets/collection/default/i1",
			Label:      "gh:github.com",
			Attributes: map[string]string{"service": "gh:github.com"},
		}},
		SenderInfo: testSender("gh", "/usr/bin/gh"),
	}
	mustAddRule(t, mgr, req)

	// Retry with same invoker, type, collection, attributes → should match
	rule := mgr.checkAutoApproveRules(
		testSender("gh", "/usr/bin/gh"),
		[]ItemInfo{{
			Path:       "/org/freedesktop/secrets/collection/default/i99",
			Attributes: map[string]string{"service": "gh:github.com", "extra": "val"},
		}},
		RequestTypeWrite,
	)
	if rule == nil {
		t.Fatal("expected auto-approve rule to match retry request")
	}
}

// TestAutoApproveRule_CommSpoofDoesNotMatch is the regression test for Vuln 4:
// an ephemeral auto-approve rule must key on the non-spoofable invoker exe, so a
// process that merely sets its comm (InvokerName) to impersonate the trusted app —
// while running a different real binary — is not auto-approved.
func TestAutoApproveRule_CommSpoofDoesNotMatch(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100, AutoApproveDuration: 2 * time.Minute})

	// User approved "always allow" for the real node binary reading login secrets.
	mustAddRule(t, mgr, &Request{
		Type:       RequestTypeGetSecret,
		Items:      []ItemInfo{{Path: "/org/freedesktop/secrets/collection/login/github-token"}},
		SenderInfo: testSender("node", "/usr/bin/node"),
	})

	// Malware sets comm to "node" but runs /tmp/malware.
	spoofed := testSender("node", "/tmp/malware")
	rule := mgr.checkAutoApproveRules(
		spoofed,
		[]ItemInfo{{Path: "/org/freedesktop/secrets/collection/login/github-token"}},
		RequestTypeGetSecret,
	)
	if rule != nil {
		t.Fatal("comm-spoofed caller with a different exe must not match the auto-approve rule")
	}

	// The genuine binary still matches.
	rule = mgr.checkAutoApproveRules(
		testSender("node", "/usr/bin/node"),
		[]ItemInfo{{Path: "/org/freedesktop/secrets/collection/login/github-token"}},
		RequestTypeGetSecret,
	)
	if rule == nil {
		t.Fatal("the genuine invoker exe should still match")
	}
}

func TestAutoApproveRule_DifferentInvokerNoMatch(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100, AutoApproveDuration: 2 * time.Minute})

	req := &Request{
		ID:         "req-1",
		Type:       RequestTypeGetSecret,
		Items:      []ItemInfo{{Path: "/org/freedesktop/secrets/collection/default/i1"}},
		SenderInfo: testSender("gh", "/usr/bin/gh"),
	}
	mustAddRule(t, mgr, req)

	rule := mgr.checkAutoApproveRules(
		testSender("seahorse", "/usr/bin/seahorse"),
		[]ItemInfo{{Path: "/org/freedesktop/secrets/collection/default/i1"}},
		RequestTypeGetSecret,
	)
	if rule != nil {
		t.Fatal("expected no match for different invoker")
	}
}

func TestAutoApproveRule_Expiry(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100, AutoApproveDuration: 50 * time.Millisecond})

	req := &Request{
		ID:         "req-1",
		Type:       RequestTypeGetSecret,
		Items:      []ItemInfo{{Path: "/org/freedesktop/secrets/collection/default/i1"}},
		SenderInfo: testSender("gh", "/usr/bin/gh"),
	}
	mustAddRule(t, mgr, req)

	time.Sleep(60 * time.Millisecond)

	rule := mgr.checkAutoApproveRules(
		testSender("gh", "/usr/bin/gh"),
		[]ItemInfo{{Path: "/org/freedesktop/secrets/collection/default/i1"}},
		RequestTypeGetSecret,
	)
	if rule != nil {
		t.Fatal("expected expired rule not to match")
	}

	// Expired rules should be cleaned up
	if len(mgr.ListAutoApproveRules()) != 0 {
		t.Fatal("expected expired rules to be cleaned up")
	}
}

func TestAutoApproveRule_IntegrationWithRequireApproval(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100, AutoApproveDuration: 2 * time.Minute})

	// Add a rule
	req := &Request{
		ID:         "req-1",
		Type:       RequestTypeGetSecret,
		Items:      []ItemInfo{{Path: "/org/freedesktop/secrets/collection/default/i1", Attributes: map[string]string{"service": "gh:github.com"}}},
		SenderInfo: testSender("gh", "/usr/bin/gh"),
	}
	mustAddRule(t, mgr, req)

	// RequireApproval should return nil immediately (auto-approved)
	_, err := mgr.RequireApproval(
		context.Background(),
		[]ItemInfo{{Path: "/org/freedesktop/secrets/collection/default/i99", Attributes: map[string]string{"service": "gh:github.com"}}},
		"session",
		RequestTypeGetSecret,
		nil,
		testSender("gh", "/usr/bin/gh"),
	)
	if err != nil {
		t.Fatalf("expected auto-approved, got: %v", err)
	}
}

func TestAutoApproveRule_ListAndRemove(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100, AutoApproveDuration: 2 * time.Minute})

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
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100, AutoApproveDuration: 2 * time.Minute})

	// Rule has specific attributes
	req := &Request{
		ID:   "req-1",
		Type: RequestTypeGetSecret,
		Items: []ItemInfo{{
			Path:       "/org/freedesktop/secrets/collection/default/i1",
			Attributes: map[string]string{"service": "gh:github.com"},
		}},
		SenderInfo: testSender("gh", "/usr/bin/gh"),
	}
	mustAddRule(t, mgr, req)

	// Request with superset attributes → should match
	rule := mgr.checkAutoApproveRules(
		testSender("gh", "/usr/bin/gh"),
		[]ItemInfo{{
			Path:       "/org/freedesktop/secrets/collection/default/i2",
			Attributes: map[string]string{"service": "gh:github.com", "user": "nb"},
		}},
		RequestTypeGetSecret,
	)
	if rule == nil {
		t.Fatal("expected subset attribute match")
	}

	// Request with different attribute value → should NOT match
	rule = mgr.checkAutoApproveRules(
		testSender("gh", "/usr/bin/gh"),
		[]ItemInfo{{
			Path:       "/org/freedesktop/secrets/collection/default/i2",
			Attributes: map[string]string{"service": "other:example.com"},
		}},
		RequestTypeGetSecret,
	)
	if rule != nil {
		t.Fatal("expected no match for different attribute value")
	}
}

func TestAutoApproveRule_DedupRefreshesExpiry(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100, AutoApproveDuration: 2 * time.Minute})

	req := &Request{
		ID:         "req-1",
		Type:       RequestTypeGetSecret,
		Items:      []ItemInfo{{Path: "/org/freedesktop/secrets/collection/default/i1"}},
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
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100, AutoApproveDuration: 2 * time.Minute})

	req1 := &Request{
		ID:   "req-1",
		Type: RequestTypeWrite,
		Items: []ItemInfo{{
			Path:       "/org/freedesktop/secrets/collection/default/i1",
			Attributes: map[string]string{"service": "gh:github.com", "username": "nikicat"},
		}},
		SenderInfo: testSender("gh", "/usr/bin/gh"),
	}
	req2 := &Request{
		ID:   "req-2",
		Type: RequestTypeWrite,
		Items: []ItemInfo{{
			Path:       "/org/freedesktop/secrets/collection/default/i2",
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

func TestExtractCollection(t *testing.T) {
	tests := []struct {
		path string
		want string
	}{
		{"/org/freedesktop/secrets/collection/default/i123", "default"},
		{"/org/freedesktop/secrets/collection/login/abc", "login"},
		{"/org/freedesktop/secrets/collection/mykeys", "mykeys"},
		{"/org/freedesktop/secrets/aliases/default", "default"},
		{"", ""},
	}
	for _, tt := range tests {
		got := extractCollection(tt.path)
		if got != tt.want {
			t.Errorf("extractCollection(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

// Reads every rule it is told about, as the WebSocket broadcaster does when it
// marshals one.
type ruleReader struct{}

func (ruleReader) OnEvent(e Event) {
	if e.Rule != nil {
		_ = *e.Rule
	}
}

// A refreshed rule reaches observers as a copy: checkAutoApproveRules rewrites
// the rule list in place while they read it. Run with -race.
func TestAutoApproveRule_RefreshHandsObserversACopy(t *testing.T) {
	mgr := newRuleManager()
	mgr.Subscribe(ruleReader{})
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
			mgr.checkAutoApproveRules(testSender("gh", "/usr/bin/gh"), nil, RequestTypeGetSecret)
		}
	})
	wg.Wait()
}
