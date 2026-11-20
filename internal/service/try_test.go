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
	mockExePath(t, "/usr/bin/gnome-keyring-daemon")

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

	l, err := locateLayout()
	require.NoError(t, err)
	mockLookPath(t, defaultLookPath)
	mockUnits(t)
	mockOwnerByInstallState(t, l.unitDir)

	// A base config whose settings the trial must inherit without modifying it.
	base, err := config.DefaultSource()
	require.NoError(t, err)
	baseConfig := base.File.String()
	require.NoError(t, base.File.CreateDir())
	baseContent := "listen: 127.0.0.1:9999\n"
	require.NoError(t, os.WriteFile(baseConfig, []byte(baseContent), 0644))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Try(ctx, TryOptions{}) }()

	// Wait for the trial to be fully installed.
	runtimeDir, err := config.LookupRuntimeDir()
	require.NoError(t, err)
	trialConfig := runtimeDir.TrialConfig().String()
	require.Eventually(t, l.unitDir.UnitFile(proxyServiceUnit).Exists, 10*time.Second, 10*time.Millisecond, "trial never installed the proxy unit")

	// The trial config inherits the base settings; the base is untouched.
	trialData, err := os.ReadFile(trialConfig)
	require.NoError(t, err)
	assert.Contains(t, string(trialData), "127.0.0.1:9999")

	// Ctrl-C.
	cancel()
	require.NoError(t, <-done)

	// Everything reverted: units, mask, demotion, trial config.
	assert.Empty(t, l.installed())
	l.files.assertAbsent(t)
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

	mockLookPath(t, defaultLookPath)
	mockUnits(t)
	mockSession(t, 4242)
	mockExePath(t, "/usr/bin/gnome-keyring-daemon")
	// Install fails after masking D-Bus activation, at the first write into the
	// unit directory: a plain file stands where that directory goes.
	unitsDir := filepath.Join(tmpDir, "systemd", "user")
	require.NoError(t, os.MkdirAll(filepath.Dir(unitsDir), 0755))
	require.NoError(t, os.WriteFile(unitsDir, nil, 0644))

	err := Try(t.Context(), TryOptions{})
	require.ErrorContains(t, err, "systemd/user")

	// The partial takeover state was rolled back.
	l, err := locateLayout()
	require.NoError(t, err)
	l.files.assertAbsent(t)
	runtimeDir, err := config.LookupRuntimeDir()
	require.NoError(t, err)
	assert.NoFileExists(t, runtimeDir.TrialConfig().String())
}
