package proxy

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/sender"
	"github.com/nikicat/secrets-dispatcher/internal/testutil"
	"github.com/nikicat/secrets-dispatcher/internal/unixsock"
)

func connectSessionBus() (*dbus.Conn, error) { return dbus.ConnectSessionBus() }

func newTestManager(t *testing.T, dir unixsock.Dir, connect Connector) (*Manager, *approval.Manager) {
	t.Helper()
	approvalMgr := approval.NewManagerForTest(approval.ManagerConfig{})
	mgr, err := NewManager(dir, NewFactory(connect, approvalMgr, SlowUpstream{}, sender.ChainReader{}))
	require.NoError(t, err, "NewManager")
	t.Cleanup(func() { mgr.watcher.Close() })
	return mgr, approvalMgr
}

func TestNewManager(t *testing.T) {
	tempDir := unixsock.Dir(t.TempDir())
	mgr, approvalMgr := newTestManager(t, tempDir, connectSessionBus)

	assert.Equal(t, tempDir, mgr.socketsDir)
	assert.Same(t, approvalMgr, mgr.factory.approval, "approval manager not set correctly")
}

func TestManager_ScanExistingSockets_NoSockets(t *testing.T) {
	tempDir := t.TempDir()
	mgr, _ := newTestManager(t, unixsock.Dir(tempDir), connectSessionBus)

	// Create a non-socket file
	require.NoError(t, os.WriteFile(filepath.Join(tempDir, "notasocket.txt"), []byte("test"), 0644))

	// Create a directory
	require.NoError(t, os.Mkdir(filepath.Join(tempDir, "subdir"), 0755))

	require.NoError(t, mgr.scanExistingSockets(t.Context()))
	assert.Empty(t, mgr.Clients())
}

func TestManager_StartProxy_ClientNameHeld(t *testing.T) {
	tempDir := unixsock.Dir(t.TempDir())
	mgr, approvalMgr := newTestManager(t, tempDir, tempDir.Socket("no-upstream").ConnectDBus)
	socketPath := tempDir.Socket("vm")

	// Another bus holds "vm", so the socket is skipped and the name stays with its holder.
	approvalMgr.ClaimClient("vm")
	mgr.startProxy(t.Context(), socketPath)
	require.False(t, approvalMgr.ClaimClient("vm"), "skipped socket released a name another bus holds")

	// A proxy that fails to start gives its name back.
	approvalMgr.ReleaseClient("vm")
	mgr.startProxy(t.Context(), socketPath)
	require.True(t, approvalMgr.ClaimClient("vm"), "failed proxy kept its client name")
}

func TestManager_StopProxy_WaitsForName(t *testing.T) {
	tempDir := unixsock.Dir(t.TempDir())
	mgr, approvalMgr := newTestManager(t, tempDir, connectSessionBus)
	socketPath := tempDir.Socket("vm")

	// A proxy whose downstream stays open runs until it is stopped.
	p := startTestProxy(t, testutil.StartDBusDaemon(t), testutil.StartDBusDaemon(t))
	claim, err := mgr.factory.Claim("vm")
	require.NoError(t, err, "Claim")
	ctx, cancel := context.WithCancel(t.Context())
	inst := &proxyInstance{proxy: p, claim: claim, cancel: cancel, done: make(chan struct{})}
	mgr.proxies.add(socketPath, inst)
	go mgr.serve(ctx, socketPath, inst)

	mgr.stopProxy(socketPath)
	require.True(t, approvalMgr.ClaimClient("vm"), "stopProxy returned before the client name was free")
	require.Empty(t, mgr.Clients(), "stopped proxy is still listed")
}
