package config

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

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

	cfg, err := File{path}.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.StateDir != "/tmp/state" {
		t.Errorf("StateDir = %q, want /tmp/state", cfg.StateDir)
	}
	if cfg.Listen != "0.0.0.0:9090" {
		t.Errorf("Listen = %q, want 0.0.0.0:9090", cfg.Listen)
	}
	assert.Equal(t, UpstreamConfig{Bus: Socket{Path: "/run/upstream.sock"}}, cfg.Serve.Upstream)
	assert.Equal(t, []BusConfig{{Bus: Sockets{Dir: "/run/socks"}}}, cfg.Serve.Downstream)
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

	cfg, err := File{path}.Load()
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
	if cfg.Serve.Upstream.Bus != nil {
		t.Errorf("Upstream.Bus = %v, want nil", cfg.Serve.Upstream.Bus)
	}
	if len(cfg.Serve.Downstream) != 0 {
		t.Errorf("Downstream len = %d, want 0", len(cfg.Serve.Downstream))
	}
}

func TestLoadInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(`{{{not yaml`), 0o644)

	_, err := File{path}.Load()
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

	_, err := File{path}.Load()
	if err == nil {
		t.Fatal("expected error for invalid duration")
	}
}

func TestDurationYAMLRoundTrip(t *testing.T) {
	in := ServeConfig{Timeout: Duration(90 * time.Second)}
	data, err := yaml.Marshal(in)
	require.NoError(t, err)
	assert.Contains(t, string(data), "timeout: 1m30s")

	var out ServeConfig
	require.NoError(t, yaml.Unmarshal(data, &out))
	assert.Equal(t, in.Timeout, out.Timeout)
}

func TestBusConfigYAMLRoundTrip(t *testing.T) {
	in := ServeConfig{
		Upstream: UpstreamConfig{Bus: Socket{Path: "run/up.sock"}},
		Downstream: []BusConfig{
			{Bus: Sockets{Dir: "/run/socks"}},
			{Bus: SessionBus{}},
			{Bus: Socket{Path: "/run/vm.sock"}},
		},
	}
	data, err := yaml.Marshal(in)
	require.NoError(t, err)
	assert.Contains(t, string(data), `upstream:
    type: socket
    path: run/up.sock
downstream:
    - type: sockets
      path: /run/socks
    - type: session_bus
    - type: socket
      path: /run/vm.sock
`)

	var out ServeConfig
	require.NoError(t, yaml.Unmarshal(data, &out))
	assert.Equal(t, in.Upstream, out.Upstream)
	assert.Equal(t, in.Downstream, out.Downstream)
}

func TestLoadInvalidRemoteName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	os.WriteFile(path, []byte(`
serve:
  remotes:
    - name: ../api
`), 0o644)

	_, err := File{path}.Load()
	if err == nil || !strings.Contains(err.Error(), `invalid remote name "../api"`) {
		t.Fatalf("expected invalid remote name error, got %v", err)
	}
}

// A relative exe_path could never match, since /proc reports every executable
// by its absolute path.
func TestLoadRelativeTrustedSignerExePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
serve:
  trusted_signers:
    - exe_path: bin/nvim
`), 0o644))

	_, err := File{path}.Load()
	assert.ErrorContains(t, err, `path must be absolute: "bin/nvim"`)
}

func TestResolveTrustedSignerWithoutExePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`
serve:
  trusted_signers:
    - repo_path: dotfiles
`), 0o644))

	cfg, err := File{path}.Load()
	require.NoError(t, err)
	_, err = cfg.Resolve()
	assert.ErrorContains(t, err, "trusted_signers[0]: exe_path is required")
}

// A malformed glob fails the load, wherever in a rule it is.
func TestLoadRejectsInvalidGlob(t *testing.T) {
	for _, field := range []string{
		"client: '['",
		"process: {args: '['}",
		"process: {cwd: '['}",
		"secret: {attributes: {service: '['}}",
		"search_attributes: {service: '['}",
	} {
		t.Run(field, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "config.yaml")
			require.NoError(t, os.WriteFile(file, []byte("serve:\n  rules:\n    - "+field+"\n"), 0o644))

			_, err := File{file}.Load()
			assert.ErrorIs(t, err, path.ErrBadPattern)
		})
	}
}

func TestLoadRejectsEmptyGlob(t *testing.T) {
	for _, field := range []string{
		"client: ''",
		"process: {unit: ''}",
		"secret: {label: ''}",
		"secret: {attributes: {service: ''}}",
		"search_attributes: {service: ''}",
	} {
		t.Run(field, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			require.NoError(t, os.WriteFile(path, []byte("serve:\n  rules:\n    - "+field+"\n"), 0o644))

			_, err := File{path}.Load()
			assert.ErrorContains(t, err, "empty glob; omit the key")
		})
	}
}

func TestLoadReadsNullScalarGlobAsOmitted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("serve:\n  rules:\n    - process: {unit: , exe: null}\n"), 0o644))

	cfg, err := File{path}.Load()
	require.NoError(t, err)
	assert.Equal(t, approval.ProcessMatcher{}, *cfg.Serve.Rules[0].Process)
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
			wantErr: `bus type must be session_bus, socket or sockets, got "tcp"`,
		},
		{
			name: "sockets upstream",
			yaml: `
serve:
  upstream:
    type: sockets
    path: /run/socks
`,
			wantErr: `upstream type must be session_bus or socket, got "sockets"`,
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
		{
			name:    "listen address",
			yaml:    "listen: \"8484\"\n",
			wantErr: `listen address "8484"`,
		},
		{
			name:    "log level",
			yaml:    "serve:\n  log_level: verbose\n",
			wantErr: `log level must be`,
		},
		{
			name:    "log format",
			yaml:    "serve:\n  log_format: logfmt\n",
			wantErr: `log format must be`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			os.WriteFile(path, []byte(tc.yaml), 0o644)
			_, err := File{path}.Load()
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
    - name: empty-action
      action: ""
`), 0o644)
	cfg, err := File{path}.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	out, err := cfg.WithDefaults()
	if err != nil {
		t.Fatalf("WithDefaults: %v", err)
	}
	for i, r := range out.Serve.Rules {
		if r.Action != approval.RuleApprove {
			t.Fatalf("rules[%d].Action = %q, want %q", i, r.Action, approval.RuleApprove)
		}
		if cfg.Serve.Rules[i].Action != "" {
			t.Fatalf("WithDefaults changed the loaded config's rules[%d] in place", i)
		}
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

	cfgFalse, err := File{pathFalse}.Load()
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

	cfgUnset, err := File{pathUnset}.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfgUnset.Serve.Notifications != nil {
		t.Errorf("unset notifications should be nil, got %v", *cfgUnset.Serve.Notifications)
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
	assert.Equal(t, UpstreamConfig{Bus: SessionBus{}}, out.Serve.Upstream)
	assert.Equal(t, []BusConfig{{Bus: Sockets{Dir: "/run/user/1000/secrets-dispatcher/sockets"}}}, out.Serve.Downstream)
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
			Serve: ServeConfig{Downstream: []BusConfig{{Bus: Socket{Path: "/tmp/bus.sock"}}}},
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
			Serve: ServeConfig{Downstream: []BusConfig{{Bus: Socket{Path: "/tmp/bus.sock"}}}},
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

func TestResolveValidates(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{
			name: "valid session_bus upstream + sockets downstream",
			cfg: Config{Serve: ServeConfig{
				Upstream:   UpstreamConfig{Bus: SessionBus{}},
				Downstream: []BusConfig{{Bus: Sockets{Dir: "/run/socks"}}},
			}},
		},
		{
			name: "valid socket upstream + session_bus downstream",
			cfg: Config{Serve: ServeConfig{
				Upstream:   UpstreamConfig{Bus: Socket{Path: "/run/up.sock"}},
				Downstream: []BusConfig{{Bus: SessionBus{}}},
			}},
		},
		{
			name: "valid remotes",
			cfg: Config{Serve: ServeConfig{
				Upstream:   UpstreamConfig{Bus: SessionBus{}},
				Downstream: []BusConfig{{Bus: Sockets{Dir: "/run/socks"}}},
				Remotes:    []RemoteConfig{{Name: "nb-home"}, {Name: "ci-2"}},
			}},
		},
		{
			name: "duplicate remote",
			cfg: Config{Serve: ServeConfig{
				Upstream:   UpstreamConfig{Bus: SessionBus{}},
				Downstream: []BusConfig{{Bus: Sockets{Dir: "/run/socks"}}},
				Remotes:    []RemoteConfig{{Name: "nb-home"}, {Name: "nb-home"}},
			}},
			wantErr: `remotes[1]: duplicate name "nb-home"`,
		},
		{
			name: "socket upstream missing path",
			cfg: Config{Serve: ServeConfig{
				Upstream:   UpstreamConfig{Bus: Socket{}},
				Downstream: []BusConfig{{Bus: SessionBus{}}},
			}},
			wantErr: "requires a non-empty path",
		},
		{
			name: "sockets downstream missing path",
			cfg: Config{Serve: ServeConfig{
				Upstream:   UpstreamConfig{Bus: SessionBus{}},
				Downstream: []BusConfig{{Bus: Sockets{}}},
			}},
			wantErr: "requires a non-empty path",
		},
		{
			name: "downstream without a type",
			cfg: Config{Serve: ServeConfig{
				Upstream:   UpstreamConfig{Bus: SessionBus{}},
				Downstream: []BusConfig{{}},
			}},
			wantErr: `downstream[0]: bus type must be session_bus, socket or sockets, got ""`,
		},
		{
			name: "both upstream and downstream session_bus",
			cfg: Config{Serve: ServeConfig{
				Upstream:   UpstreamConfig{Bus: SessionBus{}},
				Downstream: []BusConfig{{Bus: SessionBus{}}},
			}},
			wantErr: "cannot both be session_bus",
		},
		{
			name: "socket downstream named like the session bus",
			cfg: Config{Serve: ServeConfig{
				Upstream:   UpstreamConfig{Bus: SessionBus{}},
				Downstream: []BusConfig{{Bus: Socket{Path: "/run/local.sock"}}},
			}},
			wantErr: `reserved client name "local"`,
		},
		{
			name: "two socket downstreams with one client name",
			cfg: Config{Serve: ServeConfig{
				Upstream:   UpstreamConfig{Bus: SessionBus{}},
				Downstream: []BusConfig{{Bus: Socket{Path: "/a/vm.sock"}}, {Bus: Socket{Path: "/b/vm.sock"}}},
			}},
			wantErr: `already has the client name "vm"`,
		},
		{
			name: "duplicate session_bus downstream",
			cfg: Config{Serve: ServeConfig{
				Upstream:   UpstreamConfig{Bus: Socket{Path: "/up.sock"}},
				Downstream: []BusConfig{{Bus: SessionBus{}}, {Bus: SessionBus{}}},
			}},
			wantErr: "at most one session_bus downstream",
		},
		{
			name: "valid deny rule",
			cfg: Config{Serve: ServeConfig{
				Upstream:   UpstreamConfig{Bus: SessionBus{}},
				Downstream: []BusConfig{{Bus: Sockets{Dir: "/run/socks"}}},
				Rules: []approval.TrustRule{{
					Name:    "deny-epiphany",
					Action:  "deny",
					Process: &approval.ProcessMatcher{Name: approval.MustGlob("epiphany-search")},
				}},
			}},
		},
		{
			name: "ignore rule without request_types",
			cfg: Config{Serve: ServeConfig{
				Upstream:   UpstreamConfig{Bus: SessionBus{}},
				Downstream: []BusConfig{{Bus: Sockets{Dir: "/run/socks"}}},
				Rules:      []approval.TrustRule{{Name: "drop", Action: approval.RuleIgnore}},
			}},
			wantErr: `rules[0]: action "ignore" requires non-empty request_types`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.cfg.Resolve()
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("Resolve() = %v, want nil", err)
				}
			} else {
				if err == nil {
					t.Fatalf("Resolve() = nil, want error containing %q", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("Resolve() = %q, want containing %q", err, tc.wantErr)
				}
			}
		})
	}
}

func TestSocketClient(t *testing.T) {
	client, err := Socket{Path: "/run/vm.sock"}.Client()
	require.NoError(t, err)
	assert.Equal(t, approval.ClientName("vm"), client)
}

// Pins the YAML that `config show --defaults` prints, and that it reads back
// as the same settings.
func TestSettingsYAML(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	t.Setenv("SSH_AUTH_SOCK", "/tmp/agent.sock")
	tests := []struct {
		name, file, want string
	}{
		{"defaults", "{}", `state_dir: ""
listen: 127.0.0.1:8484
serve:
    upstream:
        type: session_bus
    downstream:
        - type: sockets
          path: /run/user/1000/secrets-dispatcher/sockets
    log_level: info
    log_format: text
    timeout: 5m0s
    history_limit: 100
    notifications: true
    show_pids: false
    trim_process_chain: true
    approval_window: 5s
    auto_approve_duration: 2m0s
    notification_delay: 500ms
    ignore_chrome_dummy_secret: true
    upstream_slow_threshold: 1.5s
    upstream_slow_always: false
`},
		{"set against the defaults", `
serve:
  notifications: false
  show_pids: true
  trim_process_chain: false
  ignore_chrome_dummy_secret: false
  upstream_slow_threshold: 0s
  upstream_slow_always: true
  remotes:
    - name: box
ssh: {}
`, `state_dir: ""
listen: 127.0.0.1:8484
serve:
    upstream:
        type: session_bus
    downstream:
        - type: sockets
          path: /run/user/1000/secrets-dispatcher/sockets
    log_level: info
    log_format: text
    timeout: 5m0s
    history_limit: 100
    notifications: false
    show_pids: true
    trim_process_chain: false
    approval_window: 5s
    auto_approve_duration: 2m0s
    notification_delay: 500ms
    ignore_chrome_dummy_secret: false
    upstream_slow_threshold: 0s
    upstream_slow_always: true
    remotes:
        - name: box
ssh:
    upstream: /tmp/agent.sock
    listen: /run/user/1000/secrets-dispatcher/ssh-agent.sock
`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var cfg Config
			require.NoError(t, yaml.Unmarshal([]byte(tc.file), &cfg))
			settings, err := cfg.WithDefaults()
			require.NoError(t, err)
			out, err := yaml.Marshal(settings)
			require.NoError(t, err)
			assert.Equal(t, tc.want, string(out))

			var reread Config
			require.NoError(t, yaml.Unmarshal(out, &reread))
			again, err := reread.WithDefaults()
			require.NoError(t, err)
			assert.Equal(t, settings, again)
		})
	}
}
