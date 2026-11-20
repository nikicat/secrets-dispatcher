package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBackendSet(t *testing.T) {
	for _, ok := range []Backend{"", BackendGopass, BackendGnomeKeyring, "/opt/custom/backend --flag"} {
		var b Backend
		require.NoError(t, b.Set(string(ok)))
		assert.Equal(t, ok, b)
	}
	var b Backend
	assert.ErrorContains(t, b.Set("keepassxc"), `unknown backend "keepassxc" (use a path, or one of: gopass, gnome-keyring)`)
}

func TestBackendExec(t *testing.T) {
	mockLookPath(t, defaultLookPath)

	tests := []struct {
		name    string
		value   Backend
		want    string
		wantErr string
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
			name: "command line passes through verbatim", value: "/opt/custom/backend --flag",
			want: "/opt/custom/backend --flag",
		},
		{
			name: "unknown keyword errors", value: "keepassxc",
			wantErr: `unknown backend "keepassxc" (use a path, or one of: gopass, gnome-keyring)`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.value.exec()
			if tc.wantErr != "" {
				require.EqualError(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
