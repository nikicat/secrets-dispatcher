package notification

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/procutil"
	"github.com/nikicat/secrets-dispatcher/internal/sender"
	"github.com/nikicat/secrets-dispatcher/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A Notifier that records its calls.
type mockNotifier struct {
	mu        sync.Mutex
	nextID    NotificationID
	notified  []notifyCall
	closed    []NotificationID
	notifyErr error
	closeErr  error
}

type notifyCall struct {
	summary string
	body    string
	icon    string
	buttons []Button
}

func (m *mockNotifier) Notify(summary, body, icon string, buttons []Button) (NotificationID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.notifyErr != nil {
		return 0, m.notifyErr
	}
	m.nextID++
	m.notified = append(m.notified, notifyCall{summary, body, icon, buttons})
	return m.nextID, nil
}

func (m *mockNotifier) NotifyPersistent(summary, body, icon string) (NotificationID, error) {
	return m.Notify(summary, body, icon, nil)
}

func (m *mockNotifier) Close(id NotificationID) error {
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

// Returns the one Notify call made, and fails the test unless exactly one was.
func (m *mockNotifier) onlyNotify(t *testing.T) notifyCall {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	require.Len(t, m.notified, 1, "Notify calls")
	return m.notified[0]
}

func (m *mockNotifier) lastClosed() NotificationID {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.closed) == 0 {
		return 0
	}
	return m.closed[len(m.closed)-1]
}

// An Approver that records its approve and deny calls.
type mockApprover struct {
	mu       sync.Mutex
	approved []approval.RequestID
	denied   []approval.RequestID
	err      error
}

// Returns the configured error.
func (a *mockApprover) fail() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.err
}

// Appends the given ID to the given record, or returns the configured error.
func (a *mockApprover) record(calls *[]approval.RequestID, id approval.RequestID) error {
	if err := a.fail(); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	*calls = append(*calls, id)
	return nil
}

func (a *mockApprover) Approve(id approval.RequestID) error            { return a.record(&a.approved, id) }
func (a *mockApprover) Deny(id approval.RequestID) error               { return a.record(&a.denied, id) }
func (a *mockApprover) AddRuleFromCancelled(approval.RequestID) error  { return a.fail() }
func (a *mockApprover) ApproveAndAutoApprove(approval.RequestID) error { return a.fail() }

// Returns a handler whose notifications fire immediately.
func newTestHandler() (*Handler, *mockNotifier, *mockApprover) {
	return newTestHandlerWithDelay(0)
}

const testNotificationDelay = 100 * time.Millisecond

// Advances the bubble's clock to testNotificationDelay and waits until the
// goroutines it wakes block again.
func passDelay() {
	time.Sleep(testNotificationDelay)
	synctest.Wait()
}

func newTestHandlerWithDelay(delay time.Duration) (*Handler, *mockNotifier, *mockApprover) {
	mock := &mockNotifier{}
	approver := &mockApprover{}
	h := NewHandler(mock, approver, "127.0.0.1:8484", false, 2*time.Minute, delay)
	return h, mock, approver
}

// Sends one action with the given key on the notification of the given request
// through Handler.ListenActions, and returns once it is handled.
func runAction(h *Handler, requestID approval.RequestID, key actionKey) {
	nID, _ := notificationOf(h, requestID)

	actions := make(chan Action, 1)
	actions <- Action{NotificationID: nID, ActionKey: key}
	close(actions)
	h.ListenActions(context.Background(), actions)
}

func notificationOf(h *Handler, requestID approval.RequestID) (NotificationID, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	nID, ok := h.notifications[requestID]
	return nID, ok
}

func requestOf(h *Handler, nID NotificationID) (approval.RequestID, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	reqID, ok := h.requests[nID]
	return reqID, ok
}

// Returns the one notification that a new handler shows for the given request.
func shownNotification(t *testing.T, req *approval.Request) notifyCall {
	t.Helper()
	h, mock, _ := newTestHandler()
	h.RequestCreated(approval.Pending{Request: req})
	return mock.onlyNotify(t)
}

// Returns a GetSecret request for one item labelled "Secret", with no sender.
func secretRequest(id approval.RequestID) *approval.Request {
	return &approval.Request{
		ID:    id,
		Type:  approval.RequestTypeGetSecret,
		Items: []approval.ItemInfo{{Label: "Secret"}},
	}
}

func TestHandler_RequestCreated(t *testing.T) {
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

	call := shownNotification(t, req)
	assert.Equal(t, "Secret requested", call.summary)
	assert.Contains(t, call.body, "<b>ssh-agent.service</b>@user@remote[1234]", "body should contain proc@client[pid] header")
	assert.Contains(t, call.body, "<i>GitHub Token</i>", "body should contain italic secret label")
}

// Checks that the notification offers "Approve 2m" only for a request that
// Request.AutoApprovable accepts.
func TestHandler_RequestCreated_Actions(t *testing.T) {
	tests := []struct {
		name        string
		sender      approval.SenderInfo
		wantButtons []Button
	}{
		{"local", approval.LocalSenderForTest("gh", "/usr/bin/gh"),
			[]Button{{"default", ""}, {"approve", "Approve"}, {"approve_and_auto_approve", "Approve 2m"}, {"deny", "Deny"}}},
		{"remote", approval.RemoteSenderForTest("gh"),
			[]Button{{"default", ""}, {"approve", "Approve"}, {"deny", "Deny"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := secretRequest("test-actions")
			req.SenderInfo = tt.sender

			assert.Equal(t, tt.wantButtons, shownNotification(t, req).buttons)
		})
	}
}

func TestHandler_RequestResolved_ClosesNotification(t *testing.T) {
	h, mock, _ := newTestHandler()

	req := secretRequest("test-456")

	// Create notification
	h.RequestCreated(approval.Pending{Request: req})

	require.Equal(t, 1, mock.notifyCount())

	// Approve should close it
	h.RequestEnded(approval.HistoryEntry{Request: req, Resolution: approval.ResolutionApproved})

	assert.Equal(t, 1, mock.closeCount())
	assert.Equal(t, NotificationID(1), mock.lastClosed())
}

func TestHandler_AllResolutionTypes(t *testing.T) {
	resolutions := []approval.Resolution{
		approval.ResolutionApproved,
		approval.ResolutionDenied,
		approval.ResolutionExpired,
		approval.ResolutionCancelled,
		approval.ResolutionAutoApproved,
		approval.ResolutionIgnored,
	}

	for _, resolution := range resolutions {
		t.Run(string(resolution), func(t *testing.T) {
			h, mock, _ := newTestHandler()

			req := secretRequest("test-req")

			h.RequestCreated(approval.Pending{Request: req})
			h.RequestEnded(approval.HistoryEntry{Request: req, Resolution: resolution})

			assert.Equal(t, 1, mock.closeCount(), "notification should be closed for %v", resolution)
		})
	}
}

func TestHandler_ResolveWithoutCreate(t *testing.T) {
	h, mock, _ := newTestHandler()

	req := &approval.Request{ID: "unknown"}

	// Should not panic or call close
	h.RequestEnded(approval.HistoryEntry{Request: req, Resolution: approval.ResolutionApproved})

	assert.Zero(t, mock.closeCount(), "should not close unknown notification")
}

func TestHandler_FormatBody_Search(t *testing.T) {
	req := &approval.Request{
		ID:   "search-1",
		Type: approval.RequestTypeSearch,
		Payload: approval.SearchPayload{Attributes: map[string]string{
			"service": "github",
		}},
	}

	call := shownNotification(t, req)
	assert.Equal(t, "Secrets searched", call.summary)
	assert.Contains(t, call.body, "<i>service=github</i>", "body should contain italic search attributes")
}

func TestHandler_FormatBody_MultipleItems(t *testing.T) {
	req := &approval.Request{
		ID:   "multi-1",
		Type: approval.RequestTypeGetSecret,
		Items: []approval.ItemInfo{
			{Label: "Secret1"},
			{Label: "Secret2"},
			{Label: "Secret3"},
		},
	}

	call := shownNotification(t, req)
	assert.Contains(t, call.body, "<i>3 items</i>", "body should show italic item count")
}

func TestHandler_FormatBody_PIDOnly(t *testing.T) {
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

	call := shownNotification(t, req)
	assert.Contains(t, call.body, "<b>user@host</b>[5678]", "body should show client[pid]")
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
			req := secretRequest("sender-header-1")
			req.SenderInfo = tt.sender
			assert.Equal(t, tt.want+"<i>Secret</i>", h.formatBody(req))
		})
	}
}

func TestHandler_GPGSignRequest(t *testing.T) {
	req := &approval.Request{
		ID:      "gpg-1",
		Type:    approval.RequestTypeGPGSign,
		Payload: approval.CommitSignInfoForTest("my-project", "Add feature\n\nSome body text", "a.go", "b.go"),
		SenderInfo: approval.SenderInfo{
			Info: sender.Info{
				PID:         1234,
				InvokerName: "ssh-agent.service",
			},
		},
	}

	call := shownNotification(t, req)
	assert.Equal(t, "Sign commit", call.summary)
	assert.Equal(t, "emblem-important", call.icon)
	assert.Contains(t, call.body, "<b>my-project</b>: ", "body should contain bold repo")
	assert.Contains(t, call.body, "<i>Add feature</i>", "body should contain italic commit subject")
	assert.NotContains(t, call.body, "Some body text", "body should NOT contain commit body text")
}

func TestHandler_FormatBody_GPGSign_PIDOnly(t *testing.T) {
	req := &approval.Request{
		ID:      "gpg-pid-1",
		Type:    approval.RequestTypeGPGSign,
		Payload: approval.CommitSignInfoForTest("my-project", "Fix bug"),
		SenderInfo: approval.SenderInfo{
			Info: sender.Info{
				PID: 5678,
			},
		},
	}

	call := shownNotification(t, req)
	assert.Contains(t, call.body, "<b>my-project</b>: ", "body should contain bold repo without process name")
	assert.Contains(t, call.body, "<i>Fix bug</i>", "body should contain italic commit subject")
}

// Guards that sender-controlled values reach the markup body escaped, so they
// cannot forge markup on the consent surface (ADR 0007).
func TestHandler_FormatBody_EscapesInjectedMarkup(t *testing.T) {
	// A crafted commit message that tries to close the <i> and inject a bold
	// "Verified" reassurance, plus a process comm that injects markup too.
	req := &approval.Request{
		ID:      "inject-1",
		Type:    approval.RequestTypeGPGSign,
		Payload: approval.CommitSignInfoForTest("repo<script>", "docs: typo</i> <b>Verified · GNOME Keyring</b>"),
		SenderInfo: approval.SenderInfo{
			Info: sender.Info{
				ProcessChain: []procutil.Process{{Name: "evil</b><b>git", PID: 9}},
			},
		},
	}

	body := shownNotification(t, req).body

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

// Guards against over-escaping: quotes in the markup body stay as they are
// (ADR 0007).
func TestHandler_FormatBody_QuotesNotEscaped(t *testing.T) {
	req := &approval.Request{
		ID:         "quotes-1",
		Type:       approval.RequestTypeGPGSign,
		Payload:    approval.CommitSignInfoForTest("my-project", `fix: don't say "hello" to <script> & friends`),
		SenderInfo: approval.SenderInfo{Info: sender.Info{PID: 5678}},
	}

	body := shownNotification(t, req).body

	assert.Contains(t, body, `don't say "hello"`, "quotes must pass through verbatim")
	assert.NotContains(t, body, "&#39;", "apostrophe must not be numerically escaped")
	assert.NotContains(t, body, "&#34;", "double quote must not be numerically escaped")
	assert.Contains(t, body, "&lt;script&gt; &amp; friends", "markup-relevant characters must still be escaped")
}

func TestHandler_DeleteRequest(t *testing.T) {
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

	call := shownNotification(t, req)
	assert.Equal(t, "Deletion requested", call.summary)
	assert.Equal(t, "dialog-warning", call.icon)
	assert.Contains(t, call.body, "GitHub Token", "body should contain item label")
}

func TestHandler_FormatBody_DeleteWithProcessChain(t *testing.T) {
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

	call := shownNotification(t, req)
	assert.Contains(t, call.body, "<b>My Secret</b>", "body should contain bold item label")
	assert.Contains(t, call.body, "secret-tool", "body should contain process chain")
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

func TestHandler_WriteRequest(t *testing.T) {
	req := &approval.Request{
		ID:   "write-1",
		Type: approval.RequestTypeWrite,
		Items: []approval.ItemInfo{
			{Label: "New Secret", Path: "/org/secrets/collection/default"},
		},
		SenderInfo: approval.SenderInfo{Info: sender.Info{PID: 1234}},
	}

	call := shownNotification(t, req)
	assert.Equal(t, "Secret write requested", call.summary)
	assert.Equal(t, "dialog-warning", call.icon)
	assert.Contains(t, call.body, "New Secret", "body should contain item label")
}

func TestHandler_GetSecretIcon(t *testing.T) {
	call := shownNotification(t, secretRequest("secret-icon-1"))
	assert.Equal(t, "Secret requested", call.summary)
	assert.Equal(t, "dialog-password", call.icon)
}

func TestHandler_ListenActions_Approve(t *testing.T) {
	h, _, approver := newTestHandler()

	h.RequestCreated(approval.Pending{Request: secretRequest("action-approve-1")})
	runAction(h, "action-approve-1", actionApprove)

	assert.Equal(t, []approval.RequestID{"action-approve-1"}, approver.approved)
}

func TestHandler_ListenActions_Deny(t *testing.T) {
	h, _, approver := newTestHandler()

	h.RequestCreated(approval.Pending{Request: secretRequest("action-deny-1")})
	runAction(h, "action-deny-1", actionDeny)

	assert.Equal(t, []approval.RequestID{"action-deny-1"}, approver.denied)
}

func TestHandler_ListenActions_DefaultOpensURL(t *testing.T) {
	h, _, approver := newTestHandler()

	var opened string
	h.openURL = func(u string) error { opened = u; return nil }

	h.RequestCreated(approval.Pending{Request: secretRequest("action-default-1")})
	runAction(h, "action-default-1", actionDefault)

	assert.Equal(t, "http://127.0.0.1:8484/?request=action-default-1", opened)
	assert.Empty(t, approver.approved, "default action should not approve")
	assert.Empty(t, approver.denied, "default action should not deny")
}

func TestHandler_ListenActions_UnknownNotification(t *testing.T) {
	h, _, approver := newTestHandler()

	runAction(h, "unknown", actionApprove)

	assert.Empty(t, approver.approved, "should not approve unknown notification")
}

// Checks that an approver error is logged, not propagated.
func TestHandler_ListenActions_ApproverError(t *testing.T) {
	logs := testutil.CaptureLogs(t, slog.LevelError)

	h, _, approver := newTestHandler()
	approver.err = fmt.Errorf("not found")

	h.RequestCreated(approval.Pending{Request: secretRequest("err-1")})
	runAction(h, "err-1", actionApprove)

	assert.Contains(t, logs.String(), "request_id=err-1")
}

func TestHandler_ReverseMapCleanup(t *testing.T) {
	h, _, _ := newTestHandler()

	req := secretRequest("cleanup-1")

	h.RequestCreated(approval.Pending{Request: req})

	nID, _ := notificationOf(h, "cleanup-1")
	_, hasReverse := requestOf(h, nID)

	require.True(t, hasReverse, "reverse map should contain entry after creation")

	h.RequestEnded(approval.HistoryEntry{Request: req, Resolution: approval.ResolutionApproved})

	_, hasNotif := notificationOf(h, "cleanup-1")
	_, hasReverse = requestOf(h, nID)

	assert.False(t, hasNotif, "notifications map should be cleaned up after resolve")
	assert.False(t, hasReverse, "reverse map should be cleaned up after resolve")
}

func TestHandler_DelayedNotification_SuppressesQuickCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, mock, _ := newTestHandlerWithDelay(testNotificationDelay)

		req := secretRequest("quick-cancel-1")

		h.RequestCreated(approval.Pending{Request: req})

		require.Zero(t, mock.notifyCount(), "notifications during delay")

		h.RequestEnded(approval.HistoryEntry{Request: req, Resolution: approval.ResolutionCancelled})

		passDelay()

		assert.Zero(t, mock.notifyCount(), "notifications after quick cancel")
		assert.Zero(t, mock.closeCount(), "close calls (nothing to close)")
	})
}

func TestHandler_DelayedNotification_FiresAfterDelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, mock, _ := newTestHandlerWithDelay(testNotificationDelay)

		req := secretRequest("delayed-fire-1")

		h.RequestCreated(approval.Pending{Request: req})

		passDelay()

		assert.Equal(t, 1, mock.notifyCount(), "notifications after delay")
	})
}

func TestHandler_DelayedNotification_ResolvedDuringDelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, mock, _ := newTestHandlerWithDelay(testNotificationDelay)

		req := secretRequest("resolve-during-delay-1")

		h.RequestCreated(approval.Pending{Request: req})
		h.RequestEnded(approval.HistoryEntry{Request: req, Resolution: approval.ResolutionApproved})

		passDelay()

		assert.Zero(t, mock.notifyCount(), "notifications when resolved during delay")
	})
}

func TestHandler_DelayedNotification_CancelledAfterShown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h, mock, _ := newTestHandlerWithDelay(testNotificationDelay)

		req := secretRequest("cancel-after-shown-1")
		req.SenderInfo = approval.LocalSenderForTest("gh", "/usr/bin/gh")

		h.RequestCreated(approval.Pending{Request: req})

		passDelay()
		require.Equal(t, 1, mock.notifyCount(), "notifications after delay")

		h.RequestEnded(approval.HistoryEntry{Request: req, Resolution: approval.ResolutionCancelled})

		assert.Equal(t, 2, mock.notifyCount(), "notifications (original + follow-up)")
		assert.Equal(t, 1, mock.closeCount(), "close calls for original notification")
	})
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

// Returns a DBusNotifier connected to a private test bus.
func newTestDBusNotifier(t *testing.T) *DBusNotifier {
	t.Helper()

	useTestSessionBus(t)

	conn, err := dbus.ConnectSessionBus()
	require.NoError(t, err, "connect to test bus")

	n := &DBusNotifier{
		conn:    conn,
		signals: make(chan *dbus.Signal, signalBuffer),
		actions: make(chan Action, signalBuffer),
		done:    make(chan struct{}),
	}

	err = conn.AddMatchSignal(
		dbus.WithMatchInterface(string(notifyInterface)),
		dbus.WithMatchMember("ActionInvoked"),
	)
	if err != nil {
		conn.Close()
	}
	require.NoError(t, err, "subscribe to ActionInvoked")

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
	assert.NotErrorIs(t, err, dbus.ErrClosed, "Notify should have reconnected")
}

func TestDBusNotifier_CloseReconnectsOnClosedConn(t *testing.T) {
	n := newTestDBusNotifier(t)

	// Simulate connection death.
	n.conn.Close()

	// Close should reconnect. The notification ID is bogus, but we only
	// verify it doesn't return a connection-closed error.
	err := n.Close(999)
	assert.NotErrorIs(t, err, dbus.ErrClosed, "Close should have reconnected")
}

// Checks that a cancelled request that Request.AutoApprovable rejects closes
// its notification and gets no auto-approve follow-up.
func TestHandler_CancelledRemoteRequest_NoFollowUp(t *testing.T) {
	h, mock, _ := newTestHandler()

	req := secretRequest("cancel-remote-1")
	req.SenderInfo = approval.RemoteSenderForTest("gh")

	h.RequestCreated(approval.Pending{Request: req})
	h.RequestEnded(approval.HistoryEntry{Request: req, Resolution: approval.ResolutionCancelled})

	assert.Equal(t, 1, mock.notifyCount(), "notifications (no follow-up)")
	assert.Equal(t, 1, mock.closeCount(), "closes")
}

func TestFormatDurationShort(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{45 * time.Second, "45s"},
		{2 * time.Minute, "2m"},
		{90 * time.Second, "1m30s"},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, formatDurationShort(tt.d), tt.d.String())
	}
}
