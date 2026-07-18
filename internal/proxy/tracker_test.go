package proxy

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

// startTestDBusDaemon starts a dbus-daemon on the given socket path.
func startTestDBusDaemon(t *testing.T, socketPath string) (*exec.Cmd, string) {
	t.Helper()

	addr := "unix:path=" + socketPath

	cmd := exec.Command("dbus-daemon",
		"--session",
		"--nofork",
		"--address="+addr,
	)

	if err := cmd.Start(); err != nil {
		t.Fatalf("start dbus-daemon: %v", err)
	}

	// Wait for socket to be created
	for range 50 {
		if _, err := os.Stat(socketPath); err == nil {
			return cmd, addr
		}
		time.Sleep(100 * time.Millisecond)
	}

	cmd.Process.Kill()
	t.Fatalf("dbus-daemon socket not created: %s", socketPath)
	return nil, ""
}

// TestTrackerContextForDisconnectedClient verifies that when contextForSender
// is called for a client that has already disconnected, the returned context
// is already cancelled.
//
// This tests the race condition where:
// 1. Client sends a D-Bus method call
// 2. Client immediately disconnects (Ctrl+C)
// 3. NameOwnerChanged signal is processed (but sender not tracked yet)
// 4. Handler runs and calls contextForSender
// 5. Without the fix, the context would never be cancelled
func TestTrackerContextForDisconnectedClient(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")
	cmd, addr := startTestDBusDaemon(t, socketPath)
	defer func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()

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
	clientUniqueName := senderName(clientConn.Names()[0]) // unique name like ":1.123"
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

// TestTrackerDisconnectedClientRequestNotPending verifies that when a client
// has already disconnected before contextForSender is called, the approval
// request is immediately cancelled and removed from pending.
func TestTrackerDisconnectedClientRequestNotPending(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")
	cmd, addr := startTestDBusDaemon(t, socketPath)
	defer func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()

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
	clientUniqueName := senderName(clientConn.Names()[0])
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
		_, err := approvalMgr.RequireApproval(ctx, "test-client", items, "session", approval.RequestTypeGetSecret, nil, approval.SenderInfo{})
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

// TestTrackerContextCancelledOnDisconnect verifies the normal case where
// a client disconnects while being tracked, and the context is cancelled.
func TestTrackerContextCancelledOnDisconnect(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")
	cmd, addr := startTestDBusDaemon(t, socketPath)
	defer func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()

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
	clientUniqueName := senderName(clientConn.Names()[0])
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

// requireNotCancelled fails the test immediately if ctx is already cancelled.
func requireNotCancelled(t *testing.T, ctx context.Context, msg string) {
	t.Helper()
	select {
	case <-ctx.Done():
		t.Fatal(msg)
	default:
	}
}

// requireCancelled fails the test if ctx is not cancelled within d.
func requireCancelled(t *testing.T, ctx context.Context, d time.Duration, msg string) {
	t.Helper()
	select {
	case <-ctx.Done():
	case <-time.After(d):
		t.Error(msg)
	}
}

// TestTrackerConcurrentRequestsFromSameSender is a regression test for issue #6:
// two approval requests in flight at the same time from a single D-Bus sender
// must be tracked independently. Registering a second request must not cancel
// the first, completing one must not cancel the other, and only an actual
// disconnect cancels whatever is still pending.
//
// The old tracker kept a single cancel function per sender, so a second
// concurrent request cancelled the first (its approval notification vanished),
// and one request's cleanup cancelled the other.
func TestTrackerConcurrentRequestsFromSameSender(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")
	cmd, addr := startTestDBusDaemon(t, socketPath)
	defer func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()

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
	clientUniqueName := senderName(clientConn.Names()[0])
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
