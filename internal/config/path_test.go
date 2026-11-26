package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/testutil"
)

func TestDefaultSource(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/custom/config")
	got, err := DefaultSource()
	require.NoError(t, err)
	assert.Equal(t, File{path: "/custom/config/secrets-dispatcher/config.yaml"}, got.File)
}

func TestPathOrDefault(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	def, err := DefaultSource()
	require.NoError(t, err)
	for _, tc := range []struct {
		in   Path
		want Source
	}{
		{"/etc/custom.yaml", Source{File: File{path: "/etc/custom.yaml"}}},
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
	cfg, err := File{path: filepath.Join(t.TempDir(), "missing.yaml")}.LoadOrEmpty()
	require.NoError(t, err)
	assert.Equal(t, &Config{}, cfg)
}

func TestFileSave(t *testing.T) {
	p := File{path: filepath.Join(t.TempDir(), "sub", "config.yaml")}
	require.NoError(t, p.Save(&Config{StateDir: "/srv/state"}))

	testutil.AssertPerm(t, p.String(), 0644)
	cfg, err := p.Load()
	require.NoError(t, err)
	assert.Equal(t, StateDir("/srv/state"), cfg.StateDir)
}

func TestFileSavePrivate(t *testing.T) {
	p := File{path: filepath.Join(t.TempDir(), "sub", "config.yaml"), private: true}
	require.NoError(t, p.Save(&Config{}))

	testutil.AssertPerm(t, p.String(), 0600)
	testutil.AssertPerm(t, filepath.Dir(p.String()), 0700)
}

func TestPathDraftMissingFile(t *testing.T) {
	draft, err := Path(filepath.Join(t.TempDir(), "missing.yaml")).Draft()
	require.NoError(t, err)
	assert.Equal(t, &Config{}, draft.Config)
}

// A draft saves to the file it was loaded from, and a draft moved with To saves
// to its new file only.
func TestDraftSave(t *testing.T) {
	const listen = "listen: 127.0.0.1:9999\n"
	for _, tc := range []struct {
		name string
		move func(Draft) Draft
	}{
		{"own file", func(d Draft) Draft { return d }},
		{"To", func(d Draft) Draft {
			return d.To(File{path: filepath.Join(filepath.Dir(d.File().String()), "trial.yaml")})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := yamlFile(t, listen)
			draft, err := Path(src.String()).Draft()
			require.NoError(t, err)
			assert.Equal(t, src, draft.File())

			saved := tc.move(draft)
			saved.StateDir = "/srv/state"
			require.NoError(t, saved.Save())
			cfg, err := saved.File().Load()
			require.NoError(t, err)
			assert.Equal(t, ListenAddr("127.0.0.1:9999"), cfg.Listen)
			assert.Equal(t, StateDir("/srv/state"), cfg.StateDir)
			if saved.File() != src {
				data, err := src.ReadFile()
				require.NoError(t, err)
				assert.Equal(t, listen, string(data))
			}
		})
	}
}

func TestFileRemoveMissingFile(t *testing.T) {
	assert.NoError(t, File{path: filepath.Join(t.TempDir(), "missing.yaml")}.Remove())
}
