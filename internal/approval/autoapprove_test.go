package approval

import (
	"context"
	"testing"
	"time"
)

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
		SenderInfo: SenderInfo{UnitName: "gh"},
	}
	mgr.AddAutoApproveRule(req)

	// Retry with same invoker, type, collection, attributes → should match
	rule := mgr.checkAutoApproveRules(
		SenderInfo{UnitName: "gh"},
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

func TestAutoApproveRule_DifferentInvokerNoMatch(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100, AutoApproveDuration: 2 * time.Minute})

	req := &Request{
		ID:     "req-1",
		Type:   RequestTypeGetSecret,
		Items:  []ItemInfo{{Path: "/org/freedesktop/secrets/collection/default/i1"}},
		SenderInfo: SenderInfo{UnitName: "gh"},
	}
	mgr.AddAutoApproveRule(req)

	rule := mgr.checkAutoApproveRules(
		SenderInfo{UnitName: "seahorse"},
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
		ID:     "req-1",
		Type:   RequestTypeGetSecret,
		Items:  []ItemInfo{{Path: "/org/freedesktop/secrets/collection/default/i1"}},
		SenderInfo: SenderInfo{UnitName: "gh"},
	}
	mgr.AddAutoApproveRule(req)

	time.Sleep(60 * time.Millisecond)

	rule := mgr.checkAutoApproveRules(
		SenderInfo{UnitName: "gh"},
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
		ID:     "req-1",
		Type:   RequestTypeGetSecret,
		Items:  []ItemInfo{{Path: "/org/freedesktop/secrets/collection/default/i1", Attributes: map[string]string{"service": "gh:github.com"}}},
		SenderInfo: SenderInfo{UnitName: "gh"},
	}
	mgr.AddAutoApproveRule(req)

	// RequireApproval should return nil immediately (auto-approved)
	err := mgr.RequireApproval(
		context.Background(),
		"test-client",
		[]ItemInfo{{Path: "/org/freedesktop/secrets/collection/default/i99", Attributes: map[string]string{"service": "gh:github.com"}}},
		"session",
		RequestTypeGetSecret,
		nil,
		SenderInfo{UnitName: "gh"},
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
		SenderInfo: SenderInfo{UnitName: "gh"},
	}
	ruleID := mgr.AddAutoApproveRule(req)

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
		SenderInfo: SenderInfo{UnitName: "gh"},
	}
	mgr.AddAutoApproveRule(req)

	// Request with superset attributes → should match
	rule := mgr.checkAutoApproveRules(
		SenderInfo{UnitName: "gh"},
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
		SenderInfo{UnitName: "gh"},
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
		SenderInfo: SenderInfo{UnitName: "gh"},
	}

	id1 := mgr.AddAutoApproveRule(req)

	rules := mgr.ListAutoApproveRules()
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	expiry1 := rules[0].ExpiresAt

	// Adding the same rule again should dedup
	time.Sleep(10 * time.Millisecond)
	id2 := mgr.AddAutoApproveRule(req)

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
		SenderInfo: SenderInfo{UnitName: "gh"},
	}
	req2 := &Request{
		ID:   "req-2",
		Type: RequestTypeWrite,
		Items: []ItemInfo{{
			Path:       "/org/freedesktop/secrets/collection/default/i2",
			Attributes: map[string]string{"service": "gh:github.com", "username": ""},
		}},
		SenderInfo: SenderInfo{UnitName: "gh"},
	}

	id1 := mgr.AddAutoApproveRule(req1)
	id2 := mgr.AddAutoApproveRule(req2)

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
