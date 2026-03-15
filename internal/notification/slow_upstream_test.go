package notification

import (
	"testing"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

func TestSlowUpstreamNotifier_SingleItem(t *testing.T) {
	mock := &mockNotifier{}
	n := newSlowUpstreamNotifier(mock)

	items := []approval.ItemInfo{{Label: "my-secret", Path: "/test/item"}}
	dismiss := n.NotifySlowUpstream("", items)

	mock.mu.Lock()
	if len(mock.notified) != 1 {
		t.Fatalf("expected 1 notify call, got %d", len(mock.notified))
	}
	call := mock.notified[0]
	if call.summary != "Waiting for keyring unlock" {
		t.Errorf("unexpected summary: %q", call.summary)
	}
	if call.body != "my-secret" {
		t.Errorf("unexpected body: %q", call.body)
	}
	if call.icon != "dialog-password" {
		t.Errorf("unexpected icon: %q", call.icon)
	}
	if call.actions != nil {
		t.Errorf("expected nil actions (informational), got %v", call.actions)
	}
	notifID := mock.nextID
	mock.mu.Unlock()

	dismiss()

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.closed) != 1 || mock.closed[0] != notifID {
		t.Errorf("expected Close(%d), got %v", notifID, mock.closed)
	}
}

func TestSlowUpstreamNotifier_MultipleItems(t *testing.T) {
	mock := &mockNotifier{}
	n := newSlowUpstreamNotifier(mock)

	items := []approval.ItemInfo{
		{Label: "secret-a"},
		{Label: "secret-b"},
		{Label: "secret-c"},
	}
	dismiss := n.NotifySlowUpstream("", items)
	defer dismiss()

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.notified) != 1 {
		t.Fatalf("expected 1 notify call, got %d", len(mock.notified))
	}
	if mock.notified[0].body != "secret-a, secret-b, secret-c (3 items)" {
		t.Errorf("unexpected body: %q", mock.notified[0].body)
	}
}

func TestSlowUpstreamNotifier_EmptyItems(t *testing.T) {
	mock := &mockNotifier{}
	n := newSlowUpstreamNotifier(mock)

	dismiss := n.NotifySlowUpstream("", nil)
	defer dismiss()

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if len(mock.notified) != 1 {
		t.Fatalf("expected 1 notify call, got %d", len(mock.notified))
	}
	if mock.notified[0].body != "" {
		t.Errorf("expected empty body for nil items, got %q", mock.notified[0].body)
	}
}

func TestSlowUpstreamNotifier_GPGSignSummary(t *testing.T) {
	mock := &mockNotifier{}
	n := newSlowUpstreamNotifier(mock)

	dismiss := n.NotifySlowUpstream(approval.RequestTypeGPGSign, []approval.ItemInfo{{Label: "my-repo: fix bug"}})
	defer dismiss()

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if mock.notified[0].summary != "Signing commit" {
		t.Errorf("expected summary 'Signing commit', got %q", mock.notified[0].summary)
	}
}

func TestSlowUpstreamNotifier_SSHSignSummary(t *testing.T) {
	mock := &mockNotifier{}
	n := newSlowUpstreamNotifier(mock)

	dismiss := n.NotifySlowUpstream(approval.RequestTypeSSHSign, nil)
	defer dismiss()

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if mock.notified[0].summary != "SSH key signing" {
		t.Errorf("expected summary 'SSH key signing', got %q", mock.notified[0].summary)
	}
}

func TestSlowUpstreamNotifier_SearchSummary(t *testing.T) {
	mock := &mockNotifier{}
	n := newSlowUpstreamNotifier(mock)

	dismiss := n.NotifySlowUpstream(approval.RequestTypeSearch, nil)
	defer dismiss()

	mock.mu.Lock()
	defer mock.mu.Unlock()
	if mock.notified[0].summary != "Searching keyring" {
		t.Errorf("expected summary 'Searching keyring', got %q", mock.notified[0].summary)
	}
}
