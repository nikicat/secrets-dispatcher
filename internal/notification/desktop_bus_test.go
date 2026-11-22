package notification

import (
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// notifyArgs records one org.freedesktop.Notifications.Notify call as the
// server saw it on the wire.
type notifyArgs struct {
	AppName       string
	ReplacesID    uint32
	Icon          string
	Summary       string
	Body          string
	Actions       []string
	Hints         map[string]dbus.Variant
	ExpireTimeout int32
}

// stubNotificationServer owns org.freedesktop.Notifications on a private bus
// and records Notify calls — the in-package version of the fast-tier stub.
type stubNotificationServer struct {
	conn   *dbus.Conn
	mu     sync.Mutex
	calls  []notifyArgs
	nextID uint32
}

func (s *stubNotificationServer) Notify(appName string, replacesID uint32, icon, summary, body string,
	actions []string, hints map[string]dbus.Variant, expireTimeout int32) (uint32, *dbus.Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, notifyArgs{
		AppName: appName, ReplacesID: replacesID, Icon: icon, Summary: summary,
		Body: body, Actions: actions, Hints: hints, ExpireTimeout: expireTimeout,
	})
	s.nextID++
	return s.nextID, nil
}

func (s *stubNotificationServer) CloseNotification(id uint32) *dbus.Error {
	// Real servers direct NotificationClosed at the sending connection; on a
	// plain dbus-daemon a broadcast reaches it the same way.
	_ = s.conn.Emit(notifyPath, notifyInterface.Member("NotificationClosed"), id, uint32(3))
	return nil
}

func (s *stubNotificationServer) last(t *testing.T) notifyArgs {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	require.NotEmpty(t, s.calls, "no Notify calls recorded")
	return s.calls[len(s.calls)-1]
}

// Returns a stub notification server on a private session bus.
func newNotificationBus(t *testing.T) *stubNotificationServer {
	t.Helper()
	useTestSessionBus(t)

	conn, err := dbus.ConnectSessionBus()
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	stub := &stubNotificationServer{conn: conn}
	require.NoError(t, conn.Export(stub, notifyPath, string(notifyInterface)))
	reply, err := conn.RequestName(notifyDest, dbus.NameFlagDoNotQueue)
	require.NoError(t, err)
	require.Equal(t, dbus.RequestNameReplyPrimaryOwner, reply)
	return stub
}

func TestNotifySendsNeverExpiringCritical(t *testing.T) {
	stub := newNotificationBus(t)

	n, err := NewDBusNotifier()
	require.NoError(t, err)
	defer n.Stop()

	buttons := []Button{{"approve", "Approve"}, {"deny", "Deny"}}
	id, err := n.Notify("Secret requested", "body text", "dialog-password", buttons)
	require.NoError(t, err)
	assert.NotZero(t, id)

	got := stub.last(t)
	// Spec-honoring daemons close a -1 (server default) notification after
	// their own timeout, so approvals send 0 (ADR 0007).
	assert.Equal(t, int32(0), got.ExpireTimeout)
	assert.Equal(t, dbus.MakeVariant(byte(2)), got.Hints["urgency"])
	assert.Equal(t, []string{"approve", "Approve", "deny", "Deny"}, got.Actions)
	assert.Equal(t, "secrets-dispatcher", got.AppName)
	assert.Equal(t, "Secret requested", got.Summary)
}

func TestNotifyPersistentSendsNeverExpiringCritical(t *testing.T) {
	stub := newNotificationBus(t)

	n, err := NewDBusNotifier()
	require.NoError(t, err)
	defer n.Stop()

	_, err = n.NotifyPersistent("Upstream slow", "body", "dialog-password")
	require.NoError(t, err)

	got := stub.last(t)
	assert.Equal(t, int32(0), got.ExpireTimeout)
	assert.Equal(t, dbus.MakeVariant(byte(2)), got.Hints["urgency"])
	assert.Empty(t, got.Actions)
}

// TestActionDeliverySurvivesUndrainedClosedBuffer reproduces the wedge that
// made approval buttons stop working after ~1h of use. Only the e2e notifprobe
// drains ClosedEvents(); the daemon never does, so a blocking send into the
// closed buffer eventually fills it and freezes processSignals — after which
// every ActionInvoked (every Approve click) is silently dropped and requests
// hang until the client times out. Here we flood far more NotificationClosed
// signals than the buffer holds WITHOUT draining ClosedEvents() (as the daemon
// runs), then fire one ActionInvoked and require it to still arrive. Fails
// (times out) against a blocking send; passes now that processSignals drops
// instead of blocking.
func TestActionDeliverySurvivesUndrainedClosedBuffer(t *testing.T) {
	stub := newNotificationBus(t)

	n, err := NewDBusNotifier()
	require.NoError(t, err)
	defer n.Stop()

	// Overrun both the godbus signal buffer and the closed buffer (16 each) so
	// a blocking closed-send would have parked processSignals for good. We never
	// read ClosedEvents(), mirroring production where nothing consumes it.
	for i := range 64 {
		require.NoError(t, stub.conn.Emit(notifyPath, notifyInterface.Member("NotificationClosed"), uint32(i), uint32(2)))
	}
	// Give the (possibly wedged) loop a moment to process the flood.
	time.Sleep(200 * time.Millisecond)

	require.NoError(t, stub.conn.Emit(notifyPath, notifyInterface.Member("ActionInvoked"), uint32(999), "approve"))

	select {
	case a := <-n.Actions():
		assert.Equal(t, NotificationID(999), a.NotificationID)
		assert.Equal(t, actionKey("approve"), a.ActionKey)
	case <-time.After(5 * time.Second):
		t.Fatal("ActionInvoked was not delivered — processSignals wedged on the undrained closed buffer")
	}
}

func TestClosedEventsDeliversNotificationClosed(t *testing.T) {
	newNotificationBus(t)

	n, err := NewDBusNotifier()
	require.NoError(t, err)
	defer n.Stop()

	id, err := n.Notify("to be closed", "body", "", nil)
	require.NoError(t, err)
	require.NoError(t, n.Close(id))

	select {
	case c := <-n.ClosedEvents():
		assert.Equal(t, id, c.NotificationID)
		assert.Equal(t, uint32(3), c.Reason)
	case <-time.After(5 * time.Second):
		t.Fatal("no Closed event within 5s")
	}
}
