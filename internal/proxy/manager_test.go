package proxy

import (
	"context"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

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
	approvalMgr := approval.NewManager(approval.ManagerConfig{Timeout: 5 * time.Minute, HistoryMax: 100})

	mgr, err := NewManager(tempDir, "", approvalMgr, slog.LevelInfo, nil, 0)
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
	approvalMgr := approval.NewManager(approval.ManagerConfig{Timeout: 5 * time.Minute, HistoryMax: 100})

	mgr, err := NewManager(tempDir, "", approvalMgr, slog.LevelInfo, nil, 0)
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
	approvalMgr := approval.NewManager(approval.ManagerConfig{Timeout: 5 * time.Minute, HistoryMax: 100})

	mgr, err := NewManager(tempDir, "", approvalMgr, slog.LevelInfo, nil, 0)
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

func TestManager_StartProxy_ClientNameHeld(t *testing.T) {
	tempDir := t.TempDir()
	approvalMgr := approval.NewManager(approval.ManagerConfig{Timeout: 5 * time.Minute, HistoryMax: 100})
	mgr, err := NewManager(tempDir, "unix:path="+filepath.Join(tempDir, "no-upstream"), approvalMgr, slog.LevelInfo, nil, 0)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	defer mgr.watcher.Close()
	socketPath := filepath.Join(tempDir, "vm.sock")

	// Another bus holds "vm", so the socket is skipped and the name stays with its holder.
	approvalMgr.ClaimClient("vm")
	mgr.startProxy(t.Context(), socketPath)
	if approvalMgr.ClaimClient("vm") {
		t.Fatal("skipped socket released a name another bus holds")
	}

	// A proxy that fails to start gives its name back.
	approvalMgr.ReleaseClient("vm")
	mgr.startProxy(t.Context(), socketPath)
	if !approvalMgr.ClaimClient("vm") {
		t.Fatal("failed proxy kept its client name")
	}
}

func TestManager_StopProxy_WaitsForName(t *testing.T) {
	tempDir := t.TempDir()
	approvalMgr := approval.NewManager(approval.ManagerConfig{Timeout: 5 * time.Minute, HistoryMax: 100})
	mgr, err := NewManager(tempDir, "", approvalMgr, slog.LevelInfo, nil, 0)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}
	defer mgr.watcher.Close()
	socketPath := filepath.Join(tempDir, "vm.sock")

	// A proxy whose downstream stays open runs until it is stopped.
	front, _ := net.Pipe()
	frontConn, err := dbus.NewConn(front)
	if err != nil {
		t.Fatalf("NewConn failed: %v", err)
	}
	p := New(Config{ClientName: "vm", Approval: approvalMgr})
	p.frontConn = frontConn
	approvalMgr.ClaimClient("vm")
	ctx, cancel := context.WithCancel(t.Context())
	inst := &proxyInstance{proxy: p, cancel: cancel, done: make(chan struct{})}
	mgr.proxies.add(socketPath, inst)
	go mgr.serve(ctx, socketPath, inst)

	mgr.stopProxy(socketPath)
	if !approvalMgr.ClaimClient("vm") {
		t.Fatal("stopProxy returned before the client name was free")
	}
	if len(mgr.Clients()) != 0 {
		t.Fatal("stopped proxy is still listed")
	}
}
