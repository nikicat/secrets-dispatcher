package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLookupRuntimeDir(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	dir, err := LookupRuntimeDir()
	require.NoError(t, err)
	for _, tc := range []struct{ name, got, want string }{
		{"APISocket", string(dir.APISocket()), "/run/user/1000/secrets-dispatcher/api.sock"},
		{"SSHAgentSocket", string(dir.SSHAgentSocket()), "/run/user/1000/secrets-dispatcher/ssh-agent.sock"},
		{"ClientSockets", dir.ClientSockets().String(), "/run/user/1000/secrets-dispatcher/sockets"},
		{"RemoteSocket", string(dir.RemoteSocket("nb-home")), "/run/user/1000/secrets-dispatcher/remote/nb-home.sock"},
		{"BackendBusSocket", string(dir.BackendBusSocket()), "/run/user/1000/secrets-dispatcher/backend-bus.sock"},
		{"TrialConfig", dir.TrialConfig().String(), "/run/user/1000/secrets-dispatcher/try-config.yaml"},
	} {
		assert.Equal(t, tc.want, tc.got, tc.name)
	}
}

func TestLookupRuntimeDirUnset(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	_, err := LookupRuntimeDir()
	assert.ErrorIs(t, err, ErrNoRuntimeDir)
}

func TestLookupRuntimeDirRelative(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "run/user/1000")
	_, err := LookupRuntimeDir()
	assert.ErrorIs(t, err, ErrNoRuntimeDir)
}

func TestSystemdRuntimeDir(t *testing.T) {
	for _, tc := range []struct{ name, got, want string }{
		{"BackendBusSocket", string(SystemdRuntimeDir.BackendBusSocket()), "%t/secrets-dispatcher/backend-bus.sock"},
		{"KeyringControlDir", SystemdRuntimeDir.KeyringControlDir().String(), "%t/secrets-dispatcher/keyring"},
	} {
		assert.Equal(t, tc.want, tc.got, tc.name)
	}
}
