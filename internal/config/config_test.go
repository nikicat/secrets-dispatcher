package config

import (
	"os"
	"path"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

// Writes text to config.yaml in a new temporary directory.
func yamlFile(t *testing.T, text string) File {
	t.Helper()
	f := File{path: filepath.Join(t.TempDir(), "config.yaml")}
	require.NoError(t, os.WriteFile(f.path, []byte(text), 0o644))
	return f
}

func topology(up Upstream, down ...Bus) TopologyConfig {
	topo := TopologyConfig{Upstream: UpstreamConfig{Bus: up}}
	for _, b := range down {
		topo.Downstream = append(topo.Downstream, BusConfig{Bus: b})
	}
	return topo
}

func TestLoadFullConfig(t *testing.T) {
	cfg, err := yamlFile(t, `
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
`).Load()
	require.NoError(t, err)

	assert.Equal(t, StateDir("/tmp/state"), cfg.StateDir)
	assert.Equal(t, ListenAddr("0.0.0.0:9090"), cfg.Listen)
	assert.Equal(t, UpstreamConfig{Bus: Socket{Path: "/run/upstream.sock"}}, cfg.Serve.Upstream)
	assert.Equal(t, []BusConfig{{Bus: Sockets{Dir: "/run/socks"}}}, cfg.Serve.Downstream)
	assert.Equal(t, LogLevel("debug"), cfg.Serve.LogLevel)
	assert.Equal(t, LogFormat("json"), cfg.Serve.LogFormat)
	assert.Equal(t, 10*time.Minute, time.Duration(cfg.Serve.Timeout))
	assert.Equal(t, 50, cfg.Serve.HistoryLimit)
	assert.Equal(t, new(false), cfg.Serve.Notifications)
}

func TestLoadPartialConfig(t *testing.T) {
	cfg, err := yamlFile(t, `
listen: 127.0.0.1:5555
serve:
  log_level: warn
`).Load()
	require.NoError(t, err)

	assert.Equal(t, ListenAddr("127.0.0.1:5555"), cfg.Listen)
	assert.Equal(t, LogLevel("warn"), cfg.Serve.LogLevel)
	// Unset fields should be zero values
	assert.Empty(t, cfg.StateDir)
	assert.Nil(t, cfg.Serve.Notifications)
	assert.Zero(t, cfg.Serve.HistoryLimit)
	assert.Nil(t, cfg.Serve.Upstream.Bus)
	assert.Empty(t, cfg.Serve.Downstream)
}

func TestLoadInvalidYAML(t *testing.T) {
	_, err := yamlFile(t, `{{{not yaml`).Load()
	assert.Error(t, err)
}

func TestLoadInvalidDuration(t *testing.T) {
	_, err := yamlFile(t, `
serve:
  timeout: not-a-duration
`).Load()
	assert.Error(t, err)
}

// A negative auto_approve_duration would expire every rule as it is added.
func TestLoadAutoApproveDurationSign(t *testing.T) {
	load := func(value string) (*Config, error) {
		return yamlFile(t, "serve:\n  auto_approve_duration: "+value+"\n").Load()
	}

	_, err := load("-5m")
	assert.ErrorContains(t, err, "negative duration -5m0s")

	cfg, err := load("5m")
	require.NoError(t, err)
	assert.Equal(t, Duration(5*time.Minute), cfg.Serve.AutoApproveDuration)
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
	in := ServeConfig{TopologyConfig: topology(
		Socket{Path: "run/up.sock"},
		Sockets{Dir: "/run/socks"},
		SessionBus{},
		Socket{Path: "/run/vm.sock"},
	)}
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
	assert.Equal(t, in.TopologyConfig, out.TopologyConfig)
}

func TestLoadInvalidRemoteName(t *testing.T) {
	_, err := yamlFile(t, `
serve:
  remotes:
    - name: ../api
`).Load()
	assert.ErrorContains(t, err, `invalid remote name "../api"`)
}

// A relative exe_path could never match, since /proc reports every executable
// by its absolute path.
func TestLoadRelativeTrustedSignerExePath(t *testing.T) {
	_, err := yamlFile(t, `
serve:
  trusted_signers:
    - exe_path: bin/nvim
`).Load()
	assert.ErrorContains(t, err, `path must be absolute: "bin/nvim"`)
}

func TestResolveTrustedSignerWithoutExePath(t *testing.T) {
	cfg, err := yamlFile(t, `
serve:
  trusted_signers:
    - repo_path: dotfiles
`).Load()
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
			_, err := yamlFile(t, "serve:\n  rules:\n    - "+field+"\n").Load()
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
			_, err := yamlFile(t, "serve:\n  rules:\n    - "+field+"\n").Load()
			assert.ErrorContains(t, err, "empty glob; omit the key")
		})
	}
}

func TestLoadReadsNullScalarGlobAsOmitted(t *testing.T) {
	cfg, err := yamlFile(t, "serve:\n  rules:\n    - process: {unit: , exe: null}\n").Load()
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
			_, err := yamlFile(t, tc.yaml).Load()
			assert.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestRuleActionDefaultsToApprove(t *testing.T) {
	cfg, err := yamlFile(t, `
serve:
  rules:
    - name: no-action
      process:
        exe: /usr/bin/git
    - name: empty-action
      action: ""
`).Load()
	require.NoError(t, err)
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	out, err := cfg.WithDefaults()
	require.NoError(t, err)
	for i, r := range out.Serve.Rules {
		assert.Equal(t, approval.RuleApprove, r.Action, "rules[%d]", i)
		assert.Empty(t, cfg.Serve.Rules[i].Action, "WithDefaults changed the loaded config's rules[%d] in place", i)
	}
}

func TestWithDefaults(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")

	cfg := &Config{}
	out, err := cfg.WithDefaults()
	require.NoError(t, err)

	assert.Equal(t, DefaultListenAddr, out.Listen)
	assert.Equal(t, DefaultLogLevel, out.Serve.LogLevel)
	assert.Equal(t, UpstreamConfig{Bus: SessionBus{}}, out.Serve.Upstream)
	assert.Equal(t, []BusConfig{{Bus: Sockets{Dir: "/run/user/1000/secrets-dispatcher/sockets"}}}, out.Serve.Downstream)
}

func TestWithDefaultsSSH(t *testing.T) {
	t.Run("empty fields", func(t *testing.T) {
		t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
		t.Setenv("SSH_AUTH_SOCK", "/tmp/real-agent.sock")
		cfg := &Config{SSH: &SSHConfig{}}
		out, err := cfg.WithDefaults()
		require.NoError(t, err)
		want := SSHConfig{
			Upstream: "/tmp/real-agent.sock",
			Listen:   "/run/user/1000/secrets-dispatcher/ssh-agent.sock",
		}
		assert.Equal(t, want, *out.SSH)
		assert.Equal(t, SSHConfig{}, *cfg.SSH, "WithDefaults changed the input's SSH section in place")
	})
	t.Run("fields set", func(t *testing.T) {
		t.Setenv("XDG_RUNTIME_DIR", "")
		t.Setenv("SSH_AUTH_SOCK", "/tmp/real-agent.sock")
		want := SSHConfig{Upstream: "/tmp/up.sock", Listen: "/tmp/agent.sock"}
		cfg := &Config{
			Serve: ServeConfig{TopologyConfig: topology(nil, Socket{Path: "/tmp/bus.sock"})},
			SSH:   &want,
		}
		out, err := cfg.WithDefaults()
		require.NoError(t, err)
		assert.Equal(t, want, *out.SSH)
	})
	t.Run("no section", func(t *testing.T) {
		t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
		out, err := (&Config{}).WithDefaults()
		require.NoError(t, err)
		assert.Nil(t, out.SSH)
	})
	t.Run("listen without runtime dir", func(t *testing.T) {
		t.Setenv("XDG_RUNTIME_DIR", "")
		cfg := &Config{
			Serve: ServeConfig{TopologyConfig: topology(nil, Socket{Path: "/tmp/bus.sock"})},
			SSH:   &SSHConfig{Upstream: "/tmp/up.sock"},
		}
		_, err := cfg.WithDefaults()
		assert.ErrorIs(t, err, ErrNoRuntimeDir)
	})
}

func TestWithDefaultsDownstreamWithoutRuntimeDir(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	_, err := (&Config{}).WithDefaults()
	assert.ErrorIs(t, err, ErrNoRuntimeDir)
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
				TopologyConfig: topology(SessionBus{}, Sockets{Dir: "/run/socks"}),
			}},
		},
		{
			name: "valid socket upstream + session_bus downstream",
			cfg: Config{Serve: ServeConfig{
				TopologyConfig: topology(Socket{Path: "/run/up.sock"}, SessionBus{}),
			}},
		},
		{
			name: "valid remotes",
			cfg: Config{Serve: ServeConfig{
				TopologyConfig: topology(SessionBus{}, Sockets{Dir: "/run/socks"}),
				Remotes:        []RemoteConfig{{Name: "nb-home"}, {Name: "ci-2"}},
			}},
		},
		{
			name: "duplicate remote",
			cfg: Config{Serve: ServeConfig{
				TopologyConfig: topology(SessionBus{}, Sockets{Dir: "/run/socks"}),
				Remotes:        []RemoteConfig{{Name: "nb-home"}, {Name: "nb-home"}},
			}},
			wantErr: `remotes[1]: duplicate name "nb-home"`,
		},
		{
			name: "socket upstream missing path",
			cfg: Config{Serve: ServeConfig{
				TopologyConfig: topology(Socket{}, SessionBus{}),
			}},
			wantErr: "requires a non-empty path",
		},
		{
			name: "sockets downstream missing path",
			cfg: Config{Serve: ServeConfig{
				TopologyConfig: topology(SessionBus{}, Sockets{}),
			}},
			wantErr: "requires a non-empty path",
		},
		{
			name: "downstream without a type",
			cfg: Config{Serve: ServeConfig{
				TopologyConfig: topology(SessionBus{}, nil),
			}},
			wantErr: `downstream[0]: bus type must be session_bus, socket or sockets, got ""`,
		},
		{
			name: "both upstream and downstream session_bus",
			cfg: Config{Serve: ServeConfig{
				TopologyConfig: topology(SessionBus{}, SessionBus{}),
			}},
			wantErr: "cannot both be session_bus",
		},
		{
			name: "socket downstream named like the session bus",
			cfg: Config{Serve: ServeConfig{
				TopologyConfig: topology(SessionBus{}, Socket{Path: "/run/local.sock"}),
			}},
			wantErr: `reserved client name "local"`,
		},
		{
			name: "two socket downstreams with one client name",
			cfg: Config{Serve: ServeConfig{
				TopologyConfig: topology(SessionBus{}, Socket{Path: "/a/vm.sock"}, Socket{Path: "/b/vm.sock"}),
			}},
			wantErr: `already has the client name "vm"`,
		},
		{
			name: "duplicate session_bus downstream",
			cfg: Config{Serve: ServeConfig{
				TopologyConfig: topology(Socket{Path: "/up.sock"}, SessionBus{}, SessionBus{}),
			}},
			wantErr: "at most one session_bus downstream",
		},
		{
			name: "valid deny rule",
			cfg: Config{Serve: ServeConfig{
				TopologyConfig: topology(SessionBus{}, Sockets{Dir: "/run/socks"}),
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
				TopologyConfig: topology(SessionBus{}, Sockets{Dir: "/run/socks"}),
				Rules:          []approval.TrustRule{{Name: "drop", Action: approval.RuleIgnore}},
			}},
			wantErr: `rules[0]: action "ignore" requires non-empty request_types`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.cfg.Resolve()
			if tc.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.ErrorContains(t, err, tc.wantErr)
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
