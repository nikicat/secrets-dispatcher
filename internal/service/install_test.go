package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nikicat/secrets-dispatcher/internal/config"
	"gopkg.in/yaml.v3"
)

// --- test helpers ---

func mockLookPath(t *testing.T, fn func(string) (string, error)) {
	t.Helper()
	orig := lookPathFunc
	lookPathFunc = fn
	t.Cleanup(func() { lookPathFunc = orig })
}

func mockExecOutput(t *testing.T, fn func(string, ...string) ([]byte, error)) {
	t.Helper()
	orig := execOutputFunc
	execOutputFunc = fn
	t.Cleanup(func() { execOutputFunc = orig })
}

func noopExecOutput(string, ...string) ([]byte, error) {
	return nil, nil
}

func mockSystemctl(t *testing.T) *[]string {
	t.Helper()
	orig := systemctlFunc
	var calls []string
	systemctlFunc = func(args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		return nil
	}
	t.Cleanup(func() { systemctlFunc = orig })
	return &calls
}

func defaultLookPath(name string) (string, error) {
	return "/usr/bin/" + name, nil
}

// --- remote mode (default) ---

func TestInstallRemoteWritesProxyUnit(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockSystemctl(t)

	if err := Install(Options{}); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	dir := filepath.Join(tmpDir, "systemd", "user")

	// Proxy unit should exist.
	content, err := os.ReadFile(filepath.Join(dir, unitFileName))
	if err != nil {
		t.Fatalf("read proxy unit: %v", err)
	}
	s := string(content)
	if !strings.Contains(s, "ExecStart=") || !strings.Contains(s, "serve --config") {
		t.Error("proxy unit missing ExecStart with serve --config")
	}
	if !strings.Contains(s, "WantedBy=default.target") {
		t.Error("proxy unit missing WantedBy=default.target")
	}
	// Remote mode: no Requires on backend.
	if strings.Contains(s, "Requires=") {
		t.Error("remote proxy unit should not have Requires=")
	}

	// Private-bus units should NOT exist.
	for _, name := range []string{"secrets-dispatcher-bus.socket", "secrets-dispatcher-bus.service", "secrets-dispatcher-backend.service"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("stale unit %s should not exist in remote mode", name)
		}
	}
}

func TestInstallRemoteConfig(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockSystemctl(t)

	if err := Install(Options{}); err != nil {
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

	if cfg.Serve.Upstream.Type != "session_bus" {
		t.Errorf("upstream type = %q, want session_bus", cfg.Serve.Upstream.Type)
	}
	if len(cfg.Serve.Downstream) != 1 || cfg.Serve.Downstream[0].Type != "sockets" {
		t.Errorf("downstream = %+v, want [{sockets ...}]", cfg.Serve.Downstream)
	}
	wantPath := "/run/user/1000/secrets-dispatcher/sockets"
	if cfg.Serve.Downstream[0].Path != wantPath {
		t.Errorf("downstream path = %q, want %q", cfg.Serve.Downstream[0].Path, wantPath)
	}
}

func TestInstallRemoteSystemctlCalls(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	calls := mockSystemctl(t)

	if err := Install(Options{}); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	expected := []string{
		"daemon-reload",
		"enable " + unitFileName,
	}
	if len(*calls) != len(expected) {
		t.Fatalf("expected %d systemctl calls, got %d: %v", len(expected), len(*calls), *calls)
	}
	for i, want := range expected {
		if (*calls)[i] != want {
			t.Errorf("call %d: got %q, want %q", i, (*calls)[i], want)
		}
	}
}

func TestInstallRemoteWithStart(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	calls := mockSystemctl(t)

	if err := Install(Options{Start: true}); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	expected := []string{
		"daemon-reload",
		"enable " + unitFileName,
		"start " + unitFileName,
	}
	if len(*calls) != len(expected) {
		t.Fatalf("expected %d systemctl calls, got %d: %v", len(expected), len(*calls), *calls)
	}
	for i, want := range expected {
		if (*calls)[i] != want {
			t.Errorf("call %d: got %q, want %q", i, (*calls)[i], want)
		}
	}
}

func TestInstallCustomConfigPath(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockSystemctl(t)

	customConfig := filepath.Join(tmpDir, "custom", "config.yaml")
	if err := Install(Options{ConfigPath: customConfig}); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	// Config should be written to custom path.
	if _, err := os.Stat(customConfig); err != nil {
		t.Fatalf("custom config not written: %v", err)
	}

	// ExecStart should reference the custom path.
	unitPath := filepath.Join(tmpDir, "systemd", "user", unitFileName)
	content, _ := os.ReadFile(unitPath)
	if !strings.Contains(string(content), "--config "+customConfig) {
		t.Error("ExecStart should reference custom config path")
	}
}

// --- local mode ---

func TestInstallLocalWritesAllUnits(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockSystemctl(t)
	mockLookPath(t, defaultLookPath)
	mockExecOutput(t, noopExecOutput)

	if err := Install(Options{Mode: "local"}); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	dir := filepath.Join(tmpDir, "systemd", "user")
	for _, name := range allUnitNames {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("unit file %s not found: %v", name, err)
		}
	}

	// Socket unit.
	content, _ := os.ReadFile(filepath.Join(dir, "secrets-dispatcher-bus.socket"))
	s := string(content)
	if !strings.Contains(s, "ListenStream=%t/secrets-dispatcher/backend-bus.sock") {
		t.Errorf("socket unit wrong ListenStream, got:\n%s", s)
	}
	if !strings.Contains(s, "WantedBy=sockets.target") {
		t.Error("socket unit missing WantedBy=sockets.target")
	}

	// Bus service.
	content, _ = os.ReadFile(filepath.Join(dir, "secrets-dispatcher-bus.service"))
	if !strings.Contains(string(content), "/usr/bin/dbus-daemon --session --nofork --nopidfile --address=systemd:") {
		t.Error("bus service wrong ExecStart")
	}

	// Backend service.
	content, _ = os.ReadFile(filepath.Join(dir, "secrets-dispatcher-backend.service"))
	s = string(content)
	if !strings.Contains(s, "ExecStart=/usr/bin/gopass-secret-service") {
		t.Error("backend service wrong ExecStart")
	}
	if !strings.Contains(s, "DBUS_SESSION_BUS_ADDRESS=unix:path=%t/secrets-dispatcher/backend-bus.sock") {
		t.Error("backend service missing DBUS_SESSION_BUS_ADDRESS")
	}
	if !strings.Contains(s, "Requires=secrets-dispatcher-bus.socket") {
		t.Error("backend service missing Requires")
	}

	// Proxy service (local variant).
	content, _ = os.ReadFile(filepath.Join(dir, unitFileName))
	s = string(content)
	if !strings.Contains(s, "serve --config") {
		t.Error("proxy service missing 'serve --config'")
	}
	if !strings.Contains(s, "Requires=secrets-dispatcher-backend.service") {
		t.Error("proxy service missing Requires=backend")
	}
}

func TestInstallLocalConfig(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockSystemctl(t)
	mockLookPath(t, defaultLookPath)
	mockExecOutput(t, noopExecOutput)

	if err := Install(Options{Mode: "local"}); err != nil {
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

	if cfg.Serve.Upstream.Type != "socket" {
		t.Errorf("upstream type = %q, want socket", cfg.Serve.Upstream.Type)
	}
	wantPath := "/run/user/1000/secrets-dispatcher/backend-bus.sock"
	if cfg.Serve.Upstream.Path != wantPath {
		t.Errorf("upstream path = %q, want %q", cfg.Serve.Upstream.Path, wantPath)
	}
	if len(cfg.Serve.Downstream) != 1 || cfg.Serve.Downstream[0].Type != "session_bus" {
		t.Errorf("downstream = %+v, want [{session_bus}]", cfg.Serve.Downstream)
	}
}

func TestInstallLocalSystemctlCalls(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	calls := mockSystemctl(t)
	mockLookPath(t, defaultLookPath)
	mockExecOutput(t, noopExecOutput)

	if err := Install(Options{Mode: "local"}); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	expected := []string{
		"daemon-reload",
		"enable secrets-dispatcher-bus.socket",
		"enable " + unitFileName,
	}
	if len(*calls) != len(expected) {
		t.Fatalf("expected %d systemctl calls, got %d: %v", len(expected), len(*calls), *calls)
	}
	for i, want := range expected {
		if (*calls)[i] != want {
			t.Errorf("call %d: got %q, want %q", i, (*calls)[i], want)
		}
	}
}

func TestInstallLocalWithStart(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	calls := mockSystemctl(t)
	mockLookPath(t, defaultLookPath)
	mockExecOutput(t, noopExecOutput)

	if err := Install(Options{Mode: "local", Start: true}); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	expected := []string{
		"daemon-reload",
		"enable secrets-dispatcher-bus.socket",
		"enable " + unitFileName,
		"start secrets-dispatcher-bus.socket",
		"start " + unitFileName,
	}
	if len(*calls) != len(expected) {
		t.Fatalf("expected %d systemctl calls, got %d: %v", len(expected), len(*calls), *calls)
	}
	for i, want := range expected {
		if (*calls)[i] != want {
			t.Errorf("call %d: got %q, want %q", i, (*calls)[i], want)
		}
	}
}

// --- full mode ---

func TestInstallFullConfig(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockSystemctl(t)
	mockLookPath(t, defaultLookPath)
	mockExecOutput(t, noopExecOutput)

	if err := Install(Options{Mode: "full"}); err != nil {
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

	if cfg.Serve.Upstream.Type != "socket" {
		t.Errorf("upstream type = %q, want socket", cfg.Serve.Upstream.Type)
	}
	if len(cfg.Serve.Downstream) != 2 {
		t.Fatalf("downstream count = %d, want 2", len(cfg.Serve.Downstream))
	}
	if cfg.Serve.Downstream[0].Type != "session_bus" {
		t.Errorf("downstream[0] type = %q, want session_bus", cfg.Serve.Downstream[0].Type)
	}
	if cfg.Serve.Downstream[1].Type != "sockets" {
		t.Errorf("downstream[1] type = %q, want sockets", cfg.Serve.Downstream[1].Type)
	}
	wantSockets := "/run/user/1000/secrets-dispatcher/sockets"
	if cfg.Serve.Downstream[1].Path != wantSockets {
		t.Errorf("downstream[1] path = %q, want %q", cfg.Serve.Downstream[1].Path, wantSockets)
	}
}

func TestInstallFullWritesAllUnits(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockSystemctl(t)
	mockLookPath(t, defaultLookPath)
	mockExecOutput(t, noopExecOutput)

	if err := Install(Options{Mode: "full"}); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	dir := filepath.Join(tmpDir, "systemd", "user")
	for _, name := range allUnitNames {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("unit file %s not found: %v", name, err)
		}
	}
}

// --- config preservation ---

func TestInstallPreservesExistingConfig(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockSystemctl(t)
	mockLookPath(t, defaultLookPath)
	mockExecOutput(t, noopExecOutput)

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
	if err := Install(Options{Mode: "local"}); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	// Re-read config.
	data, _ = os.ReadFile(configPath)
	var cfg config.Config
	yaml.Unmarshal(data, &cfg)

	// Topology should be updated.
	if cfg.Serve.Upstream.Type != "socket" {
		t.Errorf("upstream should be socket, got %q", cfg.Serve.Upstream.Type)
	}

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

	calls := mockSystemctl(t)

	// Pre-create all 4 unit files (simulating previous local install).
	dir := filepath.Join(tmpDir, "systemd", "user")
	os.MkdirAll(dir, 0755)
	for _, name := range allUnitNames {
		os.WriteFile(filepath.Join(dir, name), []byte("previous"), 0644)
	}

	// Switch to remote.
	if err := Install(Options{Mode: "remote"}); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	// Only proxy unit should remain.
	if _, err := os.Stat(filepath.Join(dir, unitFileName)); err != nil {
		t.Error("proxy unit should exist")
	}
	for _, name := range []string{"secrets-dispatcher-bus.socket", "secrets-dispatcher-bus.service", "secrets-dispatcher-backend.service"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("stale unit %s should have been removed", name)
		}
	}

	// Verify stop+disable were called for stale units.
	callStr := strings.Join(*calls, "\n")
	for _, name := range []string{"secrets-dispatcher-bus.socket", "secrets-dispatcher-bus.service", "secrets-dispatcher-backend.service"} {
		if !strings.Contains(callStr, "stop "+name) {
			t.Errorf("missing stop call for %s", name)
		}
		if !strings.Contains(callStr, "disable "+name) {
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

	mockSystemctl(t)
	mockExecOutput(t, noopExecOutput)
	mockLookPath(t, func(name string) (string, error) {
		switch name {
		case "dbus-daemon":
			return "/usr/bin/dbus-daemon", nil
		case "gopass-secret-service":
			return "/opt/bin/gopass-secret-service", nil
		default:
			return "", fmt.Errorf("not found: %s", name)
		}
	})

	if err := Install(Options{Mode: "local"}); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	dir := filepath.Join(tmpDir, "systemd", "user")
	content, _ := os.ReadFile(filepath.Join(dir, "secrets-dispatcher-backend.service"))
	if !strings.Contains(string(content), "ExecStart=/opt/bin/gopass-secret-service") {
		t.Error("should use auto-detected backend path")
	}
}

func TestInstallLocalExplicitBackendPath(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockSystemctl(t)
	mockLookPath(t, defaultLookPath)
	mockExecOutput(t, noopExecOutput)

	if err := Install(Options{Mode: "local", BackendPath: "/custom/backend"}); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	dir := filepath.Join(tmpDir, "systemd", "user")
	content, _ := os.ReadFile(filepath.Join(dir, "secrets-dispatcher-backend.service"))
	if !strings.Contains(string(content), "ExecStart=/custom/backend") {
		t.Error("should use explicit backend path")
	}
}

// --- error cases ---

func TestInstallBackendNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockSystemctl(t)
	mockExecOutput(t, noopExecOutput)
	mockLookPath(t, func(name string) (string, error) {
		if name == "dbus-daemon" {
			return "/usr/bin/dbus-daemon", nil
		}
		return "", fmt.Errorf("not found: %s", name)
	})

	err := Install(Options{Mode: "local"})
	if err == nil {
		t.Fatal("expected error when backend not found")
	}
	if !strings.Contains(err.Error(), "gopass-secret-service") {
		t.Errorf("error should mention gopass-secret-service, got: %v", err)
	}
}

func TestInstallDbusDaemonNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockSystemctl(t)
	mockExecOutput(t, noopExecOutput)
	mockLookPath(t, func(name string) (string, error) {
		return "", fmt.Errorf("not found: %s", name)
	})

	err := Install(Options{Mode: "local"})
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

	mockSystemctl(t)

	err := Install(Options{})
	if err == nil {
		t.Fatal("expected error when XDG_RUNTIME_DIR is empty")
	}
	if !strings.Contains(err.Error(), "XDG_RUNTIME_DIR") {
		t.Errorf("error should mention XDG_RUNTIME_DIR, got: %v", err)
	}
}

func TestInstallUnknownMode(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockSystemctl(t)

	err := Install(Options{Mode: "bogus"})
	if err == nil {
		t.Fatal("expected error for unknown mode")
	}
	if !strings.Contains(err.Error(), "unknown mode") {
		t.Errorf("error should say unknown mode, got: %v", err)
	}
}

// --- uninstall ---

func TestUninstall(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)

	// Create a single unit file (remote mode).
	dir := filepath.Join(tmpDir, "systemd", "user")
	os.MkdirAll(dir, 0755)
	unitPath := filepath.Join(dir, unitFileName)
	os.WriteFile(unitPath, []byte("fake"), 0644)

	calls := mockSystemctl(t)

	if err := Uninstall(); err != nil {
		t.Fatalf("Uninstall() error: %v", err)
	}

	if _, err := os.Stat(unitPath); !os.IsNotExist(err) {
		t.Error("unit file should have been removed")
	}

	var expected []string
	for _, name := range allUnitNames {
		expected = append(expected, "stop "+name, "disable "+name)
	}
	expected = append(expected, "daemon-reload")
	if len(*calls) != len(expected) {
		t.Fatalf("expected %d systemctl calls, got %d: %v", len(expected), len(*calls), *calls)
	}
	for i, want := range expected {
		if (*calls)[i] != want {
			t.Errorf("call %d: got %q, want %q", i, (*calls)[i], want)
		}
	}
}

func TestUninstallAllUnits(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)

	dir := filepath.Join(tmpDir, "systemd", "user")
	os.MkdirAll(dir, 0755)
	for _, name := range allUnitNames {
		os.WriteFile(filepath.Join(dir, name), []byte("fake"), 0644)
	}

	mockSystemctl(t)

	if err := Uninstall(); err != nil {
		t.Fatalf("Uninstall() error: %v", err)
	}

	for _, name := range allUnitNames {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("unit file %s should have been removed", name)
		}
	}
}

// --- UnitPath ---

func TestUnitPath(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)

	p, err := UnitPath()
	if err != nil {
		t.Fatalf("UnitPath() error: %v", err)
	}

	want := filepath.Join(tmpDir, "systemd", "user", unitFileName)
	if p != want {
		t.Errorf("UnitPath() = %q, want %q", p, want)
	}
}

// --- D-Bus activation masking ---

func TestInstallLocalMasksDBusActivation(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockSystemctl(t)
	mockLookPath(t, defaultLookPath)
	mockExecOutput(t, noopExecOutput)

	// Pre-create an existing D-Bus activation file.
	dbusDir := filepath.Join(tmpDir, "dbus-1", "services")
	os.MkdirAll(dbusDir, 0755)
	origContent := "[D-BUS Service]\nName=org.freedesktop.secrets\nExec=/usr/bin/gopass-secret-service\n"
	os.WriteFile(filepath.Join(dbusDir, dbusServiceFile), []byte(origContent), 0644)

	if err := Install(Options{Mode: "local"}); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	// Mask file should contain our mask.
	content, err := os.ReadFile(filepath.Join(dbusDir, dbusServiceFile))
	if err != nil {
		t.Fatalf("read mask: %v", err)
	}
	if string(content) != dbusActivationMask {
		t.Errorf("mask content = %q, want %q", string(content), dbusActivationMask)
	}

	// Backup should exist with original content.
	backup, err := os.ReadFile(filepath.Join(dbusDir, dbusServiceFile+dbusBackupSuffix))
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(backup) != origContent {
		t.Errorf("backup content = %q, want %q", string(backup), origContent)
	}
}

func TestInstallLocalMaskIdempotent(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockSystemctl(t)
	mockLookPath(t, defaultLookPath)
	mockExecOutput(t, noopExecOutput)

	// Pre-create mask + backup (simulating previous install).
	dbusDir := filepath.Join(tmpDir, "dbus-1", "services")
	os.MkdirAll(dbusDir, 0755)
	os.WriteFile(filepath.Join(dbusDir, dbusServiceFile), []byte(dbusActivationMask), 0644)
	origContent := "[D-BUS Service]\nName=org.freedesktop.secrets\nExec=/usr/bin/gopass\n"
	os.WriteFile(filepath.Join(dbusDir, dbusServiceFile+dbusBackupSuffix), []byte(origContent), 0644)

	if err := Install(Options{Mode: "local"}); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	// Mask should still be in place.
	content, _ := os.ReadFile(filepath.Join(dbusDir, dbusServiceFile))
	if string(content) != dbusActivationMask {
		t.Errorf("mask should remain, got %q", string(content))
	}

	// Backup should NOT have been overwritten with the mask content.
	backup, _ := os.ReadFile(filepath.Join(dbusDir, dbusServiceFile + dbusBackupSuffix))
	if string(backup) != origContent {
		t.Errorf("backup should be preserved, got %q", string(backup))
	}
}

func TestInstallRemoteUnmasks(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	mockSystemctl(t)

	// Pre-create mask + backup.
	dbusDir := filepath.Join(tmpDir, "dbus-1", "services")
	os.MkdirAll(dbusDir, 0755)
	os.WriteFile(filepath.Join(dbusDir, dbusServiceFile), []byte(dbusActivationMask), 0644)
	origContent := "[D-BUS Service]\nName=org.freedesktop.secrets\nExec=/usr/bin/gopass\n"
	os.WriteFile(filepath.Join(dbusDir, dbusServiceFile+dbusBackupSuffix), []byte(origContent), 0644)

	var reloadCalled bool
	mockExecOutput(t, func(name string, args ...string) ([]byte, error) {
		if name == "busctl" {
			reloadCalled = true
		}
		return nil, nil
	})

	if err := Install(Options{Mode: "remote"}); err != nil {
		t.Fatalf("Install() error: %v", err)
	}

	// Original file should be restored.
	content, err := os.ReadFile(filepath.Join(dbusDir, dbusServiceFile))
	if err != nil {
		t.Fatalf("read restored file: %v", err)
	}
	if string(content) != origContent {
		t.Errorf("restored content = %q, want %q", string(content), origContent)
	}

	// Backup should be gone.
	if _, err := os.Stat(filepath.Join(dbusDir, dbusServiceFile + dbusBackupSuffix)); !os.IsNotExist(err) {
		t.Error("backup should have been removed after restore")
	}

	if !reloadCalled {
		t.Error("D-Bus reload should have been called")
	}
}

func TestUninstallUnmasks(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)

	mockSystemctl(t)
	mockExecOutput(t, noopExecOutput)

	// Pre-create mask + backup.
	dbusDir := filepath.Join(tmpDir, "dbus-1", "services")
	os.MkdirAll(dbusDir, 0755)
	os.WriteFile(filepath.Join(dbusDir, dbusServiceFile), []byte(dbusActivationMask), 0644)
	origContent := "[D-BUS Service]\nName=org.freedesktop.secrets\nExec=/usr/bin/gopass\n"
	os.WriteFile(filepath.Join(dbusDir, dbusServiceFile+dbusBackupSuffix), []byte(origContent), 0644)

	if err := Uninstall(); err != nil {
		t.Fatalf("Uninstall() error: %v", err)
	}

	// Original should be restored.
	content, err := os.ReadFile(filepath.Join(dbusDir, dbusServiceFile))
	if err != nil {
		t.Fatalf("read restored file: %v", err)
	}
	if string(content) != origContent {
		t.Errorf("restored content = %q, want %q", string(content), origContent)
	}

	// Backup should be gone.
	if _, err := os.Stat(filepath.Join(dbusDir, dbusServiceFile + dbusBackupSuffix)); !os.IsNotExist(err) {
		t.Error("backup should have been removed after restore")
	}
}

func TestUnmaskNoBackupRemovesMask(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)
	t.Setenv("XDG_DATA_HOME", tmpDir)

	mockSystemctl(t)
	mockExecOutput(t, noopExecOutput)

	// Pre-create mask WITHOUT backup.
	dbusDir := filepath.Join(tmpDir, "dbus-1", "services")
	os.MkdirAll(dbusDir, 0755)
	os.WriteFile(filepath.Join(dbusDir, dbusServiceFile), []byte(dbusActivationMask), 0644)

	if err := Uninstall(); err != nil {
		t.Fatalf("Uninstall() error: %v", err)
	}

	// Mask file should be removed entirely.
	if _, err := os.Stat(filepath.Join(dbusDir, dbusServiceFile)); !os.IsNotExist(err) {
		t.Error("mask file should have been removed when no backup exists")
	}
}
