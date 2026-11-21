package notification

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/procutil"
	"github.com/nikicat/secrets-dispatcher/internal/sender"
	"github.com/nikicat/secrets-dispatcher/internal/testutil"
	"github.com/stretchr/testify/assert"
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
	buttons []Button
}

func (m *mockNotifier) Notify(summary, body, icon string, buttons []Button) (uint32, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.notifyErr != nil {
		return 0, m.notifyErr
	}
	m.nextID++
	m.notified = append(m.notified, notifyCall{summary, body, icon, buttons})
	return m.nextID, nil
}

func (m *mockNotifier) NotifyPersistent(summary, body, icon string) (uint32, error) {
	return m.Notify(summary, body, icon, nil)
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

func (a *mockApprover) AutoApprove(requestID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return a.err
	}
	a.approved = append(a.approved, "auto:"+requestID)
	return nil
}

func (a *mockApprover) ApproveAndAutoApprove(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return a.err
	}
	a.approved = append(a.approved, "approve_auto:"+id)
	return nil
}

func newTestHandler() (*Handler, *mockNotifier, *mockApprover) {
	mock := &mockNotifier{}
	approver := &mockApprover{}
	// Zero delay for existing tests — notifications fire immediately.
	h := NewHandler(mock, approver, "127.0.0.1:8484", false, 2*time.Minute, 0)
	return h, mock, approver
}

// Returns a GetSecret request for one item labelled "Secret", with no sender.
func secretRequest(id string) *approval.Request {
	return &approval.Request{
		ID:    id,
		Type:  approval.RequestTypeGetSecret,
		Items: []approval.ItemInfo{{Label: "Secret"}},
	}
}

func TestHandler_OnEvent_RequestCreated(t *testing.T) {
	h, mock, _ := newTestHandler()

	req := &approval.Request{
		ID:   "test-123",
		Type: approval.RequestTypeGetSecret,
		Items: []approval.ItemInfo{
			{Label: "GitHub Token", Path: "/org/secrets/github"},
		},
		SenderInfo: approval.SenderInfo{
			Client: "user@remote",
			Info: sender.Info{
				PID:         1234,
				SystemdUnit: "ssh-agent.service",
			},
		},
	}

	h.OnEvent(approval.Event{Type: approval.EventRequestCreated, Request: req})

	if mock.notifyCount() != 1 {
		t.Errorf("expected 1 notification, got %d", mock.notifyCount())
	}

	call := mock.lastNotify()
	if call.summary != "Secret requested" {
		t.Errorf("unexpected summary: %s", call.summary)
	}
	if !contains(call.body, "<b>ssh-agent.service</b>@user@remote[1234]") {
		t.Errorf("body should contain proc@client[pid] header: %s", call.body)
	}
	if !contains(call.body, "<i>GitHub Token</i>") {
		t.Errorf("body should contain italic secret label: %s", call.body)
	}
}

// A sender whose invoker has an executable, so Request.AutoApprovable accepts
// its requests.
var localSender = approval.SenderInfo{
	PID:          4242,
	InvokerName:  "gh",
	ProcessChain: []procutil.Process{{Name: "gh", PID: 4242, Exe: new(procutil.Path("/usr/bin/gh"))}},
}

// A sender with no process chain. A remote client's requests carry one like it
// (ADR 0011), and Request.AutoApprovable rejects them.
var remoteSender = approval.SenderInfo{InvokerName: "gh"}

// Checks that the notification offers "Approve 2m" only for a request that
// Request.AutoApprovable accepts.
func TestHandler_OnEvent_RequestCreated_Actions(t *testing.T) {
	tests := []struct {
		name        string
		sender      approval.SenderInfo
		wantButtons []Button
	}{
		{"local", localSender,
			[]Button{{"default", ""}, {"approve", "Approve"}, {"approve_and_auto_approve", "Approve 2m"}, {"deny", "Deny"}}},
		{"remote", remoteSender,
			[]Button{{"default", ""}, {"approve", "Approve"}, {"deny", "Deny"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, mock, _ := newTestHandler()

			req := &approval.Request{
				ID:         "test-actions",
				Type:       approval.RequestTypeGetSecret,
				Items:      []approval.ItemInfo{{Label: "Secret"}},
				SenderInfo: tt.sender,
			}

			h.OnEvent(approval.Event{Type: approval.EventRequestCreated, Request: req})

			if got := mock.lastNotify().buttons; !slices.Equal(got, tt.wantButtons) {
				t.Errorf("buttons = %q, want %q", got, tt.wantButtons)
			}
		})
	}
}

func TestHandler_OnEvent_RequestResolved_ClosesNotification(t *testing.T) {
	h, mock, _ := newTestHandler()

	req := secretRequest("test-456")

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

			req := secretRequest("test-req")

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
		ID:   "search-1",
		Type: approval.RequestTypeSearch,
		SearchAttributes: map[string]string{
			"service": "github",
		},
	}

	h.OnEvent(approval.Event{Type: approval.EventRequestCreated, Request: req})

	call := mock.lastNotify()
	if call.summary != "Secrets searched" {
		t.Errorf("expected summary 'Secrets searched', got %q", call.summary)
	}
	if !contains(call.body, "<i>service=github</i>") {
		t.Errorf("body should contain italic search attributes: %s", call.body)
	}
}

func TestHandler_FormatBody_MultipleItems(t *testing.T) {
	h, mock, _ := newTestHandler()

	req := &approval.Request{
		ID:   "multi-1",
		Type: approval.RequestTypeGetSecret,
		Items: []approval.ItemInfo{
			{Label: "Secret1"},
			{Label: "Secret2"},
			{Label: "Secret3"},
		},
	}

	h.OnEvent(approval.Event{Type: approval.EventRequestCreated, Request: req})

	call := mock.lastNotify()
	if !contains(call.body, "<i>3 items</i>") {
		t.Errorf("body should show italic item count: %s", call.body)
	}
}

func TestHandler_FormatBody_PIDOnly(t *testing.T) {
	h, mock, _ := newTestHandler()

	req := &approval.Request{
		ID:    "pid-1",
		Type:  approval.RequestTypeGetSecret,
		Items: []approval.ItemInfo{{Label: "Secret"}},
		SenderInfo: approval.SenderInfo{
			Client: "user@host",
			Info: sender.Info{
				PID: 5678,
				// No InvokerName
			},
		},
	}

	h.OnEvent(approval.Event{Type: approval.EventRequestCreated, Request: req})

	call := mock.lastNotify()
	if !contains(call.body, "<b>user@host</b>[5678]") {
		t.Errorf("body should show client[pid]: %s", call.body)
	}
}

func TestHandler_FormatBody_SenderHeader(t *testing.T) {
	tests := []struct {
		name   string
		sender approval.SenderInfo
		want   string
	}{
		{
			name:   "name, client and pid",
			sender: approval.SenderInfo{Client: "a<b", Info: sender.Info{PID: 7, InvokerName: "x&y"}},
			want:   "<b>x&amp;y</b>@a&lt;b[7]: ",
		},
		{
			name:   "client and pid",
			sender: approval.SenderInfo{Client: "a<b", Info: sender.Info{PID: 7}},
			want:   "<b>a&lt;b</b>[7]: ",
		},
		{
			name:   "client only",
			sender: approval.SenderInfo{Client: "a<b"},
			want:   "<b>a&lt;b</b>: ",
		},
	}
	h, _, _ := newTestHandler()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &approval.Request{
				Type:       approval.RequestTypeGetSecret,
				Items:      []approval.ItemInfo{{Label: "Secret"}},
				SenderInfo: tt.sender,
			}
			assert.Equal(t, tt.want+"<i>Secret</i>", h.formatBody(req))
		})
	}
}

func TestHandler_OnEvent_GPGSignRequest(t *testing.T) {
	h, mock, _ := newTestHandler()

	req := &approval.Request{
		ID:   "gpg-1",
		Type: approval.RequestTypeGPGSign,
		GPGSignInfo: &approval.GPGSignInfo{
			RepoName:     "my-project",
			CommitMsg:    "Add feature\n\nSome body text",
			Author:       "John",
			KeyID:        "ABCD1234",
			ChangedFiles: []string{"a.go", "b.go"},
		},
		SenderInfo: approval.SenderInfo{
			Info: sender.Info{
				PID:         1234,
				InvokerName: "ssh-agent.service",
			},
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
	if !contains(call.body, "<b>my-project</b>: ") {
		t.Errorf("body should contain bold repo: %s", call.body)
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
		ID:   "gpg-pid-1",
		Type: approval.RequestTypeGPGSign,
		GPGSignInfo: &approval.GPGSignInfo{
			RepoName:  "my-project",
			CommitMsg: "Fix bug",
		},
		SenderInfo: approval.SenderInfo{
			Info: sender.Info{
				PID: 5678,
			},
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

// TestHandler_FormatBody_EscapesInjectedMarkup is the regression test for Vuln 7:
// client-controlled fields must be HTML-escaped before being interpolated into the
// notification markup body, so an attacker cannot forge reassuring markup on the
// (directly actionable) consent surface. The notification carries approve/deny
// buttons, so injected bold/italic text would aid a consent spoof.
func TestHandler_FormatBody_EscapesInjectedMarkup(t *testing.T) {
	h, mock, _ := newTestHandler()

	// A crafted commit message that tries to close the <i> and inject a bold
	// "Verified" reassurance, plus a process comm that injects markup too.
	req := &approval.Request{
		ID:   "inject-1",
		Type: approval.RequestTypeGPGSign,
		GPGSignInfo: &approval.GPGSignInfo{
			RepoName:  "repo<script>",
			CommitMsg: "docs: typo</i> <b>Verified · GNOME Keyring</b>",
		},
		SenderInfo: approval.SenderInfo{
			Info: sender.Info{
				ProcessChain: []procutil.Process{{Name: "evil</b><b>git", PID: 9}},
			},
		},
	}

	h.OnEvent(approval.Event{Type: approval.EventRequestCreated, Request: req})
	body := mock.lastNotify().body

	// No raw attacker-supplied tags survive.
	assert.NotContains(t, body, "<b>Verified", "injected bold reassurance must be escaped")
	assert.NotContains(t, body, "typo</i>", "injected closing tag must be escaped")
	assert.NotContains(t, body, "<script>", "injected repo markup must be escaped")
	assert.NotContains(t, body, "evil</b><b>git", "injected process-name markup must be escaped")

	// The content is still present, escaped.
	assert.Contains(t, body, "&lt;b&gt;Verified", "escaped commit text should be present")
	assert.Contains(t, body, "repo&lt;script&gt;", "escaped repo name should be present")

	// Our own wrapping markup is intact.
	assert.Contains(t, body, "<b>repo&lt;script&gt;</b>", "our own <b> tag must remain literal")
}

// TestHandler_FormatBody_QuotesNotEscaped guards against over-escaping: quotes
// don't need escaping in the notification markup body (text is never inside an
// attribute), and servers like dunst and mako render html.EscapeString's
// &#39;/&#34; literally instead of decoding them.
func TestHandler_FormatBody_QuotesNotEscaped(t *testing.T) {
	h, mock, _ := newTestHandler()

	req := &approval.Request{
		ID:   "quotes-1",
		Type: approval.RequestTypeGPGSign,
		GPGSignInfo: &approval.GPGSignInfo{
			RepoName:  "my-project",
			CommitMsg: `fix: don't say "hello" to <script> & friends`,
		},
		SenderInfo: approval.SenderInfo{Info: sender.Info{PID: 5678}},
	}

	h.OnEvent(approval.Event{Type: approval.EventRequestCreated, Request: req})
	body := mock.lastNotify().body

	assert.Contains(t, body, `don't say "hello"`, "quotes must pass through verbatim")
	assert.NotContains(t, body, "&#39;", "apostrophe must not be numerically escaped")
	assert.NotContains(t, body, "&#34;", "double quote must not be numerically escaped")
	assert.Contains(t, body, "&lt;script&gt; &amp; friends", "markup-relevant characters must still be escaped")
}

func TestHandler_OnEvent_DeleteRequest(t *testing.T) {
	h, mock, _ := newTestHandler()

	req := &approval.Request{
		ID:   "delete-1",
		Type: approval.RequestTypeDelete,
		Items: []approval.ItemInfo{
			{Label: "GitHub Token", Path: "/org/secrets/github"},
		},
		SenderInfo: approval.SenderInfo{
			Info: sender.Info{
				PID:         1234,
				InvokerName: "secret-tool.service",
			},
		},
	}

	h.OnEvent(approval.Event{Type: approval.EventRequestCreated, Request: req})

	if mock.notifyCount() != 1 {
		t.Fatalf("expected 1 notification, got %d", mock.notifyCount())
	}

	call := mock.lastNotify()
	if call.summary != "Deletion requested" {
		t.Errorf("expected summary 'Deletion requested', got %q", call.summary)
	}
	if call.icon != "dialog-warning" {
		t.Errorf("expected icon 'dialog-warning', got %q", call.icon)
	}
	if !contains(call.body, "GitHub Token") {
		t.Errorf("body should contain item label: %s", call.body)
	}
}

func TestHandler_FormatBody_DeleteWithProcessChain(t *testing.T) {
	h, mock, _ := newTestHandler()

	req := &approval.Request{
		ID:   "delete-chain-1",
		Type: approval.RequestTypeDelete,
		Items: []approval.ItemInfo{
			{Label: "My Secret", Path: "/org/secrets/item1"},
		},
		SenderInfo: approval.SenderInfo{
			Info: sender.Info{
				ProcessChain: []procutil.Process{
					{Name: "bash", PID: 100},
					{Name: "secret-tool", PID: 200},
				},
			},
		},
	}

	h.OnEvent(approval.Event{Type: approval.EventRequestCreated, Request: req})

	call := mock.lastNotify()
	if !contains(call.body, "<b>My Secret</b>") {
		t.Errorf("body should contain bold item label: %s", call.body)
	}
	if !contains(call.body, "secret-tool") {
		t.Errorf("body should contain process chain: %s", call.body)
	}
}

func TestHandler_FormatBody_ProcessChainExact(t *testing.T) {
	req := &approval.Request{
		Type:  approval.RequestTypeDelete,
		Items: []approval.ItemInfo{{Label: "My Secret"}},
		SenderInfo: approval.SenderInfo{
			Client: "user@host",
			Info: sender.Info{
				ProcessChain: []procutil.Process{
					{Name: "a<b", PID: 100},
					{Name: "secret-tool", PID: 200},
				},
			},
		},
	}

	for _, tt := range []struct {
		showPIDs bool
		want     string
	}{
		{false, "<b>My Secret</b>\na&lt;b ← secret-tool"},
		{true, "<b>My Secret</b>\na&lt;b[100] ← secret-tool[200]"},
	} {
		h := NewHandler(&mockNotifier{}, &mockApprover{}, "127.0.0.1:8484", tt.showPIDs, 2*time.Minute, 0)
		assert.Equal(t, tt.want, h.formatBody(req), "showPIDs=%v", tt.showPIDs)
	}
}

func TestHandler_OnEvent_WriteRequest(t *testing.T) {
	h, mock, _ := newTestHandler()

	req := &approval.Request{
		ID:   "write-1",
		Type: approval.RequestTypeWrite,
		Items: []approval.ItemInfo{
			{Label: "New Secret", Path: "/org/secrets/collection/default"},
		},
		SenderInfo: approval.SenderInfo{Info: sender.Info{PID: 1234}},
	}

	h.OnEvent(approval.Event{Type: approval.EventRequestCreated, Request: req})

	call := mock.lastNotify()
	if call.summary != "Secret write requested" {
		t.Errorf("expected summary 'Secret write requested', got %q", call.summary)
	}
	if call.icon != "dialog-warning" {
		t.Errorf("expected icon 'dialog-warning', got %q", call.icon)
	}
	if !contains(call.body, "New Secret") {
		t.Errorf("body should contain item label: %s", call.body)
	}
}

func TestHandler_OnEvent_GetSecretIcon(t *testing.T) {
	h, mock, _ := newTestHandler()

	req := &approval.Request{
		ID:    "secret-icon-1",
		Type:  approval.RequestTypeGetSecret,
		Items: []approval.ItemInfo{{Label: "MySecret"}},
	}

	h.OnEvent(approval.Event{Type: approval.EventRequestCreated, Request: req})

	if mock.notifyCount() != 1 {
		t.Fatalf("expected 1 notification, got %d", mock.notifyCount())
	}

	call := mock.lastNotify()
	if call.summary != "Secret requested" {
		t.Errorf("expected summary 'Secret requested', got %q", call.summary)
	}
	if call.icon != "dialog-password" {
		t.Errorf("expected icon 'dialog-password', got %q", call.icon)
	}
}

func TestHandler_ListenActions_Approve(t *testing.T) {
	h, mock, approver := newTestHandler()

	req := secretRequest("action-approve-1")

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

	req := secretRequest("action-deny-1")

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
	h.openURL = func(u string) error { opened = u; return nil }

	req := secretRequest("action-default-1")

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

	if opened != "http://127.0.0.1:8484/?request=action-default-1" {
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

	req := secretRequest("err-1")
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

	req := secretRequest("cleanup-1")

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

func newTestHandlerWithDelay(delay time.Duration) (*Handler, *mockNotifier, *mockApprover) {
	mock := &mockNotifier{}
	approver := &mockApprover{}
	h := NewHandler(mock, approver, "127.0.0.1:8484", false, 2*time.Minute, delay)
	return h, mock, approver
}

func TestHandler_DelayedNotification_SuppressesQuickCancel(t *testing.T) {
	h, mock, _ := newTestHandlerWithDelay(200 * time.Millisecond)

	req := secretRequest("quick-cancel-1")

	h.OnEvent(approval.Event{Type: approval.EventRequestCreated, Request: req})

	// No notification should be sent yet (still in delay window).
	if mock.notifyCount() != 0 {
		t.Fatalf("expected 0 notifications during delay, got %d", mock.notifyCount())
	}

	// Cancel before delay expires.
	h.OnEvent(approval.Event{Type: approval.EventRequestCancelled, Request: req})

	// Wait past the delay to verify no notification fires.
	time.Sleep(300 * time.Millisecond)

	if mock.notifyCount() != 0 {
		t.Errorf("expected 0 notifications after quick cancel, got %d", mock.notifyCount())
	}
	if mock.closeCount() != 0 {
		t.Errorf("expected 0 close calls (nothing to close), got %d", mock.closeCount())
	}
}

func TestHandler_DelayedNotification_FiresAfterDelay(t *testing.T) {
	h, mock, _ := newTestHandlerWithDelay(50 * time.Millisecond)

	req := secretRequest("delayed-fire-1")

	h.OnEvent(approval.Event{Type: approval.EventRequestCreated, Request: req})

	// Wait for delay to expire.
	time.Sleep(100 * time.Millisecond)

	if mock.notifyCount() != 1 {
		t.Errorf("expected 1 notification after delay, got %d", mock.notifyCount())
	}
}

func TestHandler_DelayedNotification_ResolvedDuringDelay(t *testing.T) {
	h, mock, _ := newTestHandlerWithDelay(200 * time.Millisecond)

	req := secretRequest("resolve-during-delay-1")

	h.OnEvent(approval.Event{Type: approval.EventRequestCreated, Request: req})
	h.OnEvent(approval.Event{Type: approval.EventRequestApproved, Request: req})

	// Wait past delay to verify nothing fires.
	time.Sleep(300 * time.Millisecond)

	if mock.notifyCount() != 0 {
		t.Errorf("expected 0 notifications when resolved during delay, got %d", mock.notifyCount())
	}
}

func TestHandler_DelayedNotification_CancelledAfterShown(t *testing.T) {
	h, mock, _ := newTestHandlerWithDelay(20 * time.Millisecond)

	req := &approval.Request{
		ID:         "cancel-after-shown-1",
		Type:       approval.RequestTypeGetSecret,
		Items:      []approval.ItemInfo{{Label: "Secret"}},
		SenderInfo: localSender,
	}

	h.OnEvent(approval.Event{Type: approval.EventRequestCreated, Request: req})

	// Wait for notification to actually appear.
	time.Sleep(50 * time.Millisecond)
	if mock.notifyCount() != 1 {
		t.Fatalf("expected 1 notification after delay, got %d", mock.notifyCount())
	}

	// Cancel after notification was shown — should close + show follow-up.
	h.OnEvent(approval.Event{Type: approval.EventRequestCancelled, Request: req})

	// Original closed (1 close) + follow-up sent (2 notifies total).
	if mock.notifyCount() != 2 {
		t.Errorf("expected 2 notifications (original + follow-up), got %d", mock.notifyCount())
	}
	if mock.closeCount() != 1 {
		t.Errorf("expected 1 close call for original notification, got %d", mock.closeCount())
	}
}

// Points DBUS_SESSION_BUS_ADDRESS at a private bus for the test.
// Skips the test when dbus-daemon is not installed.
func useTestSessionBus(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skipf("dbus-daemon not found: %v", err)
	}
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", testutil.StartDBusDaemon(t))
}

// newTestDBusNotifier creates a DBusNotifier connected to a private test bus.
func newTestDBusNotifier(t *testing.T) *DBusNotifier {
	t.Helper()

	useTestSessionBus(t)

	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatalf("connect to test bus: %v", err)
	}

	n := &DBusNotifier{
		conn:    conn,
		signals: make(chan *dbus.Signal, 16),
		actions: make(chan Action, 16),
		done:    make(chan struct{}),
	}

	if err := conn.AddMatchSignal(
		dbus.WithMatchInterface(notifyInterface),
		dbus.WithMatchMember("ActionInvoked"),
	); err != nil {
		conn.Close()
		t.Fatalf("subscribe to ActionInvoked: %v", err)
	}

	conn.Signal(n.signals)
	go n.processSignals(n.signals)
	t.Cleanup(func() { n.Stop() })
	return n
}

func TestDBusNotifier_NotifyReconnectsOnClosedConn(t *testing.T) {
	n := newTestDBusNotifier(t)

	// Simulate connection death.
	n.conn.Close()

	// Notify should reconnect. It will fail because there's no notification
	// daemon on the test bus, but it must NOT fail with ErrClosed.
	_, err := n.Notify("test", "after reconnect", "", nil)
	if errors.Is(err, dbus.ErrClosed) {
		t.Errorf("Notify should have reconnected, but got ErrClosed: %v", err)
	}
}

func TestDBusNotifier_CloseReconnectsOnClosedConn(t *testing.T) {
	n := newTestDBusNotifier(t)

	// Simulate connection death.
	n.conn.Close()

	// Close should reconnect. The notification ID is bogus, but we only
	// verify it doesn't return a connection-closed error.
	err := n.Close(999)
	if errors.Is(err, dbus.ErrClosed) {
		t.Errorf("Close should have reconnected, but got ErrClosed: %v", err)
	}
}

// Checks that a cancelled request that Request.AutoApprovable rejects closes
// its notification and gets no auto-approve follow-up.
func TestHandler_CancelledRemoteRequest_NoFollowUp(t *testing.T) {
	h, mock, _ := newTestHandler()

	req := &approval.Request{
		ID:         "cancel-remote-1",
		Type:       approval.RequestTypeGetSecret,
		Items:      []approval.ItemInfo{{Label: "Secret"}},
		SenderInfo: remoteSender,
	}

	h.OnEvent(approval.Event{Type: approval.EventRequestCreated, Request: req})
	h.OnEvent(approval.Event{Type: approval.EventRequestCancelled, Request: req})

	if mock.notifyCount() != 1 {
		t.Errorf("notifications = %d, want 1 (no follow-up)", mock.notifyCount())
	}
	if mock.closeCount() != 1 {
		t.Errorf("closes = %d, want 1", mock.closeCount())
	}
}
