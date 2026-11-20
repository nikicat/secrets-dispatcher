package service

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mockReadlink(t *testing.T, fn func(string) (string, error)) {
	t.Helper()
	orig := readlinkFunc
	readlinkFunc = fn
	t.Cleanup(func() { readlinkFunc = orig })
}

// mockBusctlOwner makes the busctl GetConnectionUnixProcessID call report pid
// as the owner of org.freedesktop.secrets.
func mockBusctlOwner(t *testing.T, pid int) {
	t.Helper()
	mockExecOutput(t, func(name string, args ...string) ([]byte, error) {
		return fmt.Appendf(nil, "u %d\n", pid), nil
	})
}

func TestDetectProviderClassifiesByExe(t *testing.T) {
	tests := []struct {
		exe  string
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
		t.Run(tc.exe, func(t *testing.T) {
			mockBusctlOwner(t, 4242)
			mockReadlink(t, func(path string) (string, error) {
				require.Equal(t, "/proc/4242/exe", path)
				return tc.exe, nil
			})

			p := DetectProvider()
			assert.Equal(t, tc.want, p.Kind)
			assert.Equal(t, 4242, p.PID)
			assert.Equal(t, tc.exe, p.Exe)
		})
	}
}

func TestDetectProviderNameUnowned(t *testing.T) {
	mockExecOutput(t, func(string, ...string) ([]byte, error) {
		return nil, fmt.Errorf("Could not get owner: no such name")
	})

	p := DetectProvider()
	assert.Equal(t, ProviderNone, p.Kind)
	assert.Zero(t, p.PID)
}

func TestDetectProviderUnreadableExe(t *testing.T) {
	// PID resolved but /proc/<pid>/exe unreadable: owned by something unknown.
	mockBusctlOwner(t, 977)
	mockReadlink(t, func(string) (string, error) {
		return "", fmt.Errorf("permission denied")
	})

	p := DetectProvider()
	assert.Equal(t, ProviderUnknown, p.Kind)
	assert.Equal(t, 977, p.PID)
	assert.Empty(t, p.Exe)
}

func TestDetectProviderReadsDesktop(t *testing.T) {
	t.Setenv("XDG_CURRENT_DESKTOP", "ubuntu:GNOME")
	mockExecOutput(t, func(string, ...string) ([]byte, error) {
		return nil, fmt.Errorf("no such name")
	})

	assert.Equal(t, "ubuntu:GNOME", DetectProvider().Desktop)
}
