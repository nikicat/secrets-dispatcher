package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBackendUnmarshalText(t *testing.T) {
	for _, tc := range []struct {
		name string
		text string
		want Backend
	}{
		{"gopass keyword", "gopass", BackendGopass},
		{"gnome-keyring keyword", "gnome-keyring", BackendGnomeKeyring},
		{"command line", "/opt/custom/backend --flag", Backend{backendCommand("/opt/custom/backend --flag")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b Backend
			require.NoError(t, b.UnmarshalText([]byte(tc.text)))
			assert.Equal(t, tc.want, b)
			assert.Equal(t, tc.text, b.String())
		})
	}
	var b Backend
	assert.ErrorContains(t, b.UnmarshalText([]byte("keepassxc")), `backend must be gopass or gnome-keyring, or a command line that contains a slash, got "keepassxc"`)
	assert.ErrorContains(t, b.UnmarshalText(nil), `got ""`, "an empty text names no backend")
}

func TestBackendExec(t *testing.T) {
	mockLookPath(t, defaultLookPath)

	tests := []struct {
		name  string
		value Backend
		want  string
	}{
		{
			name: "gopass keyword", value: BackendGopass,
			want: "/usr/bin/gopass-secret service --bus-address unix:path=%t/secrets-dispatcher/backend-bus.sock",
		},
		{
			name: "gnome-keyring keyword demotes it to a private backend", value: BackendGnomeKeyring,
			want: "/usr/bin/gnome-keyring-daemon --foreground --components=secrets --control-directory=%t/secrets-dispatcher/keyring",
		},
		{
			name: "command line passes through verbatim", value: Backend{backendCommand("/opt/custom/backend --flag")},
			want: "/opt/custom/backend --flag",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.value.exec()
			require.NoError(t, err)
			assert.Equal(t, tc.want, got.ExecLine())
		})
	}
}
