package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/unixsock"
)

// How long a test waits for a message that crosses dbus-daemon.
const BusWait = 5 * time.Second

// Starts a private session bus and returns its address. The test's cleanup stops it.
// The bus is another process, so a test that uses it runs on the real clock,
// outside synctest.
func StartDBusDaemon(t testing.TB) string {
	t.Helper()
	addr, _ := StartKillableDBusDaemon(t)
	return addr
}

// Works as StartDBusDaemon does, for a test that kills the bus itself before its cleanup runs.
func StartKillableDBusDaemon(t testing.TB) (addr string, kill func()) {
	t.Helper()
	socket := unixsock.Dir(t.TempDir()).Socket("bus")
	kill = startDBusDaemon(t, socket, "--session", "--address="+socket.Address())
	return socket.Address(), kill
}

// Starts a private bus as StartDBusDaemon does, configured by the text that the
// given function returns for the bus's address. The test fails when that
// configuration does not listen on the address.
func StartDBusDaemonWithConfig(t testing.TB, config func(addr string) string) string {
	t.Helper()
	dir := t.TempDir()
	socket := unixsock.Dir(dir).Socket("bus")
	confPath := filepath.Join(dir, "bus.conf")
	require.NoError(t, os.WriteFile(confPath, []byte(config(socket.Address())), 0600), "write dbus-daemon config")
	startDBusDaemon(t, socket, "--config-file="+confPath)
	return socket.Address()
}

func startDBusDaemon(t testing.TB, socket unixsock.Path, args ...string) (kill func()) {
	t.Helper()
	cmd := exec.Command("dbus-daemon", append([]string{"--nofork"}, args...)...)
	require.NoError(t, cmd.Start(), "start dbus-daemon")
	kill = sync.OnceFunc(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})
	t.Cleanup(kill)
	require.Eventually(t, socket.Exists, 5*time.Second, 50*time.Millisecond, "dbus-daemon socket not created: %s", socket)
	return kill
}
