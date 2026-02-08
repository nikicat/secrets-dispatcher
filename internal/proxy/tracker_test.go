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
	clientUniqueName := clientConn.Names()[0] // Get the unique name like ":1.123"
	t.Logf("Client unique name: %s", clientUniqueName)

	// Disconnect the client
	clientConn.Close()
	t.Log("Client disconnected")

	// Wait for NameOwnerChanged to be processed
	// This simulates the race where the signal arrives before contextForSender is called
	time.Sleep(200 * time.Millisecond)

	// Now call contextForSender for the disconnected client
	// Without the fix, this would return a context that never gets cancelled
	ctx := tracker.contextForSender(context.Background(), clientUniqueName)

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
	approvalMgr := approval.NewManager(30*time.Second, 100)

	// Connect a client and get its unique name
	clientConn, err := dbus.Connect(addr)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	clientUniqueName := clientConn.Names()[0]
	t.Logf("Client unique name: %s", clientUniqueName)

	// Disconnect the client BEFORE we start tracking
	clientConn.Close()
	t.Log("Client disconnected")

	// Wait for NameOwnerChanged to be processed
	time.Sleep(200 * time.Millisecond)

	// Now simulate the handler flow: contextForSender + RequireApproval
	ctx := tracker.contextForSender(context.Background(), clientUniqueName)

	// Start RequireApproval in a goroutine (it should return quickly)
	done := make(chan error, 1)
	go func() {
		items := []approval.ItemInfo{{Path: "/test/item", Label: "Test"}}
		err := approvalMgr.RequireApproval(ctx, "test-client", items, "session", approval.RequestTypeGetSecret, nil, approval.SenderInfo{})
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
	clientUniqueName := clientConn.Names()[0]
	t.Logf("Client unique name: %s", clientUniqueName)

	// Create context for the client WHILE it's still connected
	ctx := tracker.contextForSender(context.Background(), clientUniqueName)

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
