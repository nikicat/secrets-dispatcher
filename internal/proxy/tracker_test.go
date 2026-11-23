package proxy

import (
	"context"
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/testutil"
)

// A dbus-daemon and a clientTracker watching it, both closed when the test ends.
type trackerEnv struct {
	t       *testing.T
	addr    string
	tracker *clientTracker
}

func newTrackerEnv(t *testing.T) *trackerEnv {
	t.Helper()
	addr := testutil.StartDBusDaemon(t)
	conn, err := dbus.Connect(addr)
	require.NoError(t, err, "connect tracker")
	t.Cleanup(func() { conn.Close() })

	tracker, err := newClientTracker(conn)
	require.NoError(t, err, "create tracker")
	t.Cleanup(tracker.close)
	return &trackerEnv{t: t, addr: addr, tracker: tracker}
}

func (e *trackerEnv) connect() *dbus.Conn {
	e.t.Helper()
	conn, err := dbus.Connect(e.addr)
	require.NoError(e.t, err, "connect client")
	return conn
}

// Closes the given client and returns once the tracker has seen it leave the
// bus. The bus delivers these signals in order, so the tracker sees a marker
// client closed afterwards leave only after it.
func (e *trackerEnv) disconnectHandled(client *dbus.Conn) {
	e.t.Helper()
	marker := e.connect()
	markerCtx, release := e.tracker.contextForSender(context.Background(), uniqueName(marker))
	defer release()

	require.NoError(e.t, client.Close())
	require.NoError(e.t, marker.Close())
	requireCancelled(e.t, markerCtx, "tracker did not handle the marker client's disconnect")
}

// Checks that clientTracker.contextForSender returns a cancelled context for a
// client that disconnected before the call, as a client killed right after
// calling does.
func TestTrackerContextForDisconnectedClient(t *testing.T) {
	env := newTrackerEnv(t)

	client := env.connect()

	// The NameOwnerChanged signal arrives before contextForSender is called.
	env.disconnectHandled(client)

	// Without the fix, this would return a context that never gets cancelled
	ctx, release := env.tracker.contextForSender(context.Background(), uniqueName(client))
	defer release()

	requireCancelled(t, ctx, "Context was NOT cancelled for disconnected client - this is the bug!")
}

// A call that arrives after the tracker closed, while the proxy shuts down, gets
// a cancelled context.
func TestTrackerContextAfterClose(t *testing.T) {
	env := newTrackerEnv(t)
	env.tracker.close()

	ctx, release := env.tracker.contextForSender(context.Background(), uniqueName(env.connect()))
	defer release()

	assert.Error(t, ctx.Err(), "context of a request after close is not cancelled")
}

// Checks that an approval request from a client that disconnected before
// clientTracker.contextForSender ran is cancelled and leaves the pending list.
func TestTrackerDisconnectedClientRequestNotPending(t *testing.T) {
	env := newTrackerEnv(t)

	// Create approval manager
	approvalMgr := approval.NewManagerForTest(approval.ManagerConfig{})

	client := env.connect()

	// Disconnect the client BEFORE we start tracking
	env.disconnectHandled(client)

	// Now simulate the handler flow: contextForSender + RequireApproval
	ctx, release := env.tracker.contextForSender(context.Background(), uniqueName(client))
	defer release()

	// Start RequireApproval in a goroutine (it should return quickly)
	done := make(chan error, 1)
	go func() {
		items := []approval.ItemInfo{{Path: "/test/item", Label: "Test"}}
		_, err := approvalMgr.RequireApproval(ctx, approval.RequestInfo{Items: items, Session: "session", Type: approval.RequestTypeGetSecret})
		done <- err
	}()

	err := testutil.Recv(t, done, testutil.BusWait, "RequireApproval did not return - request is stuck pending!")
	assert.ErrorIs(t, err, context.Canceled)

	// Verify no pending requests remain
	assert.Zero(t, approvalMgr.PendingCount(), "pending requests remain - this is the bug!")
}

// Checks that a tracked client's context is cancelled when it disconnects.
func TestTrackerContextCancelledOnDisconnect(t *testing.T) {
	env := newTrackerEnv(t)

	client := env.connect()

	// Create context for the client WHILE it's still connected
	ctx, release := env.tracker.contextForSender(context.Background(), uniqueName(client))
	defer release()

	requireNotCancelled(t, ctx, "Context should not be cancelled while client is connected")

	// Now disconnect the client
	client.Close()
	t.Log("Client disconnected")

	requireCancelled(t, ctx, "Context was NOT cancelled after client disconnect")
}

// Fails the test at once if the given context is already cancelled.
func requireNotCancelled(t *testing.T, ctx context.Context, msg string) {
	t.Helper()
	testutil.NoRecv(t, ctx.Done(), msg)
}

// Fails the test if the given context is not cancelled within testutil.BusWait.
func requireCancelled(t *testing.T, ctx context.Context, msg string) {
	t.Helper()
	testutil.Recv(t, ctx.Done(), testutil.BusWait, msg)
}

// Checks that two requests in flight from one sender are tracked apart
// (issue #6): neither starting nor finishing one cancels the other, and only
// the sender's disconnect cancels what is still pending.
func TestTrackerConcurrentRequestsFromSameSender(t *testing.T) {
	env := newTrackerEnv(t)

	client := env.connect()
	sender := uniqueName(client)

	// Two overlapping requests from the SAME still-connected sender.
	ctxA, releaseA := env.tracker.contextForSender(context.Background(), sender)
	ctxB, releaseB := env.tracker.contextForSender(context.Background(), sender)
	defer releaseB()

	// Registering the second request must not have cancelled the first.
	requireNotCancelled(t, ctxA, "first request was cancelled when a second concurrent request was registered (issue #6)")
	requireNotCancelled(t, ctxB, "second request was cancelled on registration")

	// Completing one request must not cancel its still-pending sibling.
	releaseA()
	requireCancelled(t, ctxA, "released request was not cancelled")
	requireNotCancelled(t, ctxB, "sibling request was cancelled when the other request completed (issue #6)")

	// Disconnecting the sender cancels everything still pending for it.
	client.Close()
	t.Log("Client disconnected")
	requireCancelled(t, ctxB, "surviving request was not cancelled after sender disconnect")
}
