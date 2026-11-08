package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

func TestLoadFullConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(`
state_dir: /tmp/state
listen: 0.0.0.0:9090
serve:
  upstream:
    type: socket
    path: /run/upstream.sock
  downstream:
    - type: sockets
      path: /run/socks
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
	if cfg.Serve.Upstream.Type != "socket" {
		t.Errorf("Upstream.Type = %q, want socket", cfg.Serve.Upstream.Type)
	}
	if cfg.Serve.Upstream.Path != "/run/upstream.sock" {
		t.Errorf("Upstream.Path = %q, want /run/upstream.sock", cfg.Serve.Upstream.Path)
	}
	if len(cfg.Serve.Downstream) != 1 {
		t.Fatalf("Downstream len = %d, want 1", len(cfg.Serve.Downstream))
	}
	if cfg.Serve.Downstream[0].Type != "sockets" {
		t.Errorf("Downstream[0].Type = %q, want sockets", cfg.Serve.Downstream[0].Type)
	}
	if cfg.Serve.Downstream[0].Path != "/run/socks" {
		t.Errorf("Downstream[0].Path = %q, want /run/socks", cfg.Serve.Downstream[0].Path)
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
	if cfg.Serve.Upstream.Type != "" {
		t.Errorf("Upstream.Type = %q, want empty", cfg.Serve.Upstream.Type)
	}
	if len(cfg.Serve.Downstream) != 0 {
		t.Errorf("Downstream len = %d, want 0", len(cfg.Serve.Downstream))
	}
}

func TestLoadMissingFile(t *testing.T) {
	path := "/nonexistent/path/config.yaml"
	_, err := Load(path)
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load() error = %v, want os.ErrNotExist", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("Load() error = %v, want it to name %s", err, path)
	}
}

func TestLoadDefaultMissingFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg, err := LoadDefault()
	if err != nil {
		t.Fatalf("LoadDefault() error = %v", err)
	}
	if !reflect.DeepEqual(cfg, &Config{}) {
		t.Errorf("LoadDefault() = %+v, want an empty config", cfg)
	}
}

func TestPathOrDefault(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	want, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath() error = %v", err)
	}
	for _, tc := range []struct{ in, want string }{
		{"/etc/custom.yaml", "/etc/custom.yaml"},
		{"", want},
	} {
		got, err := PathOrDefault(tc.in)
		if err != nil {
			t.Fatalf("PathOrDefault(%q) error = %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("PathOrDefault(%q) = %q, want %q", tc.in, got, tc.want)
		}
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

func TestLoadInvalidRemoteName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(`
serve:
  remotes:
    - name: ../api
`), 0o644)

	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), `invalid remote name "../api"`) {
		t.Fatalf("expected invalid remote name error, got %v", err)
	}
}

func TestLoadRejectsUnknownEnumValues(t *testing.T) {
	for _, tc := range []struct {
		name, yaml, wantErr string
	}{
		{
			name: "rule action",
			yaml: `
serve:
  rules:
    - name: bad-rule
      action: block
`,
			wantErr: `action must be "approve", "ignore" or "deny", got "block"`,
		},
		{
			name: "upstream type",
			yaml: `
serve:
  upstream:
    type: tcp
`,
			wantErr: `bus type must be "session_bus", "socket" or "sockets", got "tcp"`,
		},
		{
			name: "downstream type",
			yaml: `
serve:
  downstream:
    - type: system_bus
`,
			wantErr: `got "system_bus"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			os.WriteFile(path, []byte(tc.yaml), 0o644)
			_, err := Load(path)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("Load() error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestRuleActionDefaultsToApprove(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte(`
serve:
  rules:
    - name: no-action
      process:
        exe: /usr/bin/git
`), 0o644)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	out, err := cfg.WithDefaults()
	if err != nil {
		t.Fatalf("WithDefaults: %v", err)
	}
	if got := out.Serve.Rules[0].Action; got != approval.RuleApprove {
		t.Fatalf("Action = %q, want %q", got, approval.RuleApprove)
	}
	if cfg.Serve.Rules[0].Action != "" {
		t.Fatalf("WithDefaults changed the loaded config's rules in place")
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
	t.Setenv("XDG_CONFIG_HOME", "/custom/config")
	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	if want := "/custom/config/secrets-dispatcher/config.yaml"; got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}

func TestWithDefaults(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	cfg := &Config{}
	out, err := cfg.WithDefaults()
	if err != nil {
		t.Fatalf("WithDefaults: %v", err)
	}

	if out.Listen != DefaultListenAddr {
		t.Errorf("Listen = %q, want %q", out.Listen, DefaultListenAddr)
	}
	if out.Serve.LogLevel != DefaultLogLevel {
		t.Errorf("LogLevel = %q, want %q", out.Serve.LogLevel, DefaultLogLevel)
	}
	if out.Serve.Upstream.Type != "session_bus" {
		t.Errorf("Upstream.Type = %q, want session_bus", out.Serve.Upstream.Type)
	}
	if len(out.Serve.Downstream) != 1 {
		t.Fatalf("Downstream len = %d, want 1", len(out.Serve.Downstream))
	}
	if out.Serve.Downstream[0].Type != "sockets" {
		t.Errorf("Downstream[0].Type = %q, want sockets", out.Serve.Downstream[0].Type)
	}
	if out.Serve.Downstream[0].Path != "/run/user/1000/secrets-dispatcher/sockets" {
		t.Errorf("Downstream[0].Path = %q", out.Serve.Downstream[0].Path)
	}
}

func TestWithDefaultsSSH(t *testing.T) {
	t.Run("empty fields", func(t *testing.T) {
		t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
		t.Setenv("SSH_AUTH_SOCK", "/tmp/real-agent.sock")
		cfg := &Config{SSH: &SSHConfig{}}
		out, err := cfg.WithDefaults()
		if err != nil {
			t.Fatalf("WithDefaults: %v", err)
		}
		want := SSHConfig{
			Upstream: "/tmp/real-agent.sock",
			Listen:   "/run/user/1000/secrets-dispatcher/ssh-agent.sock",
		}
		if *out.SSH != want {
			t.Errorf("SSH = %+v, want %+v", *out.SSH, want)
		}
		if *cfg.SSH != (SSHConfig{}) {
			t.Errorf("WithDefaults changed the input's SSH section in place")
		}
	})
	t.Run("fields set", func(t *testing.T) {
		t.Setenv("XDG_RUNTIME_DIR", "")
		t.Setenv("SSH_AUTH_SOCK", "/tmp/real-agent.sock")
		want := SSHConfig{Upstream: "/tmp/up.sock", Listen: "/tmp/agent.sock"}
		cfg := &Config{
			Serve: ServeConfig{Downstream: []BusConfig{{Type: BusSocket, Path: "/tmp/bus.sock"}}},
			SSH:   &want,
		}
		out, err := cfg.WithDefaults()
		if err != nil {
			t.Fatalf("WithDefaults: %v", err)
		}
		if *out.SSH != want {
			t.Errorf("SSH = %+v, want %+v", *out.SSH, want)
		}
	})
	t.Run("no section", func(t *testing.T) {
		t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
		out, err := (&Config{}).WithDefaults()
		if err != nil {
			t.Fatalf("WithDefaults: %v", err)
		}
		if out.SSH != nil {
			t.Errorf("SSH = %+v, want nil", *out.SSH)
		}
	})
	t.Run("listen without runtime dir", func(t *testing.T) {
		t.Setenv("XDG_RUNTIME_DIR", "")
		cfg := &Config{
			Serve: ServeConfig{Downstream: []BusConfig{{Type: BusSocket, Path: "/tmp/bus.sock"}}},
			SSH:   &SSHConfig{Upstream: "/tmp/up.sock"},
		}
		if _, err := cfg.WithDefaults(); !errors.Is(err, ErrNoRuntimeDir) {
			t.Errorf("WithDefaults() error = %v, want ErrNoRuntimeDir", err)
		}
	})
}

func TestWithDefaultsDownstreamWithoutRuntimeDir(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	if _, err := (&Config{}).WithDefaults(); !errors.Is(err, ErrNoRuntimeDir) {
		t.Errorf("WithDefaults() error = %v, want ErrNoRuntimeDir", err)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{
			name: "valid session_bus upstream + sockets downstream",
			cfg: Config{Serve: ServeConfig{
				Upstream:   BusConfig{Type: "session_bus"},
				Downstream: []BusConfig{{Type: "sockets", Path: "/run/socks"}},
			}},
		},
		{
			name: "valid socket upstream + session_bus downstream",
			cfg: Config{Serve: ServeConfig{
				Upstream:   BusConfig{Type: "socket", Path: "/run/up.sock"},
				Downstream: []BusConfig{{Type: "session_bus"}},
			}},
		},
		{
			name: "valid remotes",
			cfg: Config{Serve: ServeConfig{
				Upstream:   BusConfig{Type: "session_bus"},
				Downstream: []BusConfig{{Type: "sockets", Path: "/run/socks"}},
				Remotes:    []RemoteConfig{{Name: "nb-home"}, {Name: "ci-2"}},
			}},
		},
		{
			name: "duplicate remote",
			cfg: Config{Serve: ServeConfig{
				Upstream:   BusConfig{Type: "session_bus"},
				Downstream: []BusConfig{{Type: "sockets", Path: "/run/socks"}},
				Remotes:    []RemoteConfig{{Name: "nb-home"}, {Name: "nb-home"}},
			}},
			wantErr: `remotes[1]: duplicate name "nb-home"`,
		},
		{
			name: "invalid upstream type",
			cfg: Config{Serve: ServeConfig{
				Upstream:   BusConfig{Type: "sockets"},
				Downstream: []BusConfig{{Type: "session_bus"}},
			}},
			wantErr: "upstream type must be",
		},
		{
			name: "socket upstream missing path",
			cfg: Config{Serve: ServeConfig{
				Upstream:   BusConfig{Type: "socket"},
				Downstream: []BusConfig{{Type: "session_bus"}},
			}},
			wantErr: "requires a non-empty path",
		},
		{
			name: "sockets downstream missing path",
			cfg: Config{Serve: ServeConfig{
				Upstream:   BusConfig{Type: "session_bus"},
				Downstream: []BusConfig{{Type: "sockets"}},
			}},
			wantErr: "requires a non-empty path",
		},
		{
			name: "both upstream and downstream session_bus",
			cfg: Config{Serve: ServeConfig{
				Upstream:   BusConfig{Type: "session_bus"},
				Downstream: []BusConfig{{Type: "session_bus"}},
			}},
			wantErr: "cannot both be session_bus",
		},
		{
			name: "duplicate session_bus downstream",
			cfg: Config{Serve: ServeConfig{
				Upstream:   BusConfig{Type: "socket", Path: "/up.sock"},
				Downstream: []BusConfig{{Type: "session_bus"}, {Type: "session_bus"}},
			}},
			wantErr: "at most one session_bus downstream",
		},
		{
			name: "valid deny rule",
			cfg: Config{Serve: ServeConfig{
				Upstream:   BusConfig{Type: "session_bus"},
				Downstream: []BusConfig{{Type: "sockets", Path: "/run/socks"}},
				Rules: []TrustRule{{
					Name:    "deny-epiphany",
					Action:  "deny",
					Process: &ProcessMatcher{Name: "epiphany-search"},
				}},
			}},
		},
		{
			name: "valid args rule",
			cfg: Config{Serve: ServeConfig{
				Upstream:   BusConfig{Type: "session_bus"},
				Downstream: []BusConfig{{Type: "sockets", Path: "/run/socks"}},
				Rules: []TrustRule{{
					Name:    "logcli",
					Action:  "approve",
					Process: &ProcessMatcher{Exe: "/usr/bin/bash", Args: "/home/me/.local/bin/logcli"},
				}},
			}},
		},
		{
			name: "invalid args glob",
			cfg: Config{Serve: ServeConfig{
				Upstream:   BusConfig{Type: "session_bus"},
				Downstream: []BusConfig{{Type: "sockets", Path: "/run/socks"}},
				Rules: []TrustRule{{
					Name:    "bad-args",
					Action:  "approve",
					Process: &ProcessMatcher{Args: "["},
				}},
			}},
			wantErr: "invalid glob in process.args",
		},
		{
			name: "invalid cwd glob",
			cfg: Config{Serve: ServeConfig{
				Upstream:   BusConfig{Type: "session_bus"},
				Downstream: []BusConfig{{Type: "sockets", Path: "/run/socks"}},
				Rules: []TrustRule{{
					Name:    "bad-cwd",
					Action:  "approve",
					Process: &ProcessMatcher{CWD: "["},
				}},
			}},
			wantErr: "invalid glob in process.cwd",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("Validate() = %v, want nil", err)
				}
			} else {
				if err == nil {
					t.Fatalf("Validate() = nil, want error containing %q", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("Validate() = %q, want containing %q", err, tc.wantErr)
				}
			}
		})
	}
}
