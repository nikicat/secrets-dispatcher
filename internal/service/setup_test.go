package service

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/nikicat/secrets-dispatcher/internal/systemd"
)

// A runtime dir that no test creates.
const fakeRuntimeDir = "/run/user/1000"

// The units only local and full modes install.
var privateBusUnits = []systemd.Unit{busSocketUnit, busServiceUnit, backendServiceUnit}

// Points the XDG config and data homes at a new temp dir, and XDG_RUNTIME_DIR
// at fakeRuntimeDir. Returns the temp dir.
func tempXDG(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	setXDG(t, dir, fakeRuntimeDir)
	return dir
}

// Like tempXDG, but with XDG_RUNTIME_DIR at the temp dir too.
func tempXDGWithRuntime(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	setXDG(t, dir, dir)
	return dir
}

// Like tempXDG, but with XDG_RUNTIME_DIR empty.
func tempXDGWithoutRuntime(t *testing.T) {
	t.Helper()
	setXDG(t, t.TempDir(), "")
}

func setXDG(t *testing.T, home, runtimeDir string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", home)
	t.Setenv("XDG_DATA_HOME", home)
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
}

// Returns the config file at its default location under the XDG config home.
func defaultConfigFile(t *testing.T) config.File {
	t.Helper()
	source, err := config.DefaultSource()
	require.NoError(t, err)
	return source.File
}

// Returns the config in the default config file, which must exist.
func installedConfig(t *testing.T) *config.Config {
	t.Helper()
	cfg, err := defaultConfigFile(t).Load()
	require.NoError(t, err)
	return cfg
}
