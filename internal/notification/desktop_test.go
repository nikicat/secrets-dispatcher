package notification

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

// mockNotifier records calls for testing.
type mockNotifier struct {
	mu        sync.Mutex
	nextID    uint32
	notified  []notifyCall
	closed    []uint32
	notifyErr error
	closeErr  error
}

type notifyCall struct {
	summary string
	body    string
	icon    string
	actions []string
}

func (m *mockNotifier) Notify(summary, body, icon string, actions []string) (uint32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.notifyErr != nil {
		return 0, m.notifyErr
	}
	m.nextID++
	m.notified = append(m.notified, notifyCall{summary, body, icon, actions})
	return m.nextID, nil
}

func (m *mockNotifier) Close(id uint32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closeErr != nil {
		return m.closeErr
	}
	m.closed = append(m.closed, id)
	return nil
}

func (m *mockNotifier) notifyCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.notified)
}

func (m *mockNotifier) closeCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.closed)
}

func (m *mockNotifier) lastNotify() notifyCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.notified) == 0 {
		return notifyCall{}
	}
	return m.notified[len(m.notified)-1]
}

func (m *mockNotifier) lastClosed() uint32 {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.closed) == 0 {
		return 0
	}
	return m.closed[len(m.closed)-1]
}

// mockApprover records approve/deny calls.
type mockApprover struct {
	mu       sync.Mutex
	approved []string
	denied   []string
	err      error
}

func (a *mockApprover) Approve(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return a.err
	}
	a.approved = append(a.approved, id)
	return nil
}

func (a *mockApprover) Deny(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return a.err
	}
	a.denied = append(a.denied, id)
	return nil
}

func newTestHandler() (*Handler, *mockNotifier, *mockApprover) {
	mock := &mockNotifier{}
	approver := &mockApprover{}
	h := NewHandler(mock, approver, "http://127.0.0.1:8484")
	return h, mock, approver
}

func TestHandler_OnEvent_RequestCreated(t *testing.T) {
	h, mock, _ := newTestHandler()

	req := &approval.Request{
		ID:     "test-123",
		Client: "user@remote",
		Type:   approval.RequestTypeGetSecret,
		Items: []approval.ItemInfo{
			{Label: "GitHub Token", Path: "/org/secrets/github"},
		},
		SenderInfo: approval.SenderInfo{
			PID:      1234,
			UnitName: "ssh-agent.service",
		},
	}

	h.OnEvent(approval.Event{Type: approval.EventRequestCreated, Request: req})

	if mock.notifyCount() != 1 {
		t.Errorf("expected 1 notification, got %d", mock.notifyCount())
	}

	call := mock.lastNotify()
	if call.summary != "Secret Request" {
		t.Errorf("unexpected summary: %s", call.summary)
	}
	if !contains(call.body, "user@remote") {
		t.Errorf("body should contain client name: %s", call.body)
	}
	if !contains(call.body, "GitHub Token") {
		t.Errorf("body should contain secret label: %s", call.body)
	}
	if !contains(call.body, "ssh-agent.service") {
		t.Errorf("body should contain unit name: %s", call.body)
	}
}

func TestHandler_OnEvent_RequestCreated_Actions(t *testing.T) {
	h, mock, _ := newTestHandler()

	req := &approval.Request{
		ID:     "test-actions",
		Client: "user@remote",
		Type:   approval.RequestTypeGetSecret,
		Items:  []approval.ItemInfo{{Label: "Secret"}},
	}

	h.OnEvent(approval.Event{Type: approval.EventRequestCreated, Request: req})

	call := mock.lastNotify()
	wantActions := []string{"default", "", "approve", "Approve", "deny", "Deny"}
	if len(call.actions) != len(wantActions) {
		t.Fatalf("expected %d actions, got %d", len(wantActions), len(call.actions))
	}
	for i, a := range wantActions {
		if call.actions[i] != a {
			t.Errorf("action[%d]: want %q, got %q", i, a, call.actions[i])
		}
	}
}

func TestHandler_OnEvent_RequestResolved_ClosesNotification(t *testing.T) {
	h, mock, _ := newTestHandler()

	req := &approval.Request{
		ID:     "test-456",
		Client: "user@remote",
		Type:   approval.RequestTypeGetSecret,
		Items:  []approval.ItemInfo{{Label: "Secret"}},
	}

	// Create notification
	h.OnEvent(approval.Event{Type: approval.EventRequestCreated, Request: req})

	if mock.notifyCount() != 1 {
		t.Fatalf("expected 1 notification, got %d", mock.notifyCount())
	}

	// Approve should close it
	h.OnEvent(approval.Event{Type: approval.EventRequestApproved, Request: req})

	// Give async handler time to complete
	time.Sleep(10 * time.Millisecond)

	if mock.closeCount() != 1 {
		t.Errorf("expected 1 close call, got %d", mock.closeCount())
	}
	if mock.lastClosed() != 1 {
		t.Errorf("expected to close notification ID 1, got %d", mock.lastClosed())
	}
}

func TestHandler_OnEvent_AllResolutionTypes(t *testing.T) {
	resolutions := map[string]approval.EventType{
		"approved":  approval.EventRequestApproved,
		"denied":    approval.EventRequestDenied,
		"expired":   approval.EventRequestExpired,
		"cancelled": approval.EventRequestCancelled,
	}

	for name, resolution := range resolutions {
		t.Run(name, func(t *testing.T) {
			h, mock, _ := newTestHandler()

			req := &approval.Request{
				ID:     "test-req",
				Client: "client",
				Type:   approval.RequestTypeGetSecret,
				Items:  []approval.ItemInfo{{Label: "Secret"}},
			}

			h.OnEvent(approval.Event{Type: approval.EventRequestCreated, Request: req})
			h.OnEvent(approval.Event{Type: resolution, Request: req})

			time.Sleep(10 * time.Millisecond)

			if mock.closeCount() != 1 {
				t.Errorf("expected notification to be closed for %v", resolution)
			}
		})
	}
}

func TestHandler_OnEvent_ResolveWithoutCreate(t *testing.T) {
	h, mock, _ := newTestHandler()

	req := &approval.Request{ID: "unknown"}

	// Should not panic or call close
	h.OnEvent(approval.Event{Type: approval.EventRequestApproved, Request: req})

	if mock.closeCount() != 0 {
		t.Errorf("should not close unknown notification")
	}
}

func TestHandler_FormatBody_Search(t *testing.T) {
	h, mock, _ := newTestHandler()

	req := &approval.Request{
		ID:     "search-1",
		Client: "searcher@host",
		Type:   approval.RequestTypeSearch,
		SearchAttributes: map[string]string{
			"service": "github",
			"user":    "admin",
		},
	}

	h.OnEvent(approval.Event{Type: approval.EventRequestCreated, Request: req})

	call := mock.lastNotify()
	if !contains(call.body, "search") {
		t.Errorf("body should mention search: %s", call.body)
	}
	if !contains(call.body, "github") {
		t.Errorf("body should contain search attribute: %s", call.body)
	}
}

func TestHandler_FormatBody_MultipleItems(t *testing.T) {
	h, mock, _ := newTestHandler()

	req := &approval.Request{
		ID:     "multi-1",
		Client: "user@host",
		Type:   approval.RequestTypeGetSecret,
		Items: []approval.ItemInfo{
			{Label: "Secret1"},
			{Label: "Secret2"},
			{Label: "Secret3"},
		},
	}

	h.OnEvent(approval.Event{Type: approval.EventRequestCreated, Request: req})

	call := mock.lastNotify()
	if !contains(call.body, "3 items") {
		t.Errorf("body should show item count: %s", call.body)
	}
}

func TestHandler_FormatBody_PIDOnly(t *testing.T) {
	h, mock, _ := newTestHandler()

	req := &approval.Request{
		ID:     "pid-1",
		Client: "user@host",
		Type:   approval.RequestTypeGetSecret,
		Items:  []approval.ItemInfo{{Label: "Secret"}},
		SenderInfo: approval.SenderInfo{
			PID: 5678,
			// No UnitName
		},
	}

	h.OnEvent(approval.Event{Type: approval.EventRequestCreated, Request: req})

	call := mock.lastNotify()
	if !contains(call.body, "PID: 5678") {
		t.Errorf("body should show PID: %s", call.body)
	}
}

func TestHandler_OnEvent_GPGSignRequest(t *testing.T) {
	h, mock, _ := newTestHandler()

	req := &approval.Request{
		ID:     "gpg-1",
		Client: "user@host",
		Type:   approval.RequestTypeGPGSign,
		GPGSignInfo: &approval.GPGSignInfo{
			RepoName:     "my-project",
			CommitMsg:    "Add feature\n\nSome body text",
			Author:       "John",
			KeyID:        "ABCD1234",
			ChangedFiles: []string{"a.go", "b.go"},
		},
		SenderInfo: approval.SenderInfo{
			PID:      1234,
			UnitName: "ssh-agent.service",
		},
	}

	h.OnEvent(approval.Event{Type: approval.EventRequestCreated, Request: req})

	if mock.notifyCount() != 1 {
		t.Fatalf("expected 1 notification, got %d", mock.notifyCount())
	}

	call := mock.lastNotify()
	if call.summary != "Sign commit" {
		t.Errorf("expected summary 'Sign commit', got %q", call.summary)
	}
	if call.icon != "emblem-important" {
		t.Errorf("expected icon 'emblem-important', got %q", call.icon)
	}
	if !contains(call.body, "<b>ssh-agent.service@my-project</b>: ") {
		t.Errorf("body should contain bold process@repo: %s", call.body)
	}
	if !contains(call.body, "<i>Add feature</i>") {
		t.Errorf("body should contain italic commit subject: %s", call.body)
	}
	if contains(call.body, "Some body text") {
		t.Errorf("body should NOT contain commit body text: %s", call.body)
	}
}

func TestHandler_FormatBody_GPGSign_PIDOnly(t *testing.T) {
	h, mock, _ := newTestHandler()

	req := &approval.Request{
		ID:     "gpg-pid-1",
		Client: "user@host",
		Type:   approval.RequestTypeGPGSign,
		GPGSignInfo: &approval.GPGSignInfo{
			RepoName:  "my-project",
			CommitMsg: "Fix bug",
		},
		SenderInfo: approval.SenderInfo{
			PID: 5678,
		},
	}

	h.OnEvent(approval.Event{Type: approval.EventRequestCreated, Request: req})

	call := mock.lastNotify()
	if !contains(call.body, "<b>my-project</b>: ") {
		t.Errorf("body should contain bold repo without process name: %s", call.body)
	}
	if !contains(call.body, "<i>Fix bug</i>") {
		t.Errorf("body should contain italic commit subject: %s", call.body)
	}
}

func TestHandler_OnEvent_GetSecretIcon(t *testing.T) {
	h, mock, _ := newTestHandler()

	req := &approval.Request{
		ID:     "secret-icon-1",
		Client: "user@host",
		Type:   approval.RequestTypeGetSecret,
		Items:  []approval.ItemInfo{{Label: "MySecret"}},
	}

	h.OnEvent(approval.Event{Type: approval.EventRequestCreated, Request: req})

	if mock.notifyCount() != 1 {
		t.Fatalf("expected 1 notification, got %d", mock.notifyCount())
	}

	call := mock.lastNotify()
	if call.summary != "Secret Request" {
		t.Errorf("expected summary 'Secret Request', got %q", call.summary)
	}
	if call.icon != "dialog-password" {
		t.Errorf("expected icon 'dialog-password', got %q", call.icon)
	}
}

func TestHandler_ListenActions_Approve(t *testing.T) {
	h, mock, approver := newTestHandler()

	req := &approval.Request{
		ID:     "action-approve-1",
		Client: "user@host",
		Type:   approval.RequestTypeGetSecret,
		Items:  []approval.ItemInfo{{Label: "Secret"}},
	}

	h.OnEvent(approval.Event{Type: approval.EventRequestCreated, Request: req})
	notifID := mock.lastNotify() // get the notification ID
	_ = notifID

	// Get actual notification ID from the handler's map
	h.mu.Lock()
	nID := h.notifications["action-approve-1"]
	h.mu.Unlock()

	actions := make(chan Action, 1)
	actions <- Action{NotificationID: nID, ActionKey: "approve"}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		h.ListenActions(ctx, actions)
		close(done)
	}()

	// Wait for the action to be processed
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	approver.mu.Lock()
	defer approver.mu.Unlock()
	if len(approver.approved) != 1 || approver.approved[0] != "action-approve-1" {
		t.Errorf("expected approve for 'action-approve-1', got %v", approver.approved)
	}
}

func TestHandler_ListenActions_Deny(t *testing.T) {
	h, mock, approver := newTestHandler()

	req := &approval.Request{
		ID:     "action-deny-1",
		Client: "user@host",
		Type:   approval.RequestTypeGetSecret,
		Items:  []approval.ItemInfo{{Label: "Secret"}},
	}

	h.OnEvent(approval.Event{Type: approval.EventRequestCreated, Request: req})
	_ = mock

	h.mu.Lock()
	nID := h.notifications["action-deny-1"]
	h.mu.Unlock()

	actions := make(chan Action, 1)
	actions <- Action{NotificationID: nID, ActionKey: "deny"}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		h.ListenActions(ctx, actions)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	approver.mu.Lock()
	defer approver.mu.Unlock()
	if len(approver.denied) != 1 || approver.denied[0] != "action-deny-1" {
		t.Errorf("expected deny for 'action-deny-1', got %v", approver.denied)
	}
}

func TestHandler_ListenActions_DefaultOpensURL(t *testing.T) {
	h, _, approver := newTestHandler()

	var opened string
	h.openURL = func(u string) { opened = u }

	req := &approval.Request{
		ID:     "action-default-1",
		Client: "user@host",
		Type:   approval.RequestTypeGetSecret,
		Items:  []approval.ItemInfo{{Label: "Secret"}},
	}

	h.OnEvent(approval.Event{Type: approval.EventRequestCreated, Request: req})

	h.mu.Lock()
	nID := h.notifications["action-default-1"]
	h.mu.Unlock()

	actions := make(chan Action, 1)
	actions <- Action{NotificationID: nID, ActionKey: "default"}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		h.ListenActions(ctx, actions)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	if opened != "http://127.0.0.1:8484?request=action-default-1" {
		t.Errorf("expected openURL called with request URL, got %q", opened)
	}

	approver.mu.Lock()
	defer approver.mu.Unlock()
	if len(approver.approved) != 0 || len(approver.denied) != 0 {
		t.Errorf("default action should not call approve/deny")
	}
}

func TestHandler_ListenActions_UnknownNotification(t *testing.T) {
	h, _, approver := newTestHandler()

	actions := make(chan Action, 1)
	actions <- Action{NotificationID: 9999, ActionKey: "approve"}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		h.ListenActions(ctx, actions)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	approver.mu.Lock()
	defer approver.mu.Unlock()
	if len(approver.approved) != 0 {
		t.Errorf("should not approve unknown notification, got %v", approver.approved)
	}
}

func TestHandler_ListenActions_ApproverError(t *testing.T) {
	h, _, approver := newTestHandler()
	approver.err = fmt.Errorf("not found")

	req := &approval.Request{
		ID:     "err-1",
		Client: "user@host",
		Type:   approval.RequestTypeGetSecret,
		Items:  []approval.ItemInfo{{Label: "Secret"}},
	}
	h.OnEvent(approval.Event{Type: approval.EventRequestCreated, Request: req})

	h.mu.Lock()
	nID := h.notifications["err-1"]
	h.mu.Unlock()

	actions := make(chan Action, 1)
	actions <- Action{NotificationID: nID, ActionKey: "approve"}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		h.ListenActions(ctx, actions)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	// Should not panic — error is logged but not propagated
}

func TestHandler_ReverseMapCleanup(t *testing.T) {
	h, _, _ := newTestHandler()

	req := &approval.Request{
		ID:     "cleanup-1",
		Client: "user@host",
		Type:   approval.RequestTypeGetSecret,
		Items:  []approval.ItemInfo{{Label: "Secret"}},
	}

	h.OnEvent(approval.Event{Type: approval.EventRequestCreated, Request: req})

	h.mu.Lock()
	nID := h.notifications["cleanup-1"]
	_, hasReverse := h.requests[nID]
	h.mu.Unlock()

	if !hasReverse {
		t.Fatal("reverse map should contain entry after creation")
	}

	h.OnEvent(approval.Event{Type: approval.EventRequestApproved, Request: req})

	h.mu.Lock()
	_, hasNotif := h.notifications["cleanup-1"]
	_, hasReverse = h.requests[nID]
	h.mu.Unlock()

	if hasNotif {
		t.Error("notifications map should be cleaned up after resolve")
	}
	if hasReverse {
		t.Error("reverse map should be cleaned up after resolve")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && stringContains(s, substr)))
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
