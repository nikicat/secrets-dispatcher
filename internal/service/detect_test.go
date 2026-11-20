package service

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/procutil"
	"github.com/nikicat/secrets-dispatcher/internal/testutil"
)

func mockExe(t *testing.T, fn func(procutil.PID) (procutil.Path, bool)) {
	t.Helper()
	testutil.Swap(t, &exeOf, fn)
}

// Makes every process's executable resolve to the given path.
func mockExePath(t *testing.T, path procutil.Path) {
	t.Helper()
	mockExe(t, func(procutil.PID) (procutil.Path, bool) { return path, true })
}

// A session bus on which owner owns org.freedesktop.secrets, and nothing
// does when owner is 0.
type fakeSession struct {
	owner procutil.PID
	// The number of reloadConfig calls.
	reloads int
	// The processes sent SIGTERM, in order.
	terminated []procutil.PID
}

func (s *fakeSession) secretsOwner(context.Context) (procutil.PID, error) {
	if s.owner == 0 {
		return 0, errors.New("the name has no owner")
	}
	return s.owner, nil
}

func (s *fakeSession) reloadConfig(context.Context) error {
	s.reloads++
	return nil
}

func (s *fakeSession) pokeSecrets(context.Context) error { return nil }

// Replaces the session bus with a fakeSession for the given owner. Also
// replaces terminate, so that the fake records each pid instead of signalling it.
func mockSession(t *testing.T, owner procutil.PID) *fakeSession {
	t.Helper()
	s := &fakeSession{owner: owner}
	testutil.Swap[sessionBus](t, &session, s)
	testutil.Swap(t, &terminate, func(pid procutil.PID) error {
		s.terminated = append(s.terminated, pid)
		return nil
	})
	return s
}

// A provider whose owner runs exe.
func ownedBy(exe procutil.Path) Provider { return Provider{&Owner{Exe: &exe}} }

func TestOwnerKindClassifiesByExe(t *testing.T) {
	tests := []struct {
		exe  procutil.Path
		want ProviderKind
	}{
		{"/usr/bin/gnome-keyring-daemon", ProviderGnomeKeyring},
		{"/usr/bin/gopass-secret", ProviderGopass},
		{"/usr/bin/kwalletd6", ProviderKWallet},
		{"/usr/bin/kwalletd5", ProviderKWallet},
		{"/home/user/.local/bin/secrets-dispatcher", ProviderDispatcher},
		{"/usr/bin/some-other-daemon", ProviderUnknown},
	}

	for _, tc := range tests {
		t.Run(string(tc.exe), func(t *testing.T) {
			assert.Equal(t, tc.want, ownedBy(tc.exe).Owner.Kind())
		})
	}
}

func TestDetectProviderResolvesExe(t *testing.T) {
	exe := procutil.Path("/usr/bin/gnome-keyring-daemon")
	mockSession(t, 4242)
	mockExe(t, func(pid procutil.PID) (procutil.Path, bool) {
		require.Equal(t, procutil.PID(4242), pid)
		return exe, true
	})

	assert.Equal(t, Provider{&Owner{PID: 4242, Exe: &exe}}, DetectProvider(t.Context()))
}

func TestDetectProviderNameUnowned(t *testing.T) {
	mockSession(t, 0)

	assert.Equal(t, Provider{}, DetectProvider(t.Context()))
}

func TestDetectProviderUnreadableExe(t *testing.T) {
	// PID resolved but /proc/<pid>/exe unreadable: owned by something unknown.
	mockSession(t, 977)
	mockExe(t, func(procutil.PID) (procutil.Path, bool) { return "", false })

	p := DetectProvider(t.Context())
	assert.Equal(t, Provider{&Owner{PID: 977}}, p)
	assert.Equal(t, ProviderUnknown, p.Owner.Kind())
}

// A dispatcher still running after an upgrade replaced its binary on disk is
// the dispatcher, though Linux marks its exe "(deleted)".
func TestDetectProviderReplacedBinary(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "secrets-dispatcher")
	data, err := os.ReadFile("/bin/sleep")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(exe, data, 0o755))
	cmd := exec.Command(exe, "60")
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})
	require.NoError(t, os.Remove(exe))
	pid := procutil.PID(cmd.Process.Pid)
	require.Eventually(t, func() bool { return slices.Equal(pid.Cmdline(), []string{exe, "60"}) }, 5*time.Second, 10*time.Millisecond)
	mockSession(t, pid)

	p := DetectProvider(t.Context())
	assert.Equal(t, Provider{&Owner{PID: pid, Exe: new(procutil.Path(exe))}}, p)
	assert.Equal(t, ProviderDispatcher, p.Owner.Kind())
}
