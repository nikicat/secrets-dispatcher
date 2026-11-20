package service

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/nikicat/secrets-dispatcher/internal/systemd"
	"github.com/nikicat/secrets-dispatcher/internal/testutil"
	"gopkg.in/yaml.v3"
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
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockUnits(t)

	if err := Install(t.Context(), Options{Mode: ModeRemote}); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	dir := systemd.UserUnitDir(tmpDir)

	// Proxy unit should exist.
	content, err := dir.UnitFile(proxyServiceUnit).Read()
	if err != nil {
		t.Fatalf("read proxy unit: %v", err)
	}
	if !strings.Contains(content, "ExecStart=") || !strings.Contains(content, "serve --config") {
		t.Error("proxy unit missing ExecStart with serve --config")
	}
	if !strings.Contains(content, "WantedBy=graphical-session.target") {
		t.Error("proxy unit missing WantedBy=graphical-session.target")
	}
	// Remote mode: no Requires on backend.
	if strings.Contains(content, "Requires=") {
		t.Error("remote proxy unit should not have Requires=")
	}

	// Private-bus units should NOT exist.
	for _, name := range []systemd.Unit{busSocketUnit, busServiceUnit, backendServiceUnit} {
		if dir.UnitFile(name).Exists() {
			t.Errorf("stale unit %s should not exist in remote mode", name)
		}
	}
}

func TestInstallRemoteConfig(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockUnits(t)

	if err := Install(t.Context(), Options{Mode: ModeRemote}); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	configPath := filepath.Join(tmpDir, "secrets-dispatcher", "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var cfg config.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}

	assert.Equal(t, config.UpstreamConfig{Bus: config.SessionBus{}}, cfg.Serve.Upstream)
	assert.Equal(t, []config.BusConfig{{Bus: config.Sockets{Dir: "/run/user/1000/secrets-dispatcher/sockets"}}}, cfg.Serve.Downstream)
}

func TestInstallRemoteUnitCalls(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	units := mockUnits(t)

	if err := Install(t.Context(), Options{Mode: ModeRemote}); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	expected := []unitCall{
		{opReload, ""},
		{opEnable, proxyServiceUnit},
	}
	assert.Equal(t, expected, units.calls)
}

func TestInstallRemoteWithStart(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	units := mockUnits(t)

	if err := Install(t.Context(), Options{Mode: ModeRemote, Start: true}); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	expected := []unitCall{
		{opReload, ""},
		{opEnable, proxyServiceUnit},
		{opStart, proxyServiceUnit},
	}
	assert.Equal(t, expected, units.calls)
}

func TestInstallCustomConfigPath(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockUnits(t)

	customConfig := filepath.Join(tmpDir, "custom", "config.yaml")
	if err := Install(t.Context(), Options{Mode: ModeRemote, ConfigPath: config.Path(customConfig)}); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	// Config should be written to custom path.
	if _, err := os.Stat(customConfig); err != nil {
		t.Fatalf("custom config not written: %v", err)
	}

	// ExecStart should reference the custom path.
	unit := systemd.UserUnitDir(tmpDir).UnitFile(proxyServiceUnit)
	content, _ := unit.Read()
	if !strings.Contains(content, "--config "+customConfig) {
		t.Error("ExecStart should reference custom config path")
	}
}

// --- local mode ---

func TestInstallLocalWritesAllUnits(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockUnits(t)
	mockLookPath(t, defaultLookPath)
	mockSession(t, 0)

	if err := Install(t.Context(), Options{Mode: ModeLocal}); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	dir := systemd.UserUnitDir(tmpDir)
	for _, name := range allUnitNames {
		if !dir.UnitFile(name).Exists() {
			t.Errorf("unit file %s not found", name)
		}
	}

	// Socket unit.
	content, _ := dir.UnitFile(busSocketUnit).Read()
	if !strings.Contains(content, "ListenStream=%t/secrets-dispatcher/backend-bus.sock") {
		t.Errorf("socket unit wrong ListenStream, got:\n%s", content)
	}
	if !strings.Contains(content, "WantedBy=sockets.target") {
		t.Error("socket unit missing WantedBy=sockets.target")
	}

	// Bus service.
	content, _ = dir.UnitFile(busServiceUnit).Read()
	if !strings.Contains(content, "/usr/bin/dbus-daemon --session --nofork --nopidfile --address=systemd:") {
		t.Error("bus service wrong ExecStart")
	}

	// Backend service.
	content, _ = dir.UnitFile(backendServiceUnit).Read()
	if !strings.Contains(content, "ExecStart=/usr/bin/gopass-secret service --bus-address") {
		t.Error("backend service wrong ExecStart")
	}
	if !strings.Contains(content, "DBUS_SESSION_BUS_ADDRESS=unix:path=%t/secrets-dispatcher/backend-bus.sock") {
		t.Error("backend service missing DBUS_SESSION_BUS_ADDRESS")
	}
	if !strings.Contains(content, "Requires=secrets-dispatcher-bus.socket") {
		t.Error("backend service missing Requires")
	}

	// Proxy service (local variant).
	content, _ = dir.UnitFile(proxyServiceUnit).Read()
	if !strings.Contains(content, "serve --config") {
		t.Error("proxy service missing 'serve --config'")
	}
	if !strings.Contains(content, "Requires=secrets-dispatcher-backend.service") {
		t.Error("proxy service missing Requires=backend")
	}
}

func TestInstallLocalConfig(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockUnits(t)
	mockLookPath(t, defaultLookPath)
	mockSession(t, 0)

	if err := Install(t.Context(), Options{Mode: ModeLocal}); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	configPath := filepath.Join(tmpDir, "secrets-dispatcher", "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var cfg config.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}

	assert.Equal(t, config.UpstreamConfig{Bus: config.Socket{Path: "/run/user/1000/secrets-dispatcher/backend-bus.sock"}}, cfg.Serve.Upstream)
	assert.Equal(t, []config.BusConfig{{Bus: config.SessionBus{}}}, cfg.Serve.Downstream)
}

func TestInstallLocalUnitCalls(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	units := mockUnits(t)
	mockLookPath(t, defaultLookPath)
	mockSession(t, 0)

	if err := Install(t.Context(), Options{Mode: ModeLocal}); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	expected := []unitCall{
		{opReload, ""},
		{opEnable, busSocketUnit},
		{opEnable, proxyServiceUnit},
	}
	assert.Equal(t, expected, units.calls)
}

func TestInstallLocalWithStart(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	units := mockUnits(t)
	mockLookPath(t, defaultLookPath)
	mockSession(t, 0)
	shortOwnerWait(t)

	if err := Install(t.Context(), Options{Mode: ModeLocal, Start: true}); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

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
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockUnits(t)
	mockLookPath(t, defaultLookPath)
	mockSession(t, 0)

	if err := Install(t.Context(), Options{Mode: ModeFull}); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	configPath := filepath.Join(tmpDir, "secrets-dispatcher", "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	var cfg config.Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal config: %v", err)
	}

	assert.Equal(t, config.UpstreamConfig{Bus: config.Socket{Path: "/run/user/1000/secrets-dispatcher/backend-bus.sock"}}, cfg.Serve.Upstream)
	assert.Equal(t, []config.BusConfig{
		{Bus: config.SessionBus{}},
		{Bus: config.Sockets{Dir: "/run/user/1000/secrets-dispatcher/sockets"}},
	}, cfg.Serve.Downstream)
}

func TestInstallFullWritesAllUnits(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockUnits(t)
	mockLookPath(t, defaultLookPath)
	mockSession(t, 0)

	if err := Install(t.Context(), Options{Mode: ModeFull}); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	dir := systemd.UserUnitDir(tmpDir)
	for _, name := range allUnitNames {
		if !dir.UnitFile(name).Exists() {
			t.Errorf("unit file %s not found", name)
		}
	}
}

// --- config preservation ---

func TestInstallPreservesExistingConfig(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockUnits(t)
	mockLookPath(t, defaultLookPath)
	mockSession(t, 0)

	// Pre-write a config with custom settings.
	configDir := filepath.Join(tmpDir, "secrets-dispatcher")
	os.MkdirAll(configDir, 0755)
	configPath := filepath.Join(configDir, "config.yaml")
	initial := config.Config{
		Listen:   "0.0.0.0:9999",
		StateDir: "/custom/state",
		Serve: config.ServeConfig{
			LogLevel: "debug",
		},
	}
	data, _ := yaml.Marshal(initial)
	os.WriteFile(configPath, data, 0644)

	// Install in local mode.
	if err := Install(t.Context(), Options{Mode: ModeLocal}); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	// Re-read config.
	data, _ = os.ReadFile(configPath)
	var cfg config.Config
	yaml.Unmarshal(data, &cfg)

	// Topology should be updated.
	assert.IsType(t, config.Socket{}, cfg.Serve.Upstream.Bus, "upstream")

	// Other settings should be preserved.
	if cfg.Listen != "0.0.0.0:9999" {
		t.Errorf("listen = %q, want preserved 0.0.0.0:9999", cfg.Listen)
	}
	if cfg.StateDir != "/custom/state" {
		t.Errorf("state_dir = %q, want preserved /custom/state", cfg.StateDir)
	}
	if cfg.Serve.LogLevel != "debug" {
		t.Errorf("log_level = %q, want preserved debug", cfg.Serve.LogLevel)
	}
}

// --- mode switching ---

func TestInstallSwitchLocalToRemoteRemovesStaleUnits(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	units := mockUnits(t)

	// Pre-create all 4 unit files (simulating previous local install).
	dir := systemd.UserUnitDir(tmpDir)
	for _, name := range allUnitNames {
		dir.UnitFile(name).Write("previous")
	}

	// Switch to remote.
	if err := Install(t.Context(), Options{Mode: ModeRemote}); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	// Only proxy unit should remain.
	if !dir.UnitFile(proxyServiceUnit).Exists() {
		t.Error("proxy unit should exist")
	}
	for _, name := range []systemd.Unit{busSocketUnit, busServiceUnit, backendServiceUnit} {
		if dir.UnitFile(name).Exists() {
			t.Errorf("stale unit %s should have been removed", name)
		}
	}

	// Verify stop+disable were called for stale units.
	for _, name := range []systemd.Unit{busSocketUnit, busServiceUnit, backendServiceUnit} {
		if !slices.Contains(units.calls, unitCall{opStop, name}) {
			t.Errorf("missing stop call for %s", name)
		}
		if !slices.Contains(units.calls, unitCall{opDisable, name}) {
			t.Errorf("missing disable call for %s", name)
		}
	}
}

// --- backend resolution ---

func TestInstallLocalAutoDetectsBackend(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

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

	if err := Install(t.Context(), Options{Mode: ModeLocal}); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	dir := systemd.UserUnitDir(tmpDir)
	content, _ := dir.UnitFile(backendServiceUnit).Read()
	if !strings.Contains(content, "ExecStart=/opt/bin/gopass-secret service") {
		t.Error("should use auto-detected backend path")
	}
}

func TestInstallLocalBackendCommand(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockUnits(t)
	mockLookPath(t, defaultLookPath)
	mockSession(t, 0)

	if err := Install(t.Context(), Options{Mode: ModeLocal, Backend: &Backend{backendCommand("/custom/backend")}}); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	dir := systemd.UserUnitDir(tmpDir)
	content, _ := dir.UnitFile(backendServiceUnit).Read()
	if !strings.Contains(content, "ExecStart=/custom/backend") {
		t.Error("should use explicit backend path")
	}
}

// --- error cases ---

func TestInstallBackendNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockUnits(t)
	mockSession(t, 0)
	mockLookPath(t, func(name string) (string, error) {
		if name == "dbus-daemon" {
			return "/usr/bin/dbus-daemon", nil
		}
		return "", fmt.Errorf("not found: %s", name)
	})

	err := Install(t.Context(), Options{Mode: ModeLocal})
	if err == nil {
		t.Fatal("expected error when backend not found")
	}
	if !strings.Contains(err.Error(), "gopass-secret") {
		t.Errorf("error should mention gopass-secret, got: %v", err)
	}
}

func TestInstallDbusDaemonNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockUnits(t)
	mockSession(t, 0)
	mockLookPath(t, func(name string) (string, error) {
		return "", fmt.Errorf("not found: %s", name)
	})

	err := Install(t.Context(), Options{Mode: ModeLocal})
	if err == nil {
		t.Fatal("expected error when dbus-daemon not found")
	}
	if !strings.Contains(err.Error(), "dbus-daemon") {
		t.Errorf("error should mention dbus-daemon, got: %v", err)
	}
}

func TestInstallMissingRuntimeDir(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "")

	mockUnits(t)

	err := Install(t.Context(), Options{Mode: ModeRemote})
	require.ErrorIs(t, err, config.ErrNoRuntimeDir)
}

func TestInstallRejectsModeNotNamed(t *testing.T) {
	for _, mode := range []Mode{"", "bogus"} {
		t.Run(string(mode), func(t *testing.T) {
			tmpDir := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", tmpDir)
			t.Setenv("XDG_DATA_HOME", tmpDir)
			t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
			units := mockUnits(t)

			unitDir := systemd.UserUnitDir(tmpDir)
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
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
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
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)

	// Create a single unit file (remote mode).
	dir := systemd.UserUnitDir(tmpDir)
	unit := dir.UnitFile(proxyServiceUnit)
	unit.Write("fake")

	units := mockUnits(t)

	if err := Uninstall(t.Context(), NarrateMilestones); err != nil {
		t.Fatalf("Uninstall() error: %v", err)
	}

	if unit.Exists() {
		t.Error("unit file should have been removed")
	}

	var expected []unitCall
	for _, name := range allUnitNames {
		expected = append(expected, unitCall{opStop, name}, unitCall{opDisable, name})
	}
	expected = append(expected, unitCall{opReload, ""})
	assert.Equal(t, expected, units.calls)
}

func TestUninstallAllUnits(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)

	dir := systemd.UserUnitDir(tmpDir)
	for _, name := range allUnitNames {
		dir.UnitFile(name).Write("fake")
	}

	mockUnits(t)

	if err := Uninstall(t.Context(), NarrateMilestones); err != nil {
		t.Fatalf("Uninstall() error: %v", err)
	}

	for _, name := range allUnitNames {
		if dir.UnitFile(name).Exists() {
			t.Errorf("unit file %s should have been removed", name)
		}
	}
}

// --- D-Bus activation masking ---

func TestInstallLocalMasksDBusActivation(t *testing.T) {
	mask := tempTakeoverFiles(t).mask
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

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
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

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
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

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
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	mockUnits(t)

	out := captureStdout(t, func() {
		require.NoError(t, Install(t.Context(), Options{Mode: ModeRemote}))
	})

	assert.NotContains(t, out, "Wrote unit file:")
	assert.NotContains(t, out, "Wrote config:")
	assert.NotContains(t, out, "Enabled ")
	assert.NotContains(t, out, "Created symlink")
}

func TestInstallVerboseNarratesChanges(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	units := mockUnits(t)

	out := captureStdout(t, func() {
		require.NoError(t, Install(t.Context(), Options{Mode: ModeRemote, Narration: NarrateChanges}))
	})

	assert.Contains(t, out, "Wrote unit file:")
	assert.Contains(t, out, "Wrote config:")
	assert.Contains(t, out, "Enabled "+string(proxyServiceUnit))
	assert.Contains(t, out, "Created symlink /wants/"+string(proxyServiceUnit)+" → /units/"+string(proxyServiceUnit)+".\n")
	assert.True(t, units.closed)
}

func TestUninstallQuietByDefault(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
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
