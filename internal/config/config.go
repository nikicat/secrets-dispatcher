package config

import (
	"cmp"
	"fmt"
	"os"
	"regexp"
	"slices"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/proxy"
	"github.com/nikicat/secrets-dispatcher/internal/unixsock"
)

// Defaults for settings that neither the config file nor a flag sets.
const (
	DefaultListenAddr              = ListenAddr("127.0.0.1:8484")
	DefaultTimeout                 = 5 * time.Minute
	DefaultHistoryLimit            = 100
	DefaultLogLevel                = LogInfo
	DefaultLogFormat               = LogText
	DefaultApprovalWindow          = 5 * time.Second
	DefaultNotificationDelay       = 500 * time.Millisecond
	DefaultUpstreamSlowThreshold   = 1500 * time.Millisecond
	DefaultUpstreamSlowAlways      = false
	DefaultNotifications           = true
	DefaultShowPIDs                = false
	DefaultTrimProcessChain        = true
	DefaultIgnoreChromeDummySecret = true
)

// Returns the settings in cfg, with every setting that cfg leaves unset filled
// from the program defaults. Errors with ErrNoRuntimeDir when a default it must
// fill needs the runtime directory and there is none.
func (cfg *Config) WithDefaults() (*Settings, error) {
	serve, err := cfg.Serve.withDefaults()
	if err != nil {
		return nil, err
	}
	out := &Settings{
		StateDir: cfg.StateDir,
		Listen:   cfg.Listen.OrDefault(),
		Serve:    serve,
	}
	if cfg.SSH != nil {
		ssh, err := cfg.SSH.withDefaults()
		if err != nil {
			return nil, err
		}
		out.SSH = &ssh
	}
	return out, nil
}

func (s ServeConfig) withDefaults() (ServeSettings, error) {
	out := ServeSettings{
		TopologyConfig:          s.TopologyConfig.clone(),
		LogLevel:                s.LogLevel.OrDefault(),
		LogFormat:               s.LogFormat.OrDefault(),
		Timeout:                 cmp.Or(s.Timeout, Duration(DefaultTimeout)),
		HistoryLimit:            cmp.Or(s.HistoryLimit, DefaultHistoryLimit),
		Notifications:           orDefault(s.Notifications, DefaultNotifications),
		ShowPIDs:                orDefault(s.ShowPIDs, DefaultShowPIDs),
		TrimProcessChain:        orDefault(s.TrimProcessChain, DefaultTrimProcessChain),
		ApprovalWindow:          cmp.Or(s.ApprovalWindow, Duration(DefaultApprovalWindow)),
		AutoApproveDuration:     cmp.Or(s.AutoApproveDuration, Duration(approval.DefaultAutoApproveDuration)),
		NotificationDelay:       cmp.Or(s.NotificationDelay, Duration(DefaultNotificationDelay)),
		TrustedSigners:          s.TrustedSigners,
		IgnoreChromeDummySecret: orDefault(s.IgnoreChromeDummySecret, DefaultIgnoreChromeDummySecret),
		UpstreamSlowThreshold:   orDefault(s.UpstreamSlowThreshold, Duration(DefaultUpstreamSlowThreshold)),
		UpstreamSlowAlways:      orDefault(s.UpstreamSlowAlways, DefaultUpstreamSlowAlways),
		Rules:                   approval.WithDefaultActions(s.Rules),
		Remotes:                 s.Remotes,
	}
	if out.Upstream.Bus == nil {
		out.Upstream = SessionUpstream()
	}
	if len(out.Downstream) == 0 {
		dir, err := runtimeDirFor("serve.downstream")
		if err != nil {
			return ServeSettings{}, err
		}
		out.Downstream = []BusConfig{dir.ClientsDownstream()}
	}
	return out, nil
}

func orDefault[T any](v *T, def T) T {
	if v == nil {
		return def
	}
	return *v
}

func (ssh SSHConfig) withDefaults() (SSHConfig, error) {
	if ssh.Upstream == "" {
		ssh.Upstream = unixsock.Path(os.Getenv("SSH_AUTH_SOCK"))
	}
	if ssh.Listen == "" {
		dir, err := runtimeDirFor("ssh.listen")
		if err != nil {
			return SSHConfig{}, err
		}
		ssh.Listen = dir.SSHAgentSocket()
	}
	return ssh, nil
}

// Errors with ErrNoRuntimeDir, naming the config key whose default needed
// the runtime directory.
func runtimeDirFor(key string) (RuntimeDir, error) {
	dir, err := LookupRuntimeDir()
	if err != nil {
		return "", fmt.Errorf("no default for %s: %w", key, err)
	}
	return dir, nil
}

// Returns the settings in cfg with defaults filled in, ready to run. Errors as
// WithDefaults and validate do.
func (cfg *Config) Resolve() (*Settings, error) {
	out, err := cfg.WithDefaults()
	if err != nil {
		return nil, err
	}
	if err := out.validate(); err != nil {
		return nil, err
	}
	return out, nil
}

// Errors when the settings are inconsistent.
func (cfg *Settings) validate() error {
	s := &cfg.Serve

	if err := s.Upstream.Bus.validate(); err != nil {
		return fmt.Errorf("upstream: %w", err)
	}

	check := newDownstreamCheck(s.Upstream.Bus)
	for i, d := range s.Downstream {
		if d.Bus == nil {
			return fmt.Errorf("downstream[%d]: %w", i, BusTypes.Unknown(""))
		}
		if err := d.Bus.validate(); err != nil {
			return fmt.Errorf("downstream[%d]: %w", i, err)
		}
		if err := d.Bus.Visit(check); err != nil {
			return fmt.Errorf("downstream[%d]: %w", i, err)
		}
	}

	seen := map[RemoteName]bool{}
	for i, r := range s.Remotes {
		if seen[r.Name] {
			return fmt.Errorf("remotes[%d]: duplicate name %q", i, r.Name)
		}
		seen[r.Name] = true
	}

	for i, rule := range s.Rules {
		if err := rule.Validate(); err != nil {
			return fmt.Errorf("rules[%d]: %w", i, err)
		}
	}
	for i, signer := range s.TrustedSigners {
		if err := signer.Validate(); err != nil {
			return fmt.Errorf("trusted_signers[%d]: %w", i, err)
		}
	}
	return nil
}

type Duration time.Duration

func (d Duration) String() string { return time.Duration(d).String() }

func (d Duration) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

// Parses a duration in Go syntax, such as 90s or 5m. Errors when the duration
// is negative, since no setting gives a negative duration a meaning.
func (d *Duration) UnmarshalText(text []byte) error {
	parsed, err := time.ParseDuration(string(text))
	if err != nil {
		return err
	}
	if parsed < 0 {
		return fmt.Errorf("negative duration %s", parsed)
	}
	*d = Duration(parsed)
	return nil
}

// The bus the daemon forwards requests to, and the buses where it serves
// clients (ADR 0001).
type TopologyConfig struct {
	Upstream   UpstreamConfig `yaml:"upstream"`
	Downstream []BusConfig    `yaml:"downstream"`
}

func (t TopologyConfig) Equal(o TopologyConfig) bool {
	return t.Upstream == o.Upstream && slices.Equal(t.Downstream, o.Downstream)
}

func (t TopologyConfig) clone() TopologyConfig {
	t.Downstream = slices.Clone(t.Downstream)
	return t
}

// The settings of the serve command.
type ServeConfig struct {
	TopologyConfig          `yaml:",inline"`
	LogLevel                LogLevel                 `yaml:"log_level"`
	LogFormat               LogFormat                `yaml:"log_format"`
	Timeout                 Duration                 `yaml:"timeout"`
	HistoryLimit            int                      `yaml:"history_limit"`
	Notifications           *bool                    `yaml:"notifications"`
	ShowPIDs                *bool                    `yaml:"show_pids"`
	TrimProcessChain        *bool                    `yaml:"trim_process_chain"`
	ApprovalWindow          Duration                 `yaml:"approval_window"`
	AutoApproveDuration     Duration                 `yaml:"auto_approve_duration"`
	NotificationDelay       Duration                 `yaml:"notification_delay"`
	TrustedSigners          []approval.TrustedSigner `yaml:"trusted_signers,omitempty"`
	IgnoreChromeDummySecret *bool                    `yaml:"ignore_chrome_dummy_secret"`
	// How long the upstream may take before a request is reported as slow.
	// Unset means DefaultUpstreamSlowThreshold, and 0 turns the report off.
	UpstreamSlowThreshold *Duration `yaml:"upstream_slow_threshold"`
	// Reports slow upstream replies for every request. By default a request
	// that the user approved at a prompt gets no report.
	UpstreamSlowAlways *bool                `yaml:"upstream_slow_always,omitempty"`
	Rules              []approval.TrustRule `yaml:"rules,omitempty"`
	Remotes            []RemoteConfig       `yaml:"remotes,omitempty"`
}

// A remote host that signs git commits through the signing socket the daemon
// serves for it (ADR 0011).
type RemoteConfig struct {
	Name RemoteName `yaml:"name"`
}

// A remote's name, restricted so that it can name the remote's socket file.
type RemoteName string

var remoteNameFormat = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// Returns the approval client that every request from the remote is filed
// under (ADR 0011).
func (n RemoteName) Client() approval.ClientName { return approval.ClientName("remote:" + n) }

func (n *RemoteName) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	if !remoteNameFormat.MatchString(s) {
		return fmt.Errorf("invalid remote name %q: must match %s", s, remoteNameFormat)
	}
	*n = RemoteName(s)
	return nil
}

// The settings of the SSH agent proxy.
type SSHConfig struct {
	// The real agent's socket. WithDefaults fills an empty one from
	// $SSH_AUTH_SOCK.
	Upstream unixsock.Path `yaml:"upstream"`
	// The proxy's socket. WithDefaults fills an empty one with the runtime
	// directory's SSHAgentSocket.
	Listen unixsock.Path `yaml:"listen"`
}

// The configuration file.
type Config struct {
	StateDir StateDir    `yaml:"state_dir"`
	Listen   ListenAddr  `yaml:"listen"`
	Serve    ServeConfig `yaml:"serve"`
	SSH      *SSHConfig  `yaml:"ssh,omitempty"`
}

// A Config with every default filled in, so each setting is a plain value
// (ARCHITECTURE.md, "Command line and settings"). It marshals to the same keys
// as Config.
type Settings struct {
	StateDir StateDir      `yaml:"state_dir"`
	Listen   ListenAddr    `yaml:"listen"`
	Serve    ServeSettings `yaml:"serve"`
	SSH      *SSHConfig    `yaml:"ssh,omitempty"`
}

// A ServeConfig with every default filled in.
type ServeSettings struct {
	TopologyConfig          `yaml:",inline"`
	LogLevel                LogLevel                 `yaml:"log_level"`
	LogFormat               LogFormat                `yaml:"log_format"`
	Timeout                 Duration                 `yaml:"timeout"`
	HistoryLimit            int                      `yaml:"history_limit"`
	Notifications           bool                     `yaml:"notifications"`
	ShowPIDs                bool                     `yaml:"show_pids"`
	TrimProcessChain        bool                     `yaml:"trim_process_chain"`
	ApprovalWindow          Duration                 `yaml:"approval_window"`
	AutoApproveDuration     Duration                 `yaml:"auto_approve_duration"`
	NotificationDelay       Duration                 `yaml:"notification_delay"`
	TrustedSigners          []approval.TrustedSigner `yaml:"trusted_signers,omitempty"`
	IgnoreChromeDummySecret bool                     `yaml:"ignore_chrome_dummy_secret"`
	UpstreamSlowThreshold   Duration                 `yaml:"upstream_slow_threshold"`
	UpstreamSlowAlways      bool                     `yaml:"upstream_slow_always"`
	Rules                   []approval.TrustRule     `yaml:"rules,omitempty"`
	Remotes                 []RemoteConfig           `yaml:"remotes,omitempty"`
}

// Returns the approval manager's part of the settings.
func (s ServeSettings) ManagerConfig() approval.ManagerConfig {
	return approval.ManagerConfig{
		Timeout:             time.Duration(s.Timeout),
		HistoryMax:          s.HistoryLimit,
		ApprovalWindow:      time.Duration(s.ApprovalWindow),
		AutoApproveDuration: time.Duration(s.AutoApproveDuration),
		TrustedSigners:      s.TrustedSigners,
		IgnoreChromeDummy:   s.IgnoreChromeDummySecret,
		TrustRules:          s.Rules,
	}
}

// Returns the slow-upstream report settings, without the notifier.
func (s ServeSettings) SlowUpstream() proxy.SlowUpstream {
	return proxy.SlowUpstream{
		Threshold: time.Duration(s.UpstreamSlowThreshold),
		Always:    s.UpstreamSlowAlways,
	}
}
