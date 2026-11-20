package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/nikicat/secrets-dispatcher/internal/procutil"
	"github.com/nikicat/secrets-dispatcher/internal/systemd"
	"github.com/nikicat/secrets-dispatcher/internal/testutil"
)

// Shrinks the ownership-confirmation timeout to 10ms for the rest of t.
func shortOwnerWait(t *testing.T) {
	t.Helper()
	testutil.Swap(t, &ownerWaitTimeout, 10*time.Millisecond)
}

// Fakes gnome-keyring as the owner of org.freedesktop.secrets while the proxy
// unit file in unitsDir is absent, and secrets-dispatcher while it exists.
func mockOwnerByInstallState(t *testing.T, unitsDir systemd.UnitDir) {
	t.Helper()
	mockSession(t, 4242)
	mockExe(t, func(procutil.PID) (procutil.Path, bool) {
		if unitsDir.UnitFile(proxyServiceUnit).Exists() {
			return "/usr/bin/secrets-dispatcher", true
		}
		return "/usr/bin/gnome-keyring-daemon", true
	})
}

func TestTryRefusesWhenInstalled(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", tmpDir)

	unitsDir := systemd.UserUnitDir(tmpDir)
	require.NoError(t, unitsDir.UnitFile(proxyServiceUnit).Write("x"))

	err := Try(t.Context(), TryOptions{})
	require.ErrorContains(t, err, "already installed")
}

func TestTryMissingRuntimeDir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "")

	err := Try(t.Context(), TryOptions{})
	require.ErrorIs(t, err, config.ErrNoRuntimeDir)
}

func TestTryDryRunWritesNothing(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", tmpDir)

	mockLookPath(t, defaultLookPath)
	mockSession(t, 4242)
	mockExe(t, func(procutil.PID) (procutil.Path, bool) {
		return "/usr/bin/gnome-keyring-daemon", true
	})

	require.NoError(t, Try(t.Context(), TryOptions{DryRun: true}))

	entries, err := os.ReadDir(tmpDir)
	require.NoError(t, err)
	assert.Empty(t, entries, "dry run created files")
}

func TestTryInstallsThenRestoresEverything(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", tmpDir)

	unitsDir := systemd.UserUnitDir(tmpDir)
	files, err := locateTakeoverFiles()
	require.NoError(t, err)
	mockLookPath(t, defaultLookPath)
	mockUnits(t)
	mockOwnerByInstallState(t, unitsDir)

	// A base config whose settings the trial must inherit without modifying it.
	baseDir := filepath.Join(tmpDir, "secrets-dispatcher")
	require.NoError(t, os.MkdirAll(baseDir, 0755))
	baseConfig := filepath.Join(baseDir, "config.yaml")
	baseContent := "listen: 127.0.0.1:9999\n"
	require.NoError(t, os.WriteFile(baseConfig, []byte(baseContent), 0644))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Try(ctx, TryOptions{}) }()

	// Wait for the trial to be fully installed.
	trialConfig := filepath.Join(tmpDir, "secrets-dispatcher", "try-config.yaml")
	require.Eventually(t, unitsDir.UnitFile(proxyServiceUnit).Exists, 10*time.Second, 10*time.Millisecond, "trial never installed the proxy unit")

	// The trial config inherits the base settings; the base is untouched.
	trialData, err := os.ReadFile(trialConfig)
	require.NoError(t, err)
	assert.Contains(t, string(trialData), "127.0.0.1:9999")

	// Ctrl-C.
	cancel()
	require.NoError(t, <-done)

	// Everything reverted: units, mask, demotion, trial config.
	for _, name := range allUnitNames {
		assert.False(t, unitsDir.UnitFile(name).Exists())
	}
	files.assertAbsent(t)
	assert.NoFileExists(t, trialConfig)

	data, err := os.ReadFile(baseConfig)
	require.NoError(t, err)
	assert.Equal(t, baseContent, string(data), "base config was modified")
}

func TestTryRestoresAfterFailedInstall(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", tmpDir)

	mockUnits(t)
	mockSession(t, 4242)
	mockExe(t, func(procutil.PID) (procutil.Path, bool) {
		return "/usr/bin/gnome-keyring-daemon", true
	})
	// Install fails at unit building (after mask + demotion already applied):
	// gnome-keyring-daemon resolves, dbus-daemon does not.
	mockLookPath(t, func(name string) (string, error) {
		if name == "dbus-daemon" {
			return "", os.ErrNotExist
		}
		return "/usr/bin/" + name, nil
	})

	err := Try(t.Context(), TryOptions{})
	require.ErrorContains(t, err, "dbus-daemon")

	// The partial takeover state was rolled back.
	files, err := locateTakeoverFiles()
	require.NoError(t, err)
	files.assertAbsent(t)
	assert.NoFileExists(t, filepath.Join(tmpDir, "secrets-dispatcher", "try-config.yaml"))
}
