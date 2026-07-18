package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// changeIndex returns the position of the first change matching op and a
// target substring, or -1.
func changeIndex(changes []Change, op, targetSubstr string) int {
	for i, c := range changes {
		if c.Op == op && strings.Contains(c.Target, targetSubstr) {
			return i
		}
	}
	return -1
}

func TestPlanLocalGnomeKeyringMirrorsInstallOrder(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockLookPath(t, defaultLookPath)
	mockBusctlOwner(t, 4242)
	mockReadlink(t, func(string) (string, error) {
		return "/usr/bin/gnome-keyring-daemon", nil
	})

	changes, err := Plan(Options{Mode: "local", Start: true})
	require.NoError(t, err)

	config := changeIndex(changes, "write", "config.yaml")
	mask := changeIndex(changes, "write", dbusServiceFile)
	dropIn := changeIndex(changes, "write", gkDropInName)
	shadow := changeIndex(changes, "write", gkAutostartName)
	sigterm := changeIndex(changes, "signal", "pid 4242")
	proxyUnit := changeIndex(changes, "write", unitFileName)
	start := changeIndex(changes, "run", "start "+unitFileName)

	for name, idx := range map[string]int{
		"config": config, "mask": mask, "drop-in": dropIn, "shadow": shadow,
		"sigterm": sigterm, "proxy unit": proxyUnit, "start": start,
	} {
		require.GreaterOrEqual(t, idx, 0, "missing %s change in plan: %v", name, changes)
	}

	// Execution order must mirror Install: config, then mask, then demotion,
	// then the SIGTERM to the displaced owner, then units, then start.
	assert.Less(t, config, mask)
	assert.Less(t, mask, dropIn)
	assert.Less(t, dropIn, shadow)
	assert.Less(t, shadow, sigterm)
	assert.Less(t, sigterm, proxyUnit)
	assert.Less(t, proxyUnit, start)

	// A plan must not touch the filesystem.
	entries, err := os.ReadDir(tmpDir)
	require.NoError(t, err)
	assert.Empty(t, entries, "Plan created files")
}

func TestPlanSurfacesResolvedBackend(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockLookPath(t, defaultLookPath)
	mockBusctlOwner(t, 4242)
	mockReadlink(t, func(string) (string, error) {
		return "/usr/bin/gnome-keyring-daemon", nil
	})

	changes, err := Plan(Options{Mode: "local"})
	require.NoError(t, err)

	idx := changeIndex(changes, "write", "secrets-dispatcher-backend.service")
	require.GreaterOrEqual(t, idx, 0)
	assert.Contains(t, changes[idx].Note, "gnome-keyring-daemon --foreground --components=secrets")
}

func TestPlanRemoteMinimal(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockExecOutput(t, noopExecOutput)

	changes, err := Plan(Options{})
	require.NoError(t, err)

	assert.GreaterOrEqual(t, changeIndex(changes, "write", unitFileName), 0)
	assert.Equal(t, -1, changeIndex(changes, "write", dbusServiceFile), "remote mode must not mask")
	assert.Equal(t, -1, changeIndex(changes, "signal", ""), "remote mode must not signal anyone")
	for _, c := range changes {
		assert.NotContains(t, c.Target, "bus.socket", "remote mode must not install the private bus")
	}
}

func TestPlanRemoteReversesTakeoverState(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockExecOutput(t, noopExecOutput)

	// Simulate a previous takeover: our mask, drop-in, and autostart shadow.
	dbusDir := filepath.Join(tmpDir, "dbus-1", "services")
	require.NoError(t, os.MkdirAll(dbusDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dbusDir, dbusServiceFile), []byte(dbusActivationMask), 0644))
	dropInDir := filepath.Join(tmpDir, "systemd", "user", gkDropInDirName)
	require.NoError(t, os.MkdirAll(dropInDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(dropInDir, gkDropInName), []byte("x"), 0644))
	asDir := filepath.Join(tmpDir, "autostart")
	require.NoError(t, os.MkdirAll(asDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(asDir, gkAutostartName), []byte(gkAutostartShadow), 0644))

	changes, err := Plan(Options{Mode: "remote"})
	require.NoError(t, err)

	assert.GreaterOrEqual(t, changeIndex(changes, "remove", dbusServiceFile), 0)
	assert.GreaterOrEqual(t, changeIndex(changes, "remove", gkDropInName), 0)
	assert.GreaterOrEqual(t, changeIndex(changes, "remove", gkAutostartName), 0)
	assert.GreaterOrEqual(t, changeIndex(changes, "run", "try-restart "+gkServiceUnit), 0)

	// Reading state must not modify it.
	_, err = os.Stat(filepath.Join(dbusDir, dbusServiceFile))
	assert.NoError(t, err)
}

func TestPlanMirrorsInstall(t *testing.T) {
	// The contract Plan promises: for the same options and starting state,
	// every file Plan says it writes/removes is exactly what Install then
	// writes/removes.
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockLookPath(t, defaultLookPath)
	mockBusctlOwner(t, 4242)
	mockReadlink(t, func(string) (string, error) {
		return "/usr/bin/gnome-keyring-daemon", nil
	})
	mockSystemctl(t)
	shortOwnerWait(t)

	opts := Options{Mode: "local", Start: true}
	changes, err := Plan(opts)
	require.NoError(t, err)

	require.NoError(t, Install(opts))

	for _, c := range changes {
		if c.Op == "write" {
			assert.FileExists(t, c.Target, "planned write did not happen: %s", c)
		}
	}
}
