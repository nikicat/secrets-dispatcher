package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"
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
	socketPath := filepath.Join(t.TempDir(), "bus.sock")
	addr = "unix:path=" + socketPath
	cmd := exec.Command("dbus-daemon", "--session", "--nofork", "--address="+addr)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start dbus-daemon: %v", err)
	}
	kill = sync.OnceFunc(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})
	t.Cleanup(kill)
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if _, err := os.Stat(socketPath); err == nil {
			return addr, kill
		}
	}
	t.Fatalf("dbus-daemon socket not created: %s", socketPath)
	return "", nil
}
