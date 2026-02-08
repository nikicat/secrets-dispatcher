package notification

import (
	"sync"
	"testing"
	"time"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

// mockNotifier records calls for testing.
type mockNotifier struct {
	mu       sync.Mutex
	nextID   uint32
	notified []notifyCall
	closed   []uint32
	notifyErr error
	closeErr  error
}

type notifyCall struct {
	summary string
	body    string
}

func (m *mockNotifier) Notify(summary, body string) (uint32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.notifyErr != nil {
		return 0, m.notifyErr
	}
	m.nextID++
	m.notified = append(m.notified, notifyCall{summary, body})
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

func TestHandler_OnEvent_RequestCreated(t *testing.T) {
	mock := &mockNotifier{}
	h := NewHandler(mock)

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
	if call.summary != "Secret Access Request" {
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

func TestHandler_OnEvent_RequestResolved_ClosesNotification(t *testing.T) {
	mock := &mockNotifier{}
	h := NewHandler(mock)

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
			mock := &mockNotifier{}
			h := NewHandler(mock)

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
	mock := &mockNotifier{}
	h := NewHandler(mock)

	req := &approval.Request{ID: "unknown"}

	// Should not panic or call close
	h.OnEvent(approval.Event{Type: approval.EventRequestApproved, Request: req})

	if mock.closeCount() != 0 {
		t.Errorf("should not close unknown notification")
	}
}

func TestHandler_FormatBody_Search(t *testing.T) {
	mock := &mockNotifier{}
	h := NewHandler(mock)

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
	mock := &mockNotifier{}
	h := NewHandler(mock)

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
	mock := &mockNotifier{}
	h := NewHandler(mock)

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
