package config

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/testutil"
)

func TestLookupStateDir(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/home/u/.state")
	dir, err := LookupStateDir()
	require.NoError(t, err)
	assert.Equal(t, "/home/u/.state/secrets-dispatcher/.cookie", dir.Cookie())
}

// Returns a state directory whose cookie file holds content.
func stateDirWithCookie(t *testing.T, content string) StateDir {
	t.Helper()
	dir := StateDir(t.TempDir())
	require.NoError(t, os.WriteFile(dir.Cookie(), []byte(content), 0o600))
	return dir
}

func TestReadToken(t *testing.T) {
	token, err := stateDirWithCookie(t, "my-secret-token\n").ReadToken()
	require.NoError(t, err)
	assert.Equal(t, AuthToken("my-secret-token"), token)
}

func TestReadTokenMissing(t *testing.T) {
	_, err := StateDir(t.TempDir()).ReadToken()
	assert.ErrorIs(t, err, fs.ErrNotExist)
}

func TestReadTokenEmpty(t *testing.T) {
	dir := stateDirWithCookie(t, "\n")
	_, err := dir.ReadToken()
	require.Error(t, err)
	require.NotErrorIs(t, err, fs.ErrNotExist)
	assert.ErrorContains(t, err, dir.Cookie())
}

func TestWriteTokenRoundTrip(t *testing.T) {
	dir := StateDir(filepath.Join(t.TempDir(), "nested", "state"))
	require.NoError(t, dir.WriteToken("my-secret-token"))
	token, err := dir.ReadToken()
	require.NoError(t, err)
	assert.Equal(t, AuthToken("my-secret-token"), token)
	testutil.AssertPerm(t, string(dir), 0o700)
	testutil.AssertPerm(t, dir.Cookie(), 0o600)
}

func TestStateDirOrDefault(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/home/u/.state")
	for _, tc := range []struct{ dir, want StateDir }{
		{"/srv/state", "/srv/state"},
		{"", "/home/u/.state/secrets-dispatcher"},
	} {
		got, err := tc.dir.OrDefault()
		require.NoError(t, err, tc.dir)
		assert.Equal(t, tc.want, got, tc.dir)
	}
}

func TestLoadStateDir(t *testing.T) {
	for _, tc := range []struct {
		name, yaml string
		want       StateDir
		wantErr    string
	}{
		{name: "absolute", yaml: "state_dir: /var/lib/sd\n", want: "/var/lib/sd"},
		{name: "empty", yaml: "state_dir: \"\"\n", want: ""},
		{name: "relative", yaml: "state_dir: state\n", wantErr: `state directory must be an absolute path: "state"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := yamlFile(t, tc.yaml).Load()
			if tc.wantErr != "" {
				assert.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, cfg.StateDir)
		})
	}
}
