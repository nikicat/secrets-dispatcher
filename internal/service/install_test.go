package service

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/nikicat/secrets-dispatcher/internal/testutil"
)

// --- test helpers ---

func mockLookPath(t *testing.T, fn func(string) (string, error)) {
	t.Helper()
	testutil.Swap(t, &lookPathFunc, fn)
}

func defaultLookPath(name string) (string, error) {
	return "/usr/bin/" + name, nil
}

// --- remote mode ---

func TestInstallRemoteWritesProxyUnit(t *testing.T) {
	dir := tempLayout(t).unitDir

	mockUnits(t)

	require.NoError(t, Install(t.Context(), Options{Mode: ModeRemote}))

	// Proxy unit should exist.
	content, err := dir.UnitFile(proxyServiceUnit).Read()
	require.NoError(t, err, "read proxy unit")
	assert.Contains(t, content, "ExecStart=")
	assert.Contains(t, content, "serve --config")
	assert.Contains(t, content, "WantedBy=graphical-session.target", "proxy unit missing WantedBy=graphical-session.target")
	// Remote mode: no Requires on backend.
	assert.NotContains(t, content, "Requires=", "remote proxy unit should not have Requires=")

	// Private-bus units should NOT exist.
	for _, name := range privateBusUnits {
		assert.False(t, dir.UnitFile(name).Exists(), "stale unit %s should not exist in remote mode", name)
	}
}

func TestInstallRemoteConfig(t *testing.T) {
	tempXDG(t)

	mockUnits(t)

	require.NoError(t, Install(t.Context(), Options{Mode: ModeRemote}))

	cfg := installedConfig(t)

	assert.Equal(t, config.UpstreamConfig{Bus: config.SessionBus{}}, cfg.Serve.Upstream)
	assert.Equal(t, []config.BusConfig{{Bus: config.Sockets{Dir: fakeRuntimeDir + "/secrets-dispatcher/sockets"}}}, cfg.Serve.Downstream)
}

func TestInstallRemoteUnitCalls(t *testing.T) {
	tempXDG(t)

	units := mockUnits(t)

	require.NoError(t, Install(t.Context(), Options{Mode: ModeRemote}))

	expected := []unitCall{
		{opReload, ""},
		{opEnable, proxyServiceUnit},
	}
	assert.Equal(t, expected, units.calls)
}

func TestInstallRemoteWithStart(t *testing.T) {
	tempXDG(t)

	units := mockUnits(t)

	require.NoError(t, Install(t.Context(), Options{Mode: ModeRemote, Start: true}))

	expected := []unitCall{
		{opReload, ""},
		{opEnable, proxyServiceUnit},
		{opStart, proxyServiceUnit},
	}
	assert.Equal(t, expected, units.calls)
}

func TestInstallCustomConfigPath(t *testing.T) {
	dir := tempLayout(t).unitDir

	mockUnits(t)

	customConfig := filepath.Join(t.TempDir(), "custom", "config.yaml")
	require.NoError(t, Install(t.Context(), Options{Mode: ModeRemote, ConfigPath: config.Path(customConfig)}))

	// Config should be written to custom path.
	require.FileExists(t, customConfig, "custom config not written")

	// ExecStart should reference the custom path.
	unit := dir.UnitFile(proxyServiceUnit)
	content, _ := unit.Read()
	assert.Contains(t, content, "--config "+customConfig, "ExecStart should reference custom config path")
}

// --- local mode ---

func TestInstallLocalWritesAllUnits(t *testing.T) {
	dir := tempLayout(t).unitDir

	mockUnits(t)
	mockLookPath(t, defaultLookPath)
	mockSession(t, 0)

	require.NoError(t, Install(t.Context(), Options{Mode: ModeLocal}))

	for _, name := range allUnitNames {
		assert.True(t, dir.UnitFile(name).Exists(), "unit file %s not found", name)
	}

	// Socket unit.
	content, _ := dir.UnitFile(busSocketUnit).Read()
	assert.Contains(t, content, "ListenStream=%t/secrets-dispatcher/backend-bus.sock", "socket unit wrong ListenStream")
	assert.Contains(t, content, "WantedBy=sockets.target", "socket unit missing WantedBy=sockets.target")

	// Bus service.
	content, _ = dir.UnitFile(busServiceUnit).Read()
	assert.Contains(t, content, "/usr/bin/dbus-daemon --session --nofork --nopidfile --address=systemd:", "bus service wrong ExecStart")

	// Backend service.
	content, _ = dir.UnitFile(backendServiceUnit).Read()
	assert.Contains(t, content, "ExecStart=/usr/bin/gopass-secret service --bus-address", "backend service wrong ExecStart")
	assert.Contains(t, content, "DBUS_SESSION_BUS_ADDRESS=unix:path=%t/secrets-dispatcher/backend-bus.sock", "backend service missing DBUS_SESSION_BUS_ADDRESS")
	assert.Contains(t, content, "Requires=secrets-dispatcher-bus.socket", "backend service missing Requires")

	// Proxy service (local variant).
	content, _ = dir.UnitFile(proxyServiceUnit).Read()
	assert.Contains(t, content, "serve --config", "proxy service missing 'serve --config'")
	assert.Contains(t, content, "Requires=secrets-dispatcher-backend.service", "proxy service missing Requires=backend")
}

func TestInstallLocalConfig(t *testing.T) {
	tempXDG(t)

	mockUnits(t)
	mockLookPath(t, defaultLookPath)
	mockSession(t, 0)

	require.NoError(t, Install(t.Context(), Options{Mode: ModeLocal}))

	cfg := installedConfig(t)

	assert.Equal(t, config.UpstreamConfig{Bus: config.Socket{Path: fakeRuntimeDir + "/secrets-dispatcher/backend-bus.sock"}}, cfg.Serve.Upstream)
	assert.Equal(t, []config.BusConfig{{Bus: config.SessionBus{}}}, cfg.Serve.Downstream)
}

func TestInstallLocalUnitCalls(t *testing.T) {
	tempXDG(t)

	units := mockUnits(t)
	mockLookPath(t, defaultLookPath)
	mockSession(t, 0)

	require.NoError(t, Install(t.Context(), Options{Mode: ModeLocal}))

	expected := []unitCall{
		{opReload, ""},
		{opEnable, busSocketUnit},
		{opEnable, proxyServiceUnit},
	}
	assert.Equal(t, expected, units.calls)
}

func TestInstallLocalWithStart(t *testing.T) {
	tempXDG(t)

	units := mockUnits(t)
	mockLookPath(t, defaultLookPath)
	mockSession(t, 0)
	shortOwnerWait(t)

	require.NoError(t, Install(t.Context(), Options{Mode: ModeLocal, Start: true}))

	expected := []unitCall{
		{opReload, ""},
		{opEnable, busSocketUnit},
		{opEnable, proxyServiceUnit},
		{opStart, busSocketUnit},
		{opStart, proxyServiceUnit},
	}
	assert.Equal(t, expected, units.calls)
}

// --- full mode ---

func TestInstallFullConfig(t *testing.T) {
	tempXDG(t)

	mockUnits(t)
	mockLookPath(t, defaultLookPath)
	mockSession(t, 0)

	require.NoError(t, Install(t.Context(), Options{Mode: ModeFull}))

	cfg := installedConfig(t)

	assert.Equal(t, config.UpstreamConfig{Bus: config.Socket{Path: fakeRuntimeDir + "/secrets-dispatcher/backend-bus.sock"}}, cfg.Serve.Upstream)
	assert.Equal(t, []config.BusConfig{
		{Bus: config.SessionBus{}},
		{Bus: config.Sockets{Dir: fakeRuntimeDir + "/secrets-dispatcher/sockets"}},
	}, cfg.Serve.Downstream)
}

func TestInstallFullWritesAllUnits(t *testing.T) {
	dir := tempLayout(t).unitDir

	mockUnits(t)
	mockLookPath(t, defaultLookPath)
	mockSession(t, 0)

	require.NoError(t, Install(t.Context(), Options{Mode: ModeFull}))

	for _, name := range allUnitNames {
		assert.True(t, dir.UnitFile(name).Exists(), "unit file %s not found", name)
	}
}

// --- config preservation ---

func TestInstallPreservesExistingConfig(t *testing.T) {
	tempXDG(t)

	mockUnits(t)
	mockLookPath(t, defaultLookPath)
	mockSession(t, 0)

	// Pre-write a config with custom settings.
	initial := config.Config{
		Listen:   "0.0.0.0:9999",
		StateDir: "/custom/state",
		Serve: config.ServeConfig{
			LogLevel: "debug",
		},
	}
	require.NoError(t, defaultConfigFile(t).Save(&initial))

	// Install in local mode.
	require.NoError(t, Install(t.Context(), Options{Mode: ModeLocal}))

	cfg := installedConfig(t)

	// Topology should be updated.
	assert.IsType(t, config.Socket{}, cfg.Serve.Upstream.Bus, "upstream")

	// Other settings should be preserved.
	assert.Equal(t, config.ListenAddr("0.0.0.0:9999"), cfg.Listen)
	assert.Equal(t, config.StateDir("/custom/state"), cfg.StateDir)
	assert.Equal(t, config.LogLevel("debug"), cfg.Serve.LogLevel)
}

// --- mode switching ---

func TestInstallSwitchLocalToRemoteRemovesStaleUnits(t *testing.T) {
	dir := tempLayout(t).unitDir

	units := mockUnits(t)

	// Pre-create all 4 unit files (simulating previous local install).
	for _, name := range allUnitNames {
		dir.UnitFile(name).Write("previous")
	}

	// Switch to remote.
	require.NoError(t, Install(t.Context(), Options{Mode: ModeRemote}))

	// Only proxy unit should remain.
	assert.True(t, dir.UnitFile(proxyServiceUnit).Exists(), "proxy unit should exist")
	for _, name := range privateBusUnits {
		assert.False(t, dir.UnitFile(name).Exists(), "stale unit %s should have been removed", name)
	}

	// Verify stop+disable were called for stale units.
	for _, name := range privateBusUnits {
		assert.Contains(t, units.calls, unitCall{opStop, name}, "missing stop call for %s", name)
		assert.Contains(t, units.calls, unitCall{opDisable, name}, "missing disable call for %s", name)
	}
}

// --- backend resolution ---

func TestInstallLocalAutoDetectsBackend(t *testing.T) {
	dir := tempLayout(t).unitDir

	mockUnits(t)
	mockSession(t, 0)
	mockLookPath(t, func(name string) (string, error) {
		switch name {
		case "dbus-daemon":
			return "/usr/bin/dbus-daemon", nil
		case "gopass-secret":
			return "/opt/bin/gopass-secret", nil
		default:
			return "", fmt.Errorf("not found: %s", name)
		}
	})

	require.NoError(t, Install(t.Context(), Options{Mode: ModeLocal}))

	content, _ := dir.UnitFile(backendServiceUnit).Read()
	assert.Contains(t, content, "ExecStart=/opt/bin/gopass-secret service", "should use auto-detected backend path")
}

func TestInstallLocalBackendCommand(t *testing.T) {
	dir := tempLayout(t).unitDir

	mockUnits(t)
	mockLookPath(t, defaultLookPath)
	mockSession(t, 0)

	require.NoError(t, Install(t.Context(), Options{Mode: ModeLocal, Backend: &Backend{backendCommand("/custom/backend")}}))

	content, _ := dir.UnitFile(backendServiceUnit).Read()
	assert.Contains(t, content, "ExecStart=/custom/backend", "should use explicit backend path")
}

// --- error cases ---

func TestInstallBackendNotFound(t *testing.T) {
	tempXDG(t)

	mockUnits(t)
	mockSession(t, 0)
	mockLookPath(t, func(name string) (string, error) {
		if name == "dbus-daemon" {
			return "/usr/bin/dbus-daemon", nil
		}
		return "", fmt.Errorf("not found: %s", name)
	})

	err := Install(t.Context(), Options{Mode: ModeLocal})
	require.ErrorContains(t, err, "gopass-secret")
}

func TestInstallDbusDaemonNotFound(t *testing.T) {
	tempXDG(t)

	mockUnits(t)
	mockSession(t, 0)
	mockLookPath(t, func(name string) (string, error) {
		return "", fmt.Errorf("not found: %s", name)
	})

	err := Install(t.Context(), Options{Mode: ModeLocal})
	require.ErrorContains(t, err, "dbus-daemon")
}

func TestInstallMissingRuntimeDir(t *testing.T) {
	tempXDGWithoutRuntime(t)

	mockUnits(t)

	err := Install(t.Context(), Options{Mode: ModeRemote})
	require.ErrorIs(t, err, config.ErrNoRuntimeDir)
}

func TestInstallRejectsModeNotNamed(t *testing.T) {
	for _, mode := range []Mode{"", "bogus"} {
		t.Run(string(mode), func(t *testing.T) {
			unitDir := tempLayout(t).unitDir
			units := mockUnits(t)

			installed := unitDir.UnitFile(proxyServiceUnit)
			require.NoError(t, installed.Write("installed"))

			_, err := Plan(t.Context(), Options{Mode: mode})
			require.ErrorContains(t, err, "mode must be")
			require.ErrorContains(t, Install(t.Context(), Options{Mode: mode}), "mode must be")

			for _, u := range allUnitNames {
				assert.Equal(t, u == proxyServiceUnit, unitDir.UnitFile(u).Exists(), u)
			}
			content, err := installed.Read()
			require.NoError(t, err)
			assert.Equal(t, "installed", content)
			assert.Empty(t, units.calls)
		})
	}
}

func TestInstallRejectsBackendForRemote(t *testing.T) {
	tmpDir := tempXDG(t)
	units := mockUnits(t)
	opts := Options{Mode: ModeRemote, Backend: new(BackendGopass)}

	_, err := Plan(t.Context(), opts)
	require.ErrorContains(t, err, "only to local and full modes")
	require.ErrorContains(t, Install(t.Context(), opts), "only to local and full modes")

	assert.NoDirExists(t, filepath.Join(tmpDir, "systemd"))
	assert.NoDirExists(t, filepath.Join(tmpDir, "secrets-dispatcher"))
	assert.Empty(t, units.calls)
}

// --- uninstall ---

func TestUninstall(t *testing.T) {
	dir := tempLayout(t).unitDir

	// Create a single unit file (remote mode).
	unit := dir.UnitFile(proxyServiceUnit)
	unit.Write("fake")

	units := mockUnits(t)

	require.NoError(t, Uninstall(t.Context(), NarrateMilestones))

	assert.False(t, unit.Exists(), "unit file should have been removed")

	var expected []unitCall
	for _, name := range allUnitNames {
		expected = append(expected, unitCall{opStop, name}, unitCall{opDisable, name})
	}
	expected = append(expected, unitCall{opReload, ""})
	assert.Equal(t, expected, units.calls)
}

func TestUninstallAllUnits(t *testing.T) {
	dir := tempLayout(t).unitDir

	for _, name := range allUnitNames {
		dir.UnitFile(name).Write("fake")
	}

	mockUnits(t)

	require.NoError(t, Uninstall(t.Context(), NarrateMilestones))

	for _, name := range allUnitNames {
		assert.False(t, dir.UnitFile(name).Exists(), "unit file %s should have been removed", name)
	}
}

// --- D-Bus activation masking ---

func TestInstallLocalMasksDBusActivation(t *testing.T) {
	mask := tempTakeoverFiles(t).mask

	mockUnits(t)
	mockLookPath(t, defaultLookPath)
	mockSession(t, 0)

	// Pre-create an existing D-Bus activation file.
	origContent := "[D-BUS Service]\nName=org.freedesktop.secrets\nExec=/usr/bin/gopass-secret-service\n"
	require.NoError(t, mask.file.Write(origContent))

	require.NoError(t, Install(t.Context(), Options{Mode: ModeLocal}))

	assert.Equal(t, dbusActivationMask, readFile(t, mask.file), "the mask is in place")
	assert.Equal(t, origContent, readFile(t, mask.backup()), "the backup keeps the original")
}

func TestInstallLocalMaskIdempotent(t *testing.T) {
	mask := tempTakeoverFiles(t).mask

	mockUnits(t)
	mockLookPath(t, defaultLookPath)
	mockSession(t, 0)

	// Pre-create mask + backup (simulating previous install).
	require.NoError(t, mask.file.Write(dbusActivationMask))
	origContent := "[D-BUS Service]\nName=org.freedesktop.secrets\nExec=/usr/bin/gopass\n"
	require.NoError(t, mask.backup().Write(origContent))

	require.NoError(t, Install(t.Context(), Options{Mode: ModeLocal}))

	assert.Equal(t, dbusActivationMask, readFile(t, mask.file), "the mask stays")
	assert.Equal(t, origContent, readFile(t, mask.backup()), "the backup is not overwritten with the mask")
}

func TestInstallRemoteUnmasks(t *testing.T) {
	mask := tempTakeoverFiles(t).mask

	mockUnits(t)

	// Pre-create mask + backup.
	require.NoError(t, mask.file.Write(dbusActivationMask))
	origContent := "[D-BUS Service]\nName=org.freedesktop.secrets\nExec=/usr/bin/gopass\n"
	require.NoError(t, mask.backup().Write(origContent))

	bus := mockSession(t, 0)

	require.NoError(t, Install(t.Context(), Options{Mode: ModeRemote}))

	assert.Equal(t, origContent, readFile(t, mask.file), "the original is restored")
	assert.False(t, mask.backup().Exists(), "the backup is gone after the restore")
	assert.NotZero(t, bus.reloads, "D-Bus reloads its config")
}

func TestUninstallUnmasks(t *testing.T) {
	mask := tempTakeoverFiles(t).mask

	mockUnits(t)
	mockSession(t, 0)

	// Pre-create mask + backup.
	require.NoError(t, mask.file.Write(dbusActivationMask))
	origContent := "[D-BUS Service]\nName=org.freedesktop.secrets\nExec=/usr/bin/gopass\n"
	require.NoError(t, mask.backup().Write(origContent))

	require.NoError(t, Uninstall(t.Context(), NarrateMilestones))

	assert.Equal(t, origContent, readFile(t, mask.file), "the original is restored")
	assert.False(t, mask.backup().Exists(), "the backup is gone after the restore")
}

func TestUnmaskNoBackupRemovesMask(t *testing.T) {
	mask := tempTakeoverFiles(t).mask

	mockUnits(t)
	mockSession(t, 0)

	// Pre-create mask WITHOUT backup.
	require.NoError(t, mask.file.Write(dbusActivationMask))

	require.NoError(t, Uninstall(t.Context(), NarrateMilestones))

	assert.False(t, mask.file.Exists(), "the mask goes when no backup exists")
}

// --- output verbosity ---

// captureStdout runs fn with os.Stdout redirected to a pipe and returns
// everything it printed.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w
	defer func() { os.Stdout = orig }()
	fn()
	require.NoError(t, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(out)
}

func TestInstallQuietByDefault(t *testing.T) {
	tempXDG(t)
	mockUnits(t)

	out := captureStdout(t, func() {
		require.NoError(t, Install(t.Context(), Options{Mode: ModeRemote}))
	})

	assert.NotContains(t, out, "Wrote "+string(proxyServiceUnit)+": ")
	assert.NotContains(t, out, "Wrote config:")
	assert.NotContains(t, out, "Enabled ")
	assert.NotContains(t, out, "Created symlink")
}

func TestInstallVerboseNarratesChanges(t *testing.T) {
	tempXDG(t)
	units := mockUnits(t)

	out := captureStdout(t, func() {
		require.NoError(t, Install(t.Context(), Options{Mode: ModeRemote, Narration: NarrateChanges}))
	})

	assert.Contains(t, out, "Wrote "+string(proxyServiceUnit)+": ")
	assert.Contains(t, out, "Wrote config:")
	assert.Contains(t, out, "Enabled "+string(proxyServiceUnit))
	assert.Contains(t, out, "Created symlink /wants/"+string(proxyServiceUnit)+" → /units/"+string(proxyServiceUnit)+".\n")
	assert.True(t, units.closed)
}

func TestUninstallQuietByDefault(t *testing.T) {
	tempXDG(t)
	mockUnits(t)
	mockSession(t, 0)
	require.NoError(t, Install(t.Context(), Options{Mode: ModeRemote}))

	out := captureStdout(t, func() {
		require.NoError(t, Uninstall(t.Context(), NarrateMilestones))
	})

	assert.NotContains(t, out, "Removed ")
}

func TestRestartProxy(t *testing.T) {
	units := mockUnits(t)

	require.NoError(t, RestartProxy(t.Context()))

	assert.Equal(t, []unitCall{{opRestart, proxyServiceUnit}}, units.calls)
}
