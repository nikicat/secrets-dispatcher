package config

import (
	"errors"
	"testing"
)

func TestLookupRuntimeDir(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	dir, err := LookupRuntimeDir()
	if err != nil {
		t.Fatalf("LookupRuntimeDir: %v", err)
	}
	for _, tc := range []struct{ name, got, want string }{
		{"APISocket", dir.APISocket(), "/run/user/1000/secrets-dispatcher/api.sock"},
		{"SSHAgentSocket", dir.SSHAgentSocket(), "/run/user/1000/secrets-dispatcher/ssh-agent.sock"},
		{"ClientSockets", dir.ClientSockets(), "/run/user/1000/secrets-dispatcher/sockets"},
		{"RemoteSocket", dir.RemoteSocket("nb-home"), "/run/user/1000/secrets-dispatcher/remote/nb-home.sock"},
		{"BackendBusSocket", dir.BackendBusSocket(), "/run/user/1000/secrets-dispatcher/backend-bus.sock"},
		{"TrialConfig", dir.TrialConfig(), "/run/user/1000/secrets-dispatcher/try-config.yaml"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

func TestLookupRuntimeDirUnset(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	if _, err := LookupRuntimeDir(); !errors.Is(err, ErrNoRuntimeDir) {
		t.Fatalf("LookupRuntimeDir() error = %v, want ErrNoRuntimeDir", err)
	}
}

func TestLookupRuntimeDirRelative(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "run/user/1000")
	if _, err := LookupRuntimeDir(); !errors.Is(err, ErrNoRuntimeDir) {
		t.Fatalf("LookupRuntimeDir() error = %v, want ErrNoRuntimeDir", err)
	}
}

func TestSystemdRuntimeDir(t *testing.T) {
	for _, tc := range []struct{ name, got, want string }{
		{"BackendBusSocket", SystemdRuntimeDir.BackendBusSocket(), "%t/secrets-dispatcher/backend-bus.sock"},
		{"KeyringControlDir", SystemdRuntimeDir.KeyringControlDir(), "%t/secrets-dispatcher/keyring"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}
