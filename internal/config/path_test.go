package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultSource(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/custom/config")
	got, err := DefaultSource()
	require.NoError(t, err)
	assert.Equal(t, File{"/custom/config/secrets-dispatcher/config.yaml"}, got.File)
}

func TestPathOrDefault(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	def, err := DefaultSource()
	require.NoError(t, err)
	for _, tc := range []struct {
		in   Path
		want Source
	}{
		{"/etc/custom.yaml", Source{File: File{"/etc/custom.yaml"}}},
		{"", def},
	} {
		got, err := tc.in.OrDefault()
		require.NoError(t, err)
		assert.Equal(t, tc.want, got, "Path(%q).OrDefault()", tc.in)
	}
}

func TestSourceLoadDefaultMissingFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	source, err := Path("").OrDefault()
	require.NoError(t, err)
	cfg, err := source.Load()
	require.NoError(t, err)
	assert.Equal(t, &Config{}, cfg)
}

func TestSourceLoadExplicitMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.yaml")
	source, err := Path(path).OrDefault()
	require.NoError(t, err)
	_, err = source.Load()
	assert.ErrorIs(t, err, os.ErrNotExist)
	assert.ErrorContains(t, err, path)
}

func TestFileLoadOrEmptyMissingFile(t *testing.T) {
	cfg, err := File{filepath.Join(t.TempDir(), "missing.yaml")}.LoadOrEmpty()
	require.NoError(t, err)
	assert.Equal(t, &Config{}, cfg)
}

func TestFileSave(t *testing.T) {
	p := File{filepath.Join(t.TempDir(), "sub", "config.yaml")}
	require.NoError(t, p.Save(&Config{StateDir: "/srv/state"}))

	info, err := os.Stat(p.String())
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0644), info.Mode().Perm())
	cfg, err := p.Load()
	require.NoError(t, err)
	assert.Equal(t, StateDir("/srv/state"), cfg.StateDir)
}

func TestFileCopyTo(t *testing.T) {
	dir := t.TempDir()
	src := File{filepath.Join(dir, "config.yaml")}
	require.NoError(t, os.WriteFile(src.String(), []byte("listen: 127.0.0.1:9999\n"), 0644))
	dst := File{filepath.Join(dir, "trial", "config.yaml")}
	require.NoError(t, src.CopyTo(dst))

	data, err := dst.ReadFile()
	require.NoError(t, err)
	assert.Equal(t, "listen: 127.0.0.1:9999\n", string(data))
	info, err := os.Stat(dst.String())
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
}

func TestFileCopyToMissingFile(t *testing.T) {
	dir := t.TempDir()
	err := File{filepath.Join(dir, "missing.yaml")}.CopyTo(File{filepath.Join(dir, "copy.yaml")})
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestFileRemoveMissingFile(t *testing.T) {
	assert.NoError(t, File{filepath.Join(t.TempDir(), "missing.yaml")}.Remove())
}
