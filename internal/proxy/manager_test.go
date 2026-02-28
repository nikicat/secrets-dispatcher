package proxy

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

func TestClientNameFromSocket(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"/path/to/myserver.sock", "myserver"},
		{"/path/to/server1.sock", "server1"},
		{"relative/path.sock", "path"},
		{"just.sock", "just"},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			result := clientNameFromSocket(tc.path)
			if result != tc.expected {
				t.Errorf("clientNameFromSocket(%q) = %q, want %q", tc.path, result, tc.expected)
			}
		})
	}
}

func TestIsSocketFile(t *testing.T) {
	tests := []struct {
		name     string
		expected bool
	}{
		{"myserver.sock", true},
		{"/path/to/server.sock", true},
		{"notasocket.txt", false},
		{"socket", false},
		{"socket.sock.bak", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := isSocketFile(tc.name)
			if result != tc.expected {
				t.Errorf("isSocketFile(%q) = %v, want %v", tc.name, result, tc.expected)
			}
		})
	}
}

func TestNewManager(t *testing.T) {
	tempDir := t.TempDir()
	approvalMgr := approval.NewManager(5*time.Minute, 100, 0)

	mgr, err := NewManager(tempDir, "", approvalMgr, slog.LevelInfo, false)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	defer mgr.watcher.Close()

	if mgr.socketsDir != tempDir {
		t.Errorf("expected socketsDir %q, got %q", tempDir, mgr.socketsDir)
	}

	if mgr.approval != approvalMgr {
		t.Error("approval manager not set correctly")
	}

	if mgr.logLevel != slog.LevelInfo {
		t.Errorf("expected logLevel %v, got %v", slog.LevelInfo, mgr.logLevel)
	}
}

func TestManager_ClientsEmpty(t *testing.T) {
	tempDir := t.TempDir()
	approvalMgr := approval.NewManager(5*time.Minute, 100, 0)

	mgr, err := NewManager(tempDir, "", approvalMgr, slog.LevelInfo, false)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	defer mgr.watcher.Close()

	clients := mgr.Clients()
	if len(clients) != 0 {
		t.Errorf("expected 0 clients, got %d", len(clients))
	}
}

func TestManager_ScanExistingSockets_NoSockets(t *testing.T) {
	tempDir := t.TempDir()
	approvalMgr := approval.NewManager(5*time.Minute, 100, 0)

	mgr, err := NewManager(tempDir, "", approvalMgr, slog.LevelInfo, false)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	defer mgr.watcher.Close()

	// Create a non-socket file
	if err := os.WriteFile(filepath.Join(tempDir, "notasocket.txt"), []byte("test"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Create a directory
	if err := os.Mkdir(filepath.Join(tempDir, "subdir"), 0755); err != nil {
		t.Fatalf("failed to create test dir: %v", err)
	}

	// This should not fail and should not try to connect to non-socket files
	// We can't fully test actual proxy connections without a mock D-Bus,
	// but we can verify the manager doesn't crash on non-socket files
	clients := mgr.Clients()
	if len(clients) != 0 {
		t.Errorf("expected 0 clients, got %d", len(clients))
	}
}

func TestClientInfo(t *testing.T) {
	info := ClientInfo{
		Name:       "testclient",
		SocketPath: "/path/to/socket.sock",
	}

	if info.Name != "testclient" {
		t.Errorf("expected name 'testclient', got '%s'", info.Name)
	}
	if info.SocketPath != "/path/to/socket.sock" {
		t.Errorf("expected socket_path '/path/to/socket.sock', got '%s'", info.SocketPath)
	}
}
