package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadFullConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(`
state_dir: /tmp/state
listen: 0.0.0.0:9090
serve:
  sockets_dir: /run/socks
  log_level: debug
  log_format: json
  timeout: 10m
  history_limit: 50
  notifications: false
`), 0o644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.StateDir != "/tmp/state" {
		t.Errorf("StateDir = %q, want /tmp/state", cfg.StateDir)
	}
	if cfg.Listen != "0.0.0.0:9090" {
		t.Errorf("Listen = %q, want 0.0.0.0:9090", cfg.Listen)
	}
	if cfg.Serve.SocketsDir != "/run/socks" {
		t.Errorf("SocketsDir = %q, want /run/socks", cfg.Serve.SocketsDir)
	}
	if cfg.Serve.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want debug", cfg.Serve.LogLevel)
	}
	if cfg.Serve.LogFormat != "json" {
		t.Errorf("LogFormat = %q, want json", cfg.Serve.LogFormat)
	}
	if time.Duration(cfg.Serve.Timeout) != 10*time.Minute {
		t.Errorf("Timeout = %v, want 10m", time.Duration(cfg.Serve.Timeout))
	}
	if cfg.Serve.HistoryLimit != 50 {
		t.Errorf("HistoryLimit = %d, want 50", cfg.Serve.HistoryLimit)
	}
	if cfg.Serve.Notifications == nil || *cfg.Serve.Notifications != false {
		t.Errorf("Notifications = %v, want ptr to false", cfg.Serve.Notifications)
	}
}

func TestLoadPartialConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(`
listen: 127.0.0.1:5555
serve:
  log_level: warn
`), 0o644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Listen != "127.0.0.1:5555" {
		t.Errorf("Listen = %q, want 127.0.0.1:5555", cfg.Listen)
	}
	if cfg.Serve.LogLevel != "warn" {
		t.Errorf("LogLevel = %q, want warn", cfg.Serve.LogLevel)
	}
	// Unset fields should be zero values
	if cfg.StateDir != "" {
		t.Errorf("StateDir = %q, want empty", cfg.StateDir)
	}
	if cfg.Serve.Notifications != nil {
		t.Errorf("Notifications = %v, want nil", cfg.Serve.Notifications)
	}
	if cfg.Serve.HistoryLimit != 0 {
		t.Errorf("HistoryLimit = %d, want 0", cfg.Serve.HistoryLimit)
	}
}

func TestLoadMissingFile(t *testing.T) {
	cfg, err := Load("/nonexistent/path/config.yaml")
	if err != nil {
		t.Fatalf("Load: expected nil error for missing file, got %v", err)
	}
	if cfg.StateDir != "" || cfg.Listen != "" {
		t.Errorf("expected empty config, got %+v", cfg)
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(`{{{not yaml`), 0o644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLoadInvalidDuration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(`
serve:
  timeout: not-a-duration
`), 0o644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid duration")
	}
}

func TestNotificationsFalseVsUnset(t *testing.T) {
	dir := t.TempDir()

	// notifications: false (explicitly set)
	pathFalse := filepath.Join(dir, "false.yaml")
	os.WriteFile(pathFalse, []byte(`
serve:
  notifications: false
`), 0o644)

	cfgFalse, err := Load(pathFalse)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfgFalse.Serve.Notifications == nil {
		t.Fatal("notifications: false should produce non-nil pointer")
	}
	if *cfgFalse.Serve.Notifications != false {
		t.Errorf("notifications = %v, want false", *cfgFalse.Serve.Notifications)
	}

	// notifications not set
	pathUnset := filepath.Join(dir, "unset.yaml")
	os.WriteFile(pathUnset, []byte(`
serve:
  log_level: info
`), 0o644)

	cfgUnset, err := Load(pathUnset)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfgUnset.Serve.Notifications != nil {
		t.Errorf("unset notifications should be nil, got %v", *cfgUnset.Serve.Notifications)
	}
}

func TestDefaultPath(t *testing.T) {
	// With XDG_CONFIG_HOME set
	t.Setenv("XDG_CONFIG_HOME", "/custom/config")
	got := DefaultPath()
	want := "/custom/config/secrets-dispatcher/config.yaml"
	if got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}
