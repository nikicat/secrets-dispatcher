package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shortOwnerWait shrinks the ownership-confirmation timeout so tests whose
// fakes never produce the awaited owner don't stall for the full 5s.
func shortOwnerWait(t *testing.T) {
	t.Helper()
	orig := ownerWaitTimeout
	ownerWaitTimeout = 10 * time.Millisecond
	t.Cleanup(func() { ownerWaitTimeout = orig })
}

// mockOwnerByInstallState wires DetectProvider to the realistic sequence: the
// owner is gnome-keyring until the proxy unit file exists, secrets-dispatcher
// while it does, and gnome-keyring again after uninstall. This keeps every
// waitForOwner poll in Try/Install on its fast path.
func mockOwnerByInstallState(t *testing.T, unitsDir string) {
	t.Helper()
	mockBusctlOwner(t, 4242)
	mockReadlink(t, func(string) (string, error) {
		if _, err := os.Stat(filepath.Join(unitsDir, unitFileName)); err == nil {
			return "/usr/bin/secrets-dispatcher", nil
		}
		return "/usr/bin/gnome-keyring-daemon", nil
	})
}

func TestTryRefusesWhenInstalled(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", tmpDir)

	unitsDir := filepath.Join(tmpDir, "systemd", "user")
	require.NoError(t, os.MkdirAll(unitsDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(unitsDir, unitFileName), []byte("x"), 0644))

	err := Try(context.Background(), TryOptions{})
	require.ErrorContains(t, err, "already installed")
}

func TestTryDryRunWritesNothing(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", tmpDir)

	mockLookPath(t, defaultLookPath)
	mockBusctlOwner(t, 4242)
	mockReadlink(t, func(string) (string, error) {
		return "/usr/bin/gnome-keyring-daemon", nil
	})

	require.NoError(t, Try(context.Background(), TryOptions{DryRun: true}))

	entries, err := os.ReadDir(tmpDir)
	require.NoError(t, err)
	assert.Empty(t, entries, "dry run created files")
}

func TestTryInstallsThenRestoresEverything(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", tmpDir)

	unitsDir := filepath.Join(tmpDir, "systemd", "user")
	mockLookPath(t, defaultLookPath)
	mockSystemctl(t)
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
	require.Eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(unitsDir, unitFileName))
		return err == nil
	}, 10*time.Second, 10*time.Millisecond, "trial never installed the proxy unit")

	// The trial config inherits the base settings; the base is untouched.
	trialData, err := os.ReadFile(trialConfig)
	require.NoError(t, err)
	assert.Contains(t, string(trialData), "127.0.0.1:9999")

	// Ctrl-C.
	cancel()
	require.NoError(t, <-done)

	// Everything reverted: units, mask, demotion, trial config.
	for _, name := range allUnitNames {
		assert.NoFileExists(t, filepath.Join(unitsDir, name))
	}
	assert.NoFileExists(t, filepath.Join(tmpDir, "dbus-1", "services", dbusServiceFile))
	assert.NoFileExists(t, filepath.Join(unitsDir, gkDropInDirName, gkDropInName))
	assert.NoFileExists(t, filepath.Join(tmpDir, "autostart", gkAutostartName))
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

	mockSystemctl(t)
	mockBusctlOwner(t, 4242)
	mockReadlink(t, func(string) (string, error) {
		return "/usr/bin/gnome-keyring-daemon", nil
	})
	// Install fails at unit building (after mask + demotion already applied):
	// gnome-keyring-daemon resolves, dbus-daemon does not.
	mockLookPath(t, func(name string) (string, error) {
		if name == "dbus-daemon" {
			return "", os.ErrNotExist
		}
		return "/usr/bin/" + name, nil
	})

	err := Try(context.Background(), TryOptions{})
	require.ErrorContains(t, err, "dbus-daemon")

	// The partial takeover state was rolled back.
	unitsDir := filepath.Join(tmpDir, "systemd", "user")
	assert.NoFileExists(t, filepath.Join(tmpDir, "dbus-1", "services", dbusServiceFile))
	assert.NoFileExists(t, filepath.Join(unitsDir, gkDropInDirName, gkDropInName))
	assert.NoFileExists(t, filepath.Join(tmpDir, "autostart", gkAutostartName))
	assert.NoFileExists(t, filepath.Join(tmpDir, "secrets-dispatcher", "try-config.yaml"))
}
