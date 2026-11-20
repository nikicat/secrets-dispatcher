package testutil

import (
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/unixsock"
)

// Starts a private session bus and returns its address. The test's cleanup stops it.
func StartDBusDaemon(t testing.TB) string {
	t.Helper()
	addr, _ := StartKillableDBusDaemon(t)
	return addr
}

// Works as StartDBusDaemon does, for a test that kills the bus itself before its cleanup runs.
func StartKillableDBusDaemon(t testing.TB) (addr string, kill func()) {
	t.Helper()
	socket := unixsock.Dir(t.TempDir()).Socket("bus")
	addr = socket.Address()
	cmd := exec.Command("dbus-daemon", "--session", "--nofork", "--address="+addr)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start dbus-daemon: %v", err)
	}
	kill = sync.OnceFunc(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})
	t.Cleanup(kill)
	require.Eventually(t, socket.Exists, 5*time.Second, 50*time.Millisecond, "dbus-daemon socket not created: %s", socket)
	return addr, kill
}
