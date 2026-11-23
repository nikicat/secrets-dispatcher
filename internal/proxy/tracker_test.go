package proxy

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/testutil"
)

// Checks that clientTracker.contextForSender returns a cancelled context for a
// client that disconnected before the call, as a client killed right after
// calling does.
func TestTrackerContextForDisconnectedClient(t *testing.T) {
	addr := testutil.StartDBusDaemon(t)

	// Connect the main connection for the tracker
	trackerConn, err := dbus.Connect(addr)
	if err != nil {
		t.Fatalf("connect tracker: %v", err)
	}
	defer trackerConn.Close()

	// Create the tracker
	tracker, err := newClientTracker(trackerConn)
	if err != nil {
		t.Fatalf("create tracker: %v", err)
	}
	defer tracker.close()

	// Connect a client and get its unique name
	clientConn, err := dbus.Connect(addr)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	clientUniqueName := uniqueName(clientConn)
	t.Logf("Client unique name: %s", clientUniqueName)

	// Disconnect the client
	clientConn.Close()
	t.Log("Client disconnected")

	// Wait for NameOwnerChanged to be processed
	// This simulates the race where the signal arrives before contextForSender is called
	time.Sleep(200 * time.Millisecond)

	// Now call contextForSender for the disconnected client
	// Without the fix, this would return a context that never gets cancelled
	ctx, release := tracker.contextForSender(context.Background(), clientUniqueName)
	defer release()

	// Verify the context is already cancelled (or gets cancelled very quickly)
	select {
	case <-ctx.Done():
		t.Log("Context correctly cancelled for disconnected client")
	case <-time.After(500 * time.Millisecond):
		t.Error("Context was NOT cancelled for disconnected client - this is the bug!")
	}
}

// A call that arrives after the tracker closed, while the proxy shuts down, gets
// a cancelled context.
func TestTrackerContextAfterClose(t *testing.T) {
	addr := testutil.StartDBusDaemon(t)
	conn, err := dbus.Connect(addr)
	require.NoError(t, err)
	defer conn.Close()
	tracker, err := newClientTracker(conn)
	require.NoError(t, err)
	tracker.close()

	ctx, release := tracker.contextForSender(context.Background(), uniqueName(conn))
	defer release()

	assert.Error(t, ctx.Err(), "context of a request after close is not cancelled")
}

// Checks that an approval request from a client that disconnected before
// clientTracker.contextForSender ran is cancelled and leaves the pending list.
func TestTrackerDisconnectedClientRequestNotPending(t *testing.T) {
	addr := testutil.StartDBusDaemon(t)

	// Connect the main connection for the tracker
	trackerConn, err := dbus.Connect(addr)
	if err != nil {
		t.Fatalf("connect tracker: %v", err)
	}
	defer trackerConn.Close()

	// Create the tracker
	tracker, err := newClientTracker(trackerConn)
	if err != nil {
		t.Fatalf("create tracker: %v", err)
	}
	defer tracker.close()

	// Create approval manager
	approvalMgr := approval.NewManager(approval.ManagerConfig{Timeout: 30 * time.Second, HistoryMax: 100})

	// Connect a client and get its unique name
	clientConn, err := dbus.Connect(addr)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	clientUniqueName := uniqueName(clientConn)
	t.Logf("Client unique name: %s", clientUniqueName)

	// Disconnect the client BEFORE we start tracking
	clientConn.Close()
	t.Log("Client disconnected")

	// Wait for NameOwnerChanged to be processed
	time.Sleep(200 * time.Millisecond)

	// Now simulate the handler flow: contextForSender + RequireApproval
	ctx, release := tracker.contextForSender(context.Background(), clientUniqueName)
	defer release()

	// Start RequireApproval in a goroutine (it should return quickly)
	done := make(chan error, 1)
	go func() {
		items := []approval.ItemInfo{{Path: "/test/item", Label: "Test"}}
		_, err := approvalMgr.RequireApproval(ctx, approval.RequestInfo{Items: items, Session: "session", Type: approval.RequestTypeGetSecret})
		done <- err
	}()

	// Wait for RequireApproval to return
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		} else {
			t.Log("RequireApproval correctly returned context.Canceled")
		}
	case <-time.After(2 * time.Second):
		t.Error("RequireApproval did not return - request is stuck pending!")
	}

	// Verify no pending requests remain
	if count := approvalMgr.PendingCount(); count != 0 {
		t.Errorf("expected 0 pending requests, got %d - this is the bug!", count)
	} else {
		t.Log("No pending requests remain (correct)")
	}
}

// Checks that a tracked client's context is cancelled when it disconnects.
func TestTrackerContextCancelledOnDisconnect(t *testing.T) {
	addr := testutil.StartDBusDaemon(t)

	// Connect the main connection for the tracker
	trackerConn, err := dbus.Connect(addr)
	if err != nil {
		t.Fatalf("connect tracker: %v", err)
	}
	defer trackerConn.Close()

	// Create the tracker
	tracker, err := newClientTracker(trackerConn)
	if err != nil {
		t.Fatalf("create tracker: %v", err)
	}
	defer tracker.close()

	// Connect a client
	clientConn, err := dbus.Connect(addr)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	clientUniqueName := uniqueName(clientConn)
	t.Logf("Client unique name: %s", clientUniqueName)

	// Create context for the client WHILE it's still connected
	ctx, release := tracker.contextForSender(context.Background(), clientUniqueName)
	defer release()

	// Verify context is not cancelled yet
	select {
	case <-ctx.Done():
		t.Fatal("Context should not be cancelled while client is connected")
	default:
		t.Log("Context correctly not cancelled while client connected")
	}

	// Now disconnect the client
	clientConn.Close()
	t.Log("Client disconnected")

	// Wait for context to be cancelled
	select {
	case <-ctx.Done():
		t.Log("Context correctly cancelled after disconnect")
	case <-time.After(2 * time.Second):
		t.Error("Context was NOT cancelled after client disconnect")
	}
}

// Fails the test at once if the given context is already cancelled.
func requireNotCancelled(t *testing.T, ctx context.Context, msg string) {
	t.Helper()
	select {
	case <-ctx.Done():
		t.Fatal(msg)
	default:
	}
}

// Fails the test if the given context is not cancelled within the given time.
func requireCancelled(t *testing.T, ctx context.Context, d time.Duration, msg string) {
	t.Helper()
	select {
	case <-ctx.Done():
	case <-time.After(d):
		t.Error(msg)
	}
}

// Checks that two requests in flight from one sender are tracked apart
// (issue #6): neither starting nor finishing one cancels the other, and only
// the sender's disconnect cancels what is still pending.
func TestTrackerConcurrentRequestsFromSameSender(t *testing.T) {
	addr := testutil.StartDBusDaemon(t)

	// Connect the main connection for the tracker
	trackerConn, err := dbus.Connect(addr)
	if err != nil {
		t.Fatalf("connect tracker: %v", err)
	}
	defer trackerConn.Close()

	// Create the tracker
	tracker, err := newClientTracker(trackerConn)
	if err != nil {
		t.Fatalf("create tracker: %v", err)
	}
	defer tracker.close()

	// Connect a single client that will issue two overlapping requests
	clientConn, err := dbus.Connect(addr)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	clientUniqueName := uniqueName(clientConn)
	t.Logf("Client unique name: %s", clientUniqueName)

	// Two overlapping requests from the SAME still-connected sender.
	ctxA, releaseA := tracker.contextForSender(context.Background(), clientUniqueName)
	ctxB, releaseB := tracker.contextForSender(context.Background(), clientUniqueName)
	defer releaseB()

	// Registering the second request must not have cancelled the first.
	requireNotCancelled(t, ctxA, "first request was cancelled when a second concurrent request was registered (issue #6)")
	requireNotCancelled(t, ctxB, "second request was cancelled on registration")

	// Completing one request must not cancel its still-pending sibling.
	releaseA()
	requireCancelled(t, ctxA, 500*time.Millisecond, "released request was not cancelled")
	requireNotCancelled(t, ctxB, "sibling request was cancelled when the other request completed (issue #6)")

	// Disconnecting the sender cancels everything still pending for it.
	clientConn.Close()
	t.Log("Client disconnected")
	requireCancelled(t, ctxB, 2*time.Second, "surviving request was not cancelled after sender disconnect")
}
