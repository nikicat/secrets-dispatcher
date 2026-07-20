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
		{"/usr/bin/gopass-secret-service", ProviderGopass},
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

func TestResolveBackendExec(t *testing.T) {
	mockLookPath(t, defaultLookPath)

	gk := Provider{Kind: ProviderGnomeKeyring}
	none := Provider{Kind: ProviderNone}

	tests := []struct {
		name     string
		value    string
		provider Provider
		want     string
		wantErr  string
	}{
		{
			// gopass carries --bus-address: it ignores DBUS_SESSION_BUS_ADDRESS
			// (which the backend unit sets for gnome-keyring) and would otherwise
			// crash-loop unable to reach the private backend bus.
			name:  "default without gnome-keyring is gopass",
			value: "", provider: none,
			want: "/usr/bin/gopass-secret-service --bus-address unix:path=%t/secrets-dispatcher/backend-bus.sock",
		},
		{
			name:  "default with gnome-keyring provider demotes it to private backend",
			value: "", provider: gk,
			want: "/usr/bin/gnome-keyring-daemon --foreground --components=secrets --control-directory=%t/secrets-dispatcher/keyring",
		},
		{
			name: "explicit gopass keyword", value: "gopass", provider: gk,
			want: "/usr/bin/gopass-secret-service --bus-address unix:path=%t/secrets-dispatcher/backend-bus.sock",
		},
		{
			name: "explicit gnome-keyring keyword", value: "gnome-keyring", provider: none,
			want: "/usr/bin/gnome-keyring-daemon --foreground --components=secrets --control-directory=%t/secrets-dispatcher/keyring",
		},
		{
			name: "raw path passes through verbatim", value: "/opt/custom/backend --flag", provider: gk,
			want: "/opt/custom/backend --flag",
		},
		{
			name: "unknown keyword errors", value: "keepassxc", provider: none,
			wantErr: `unknown backend "keepassxc"`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveBackendExec(tc.value, tc.provider)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
