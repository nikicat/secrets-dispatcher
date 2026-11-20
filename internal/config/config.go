package config

import (
	"fmt"
	"os"
	"path"
	"regexp"
	"slices"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/procutil"
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
	DefaultAutoApproveDuration     = 2 * time.Minute
	DefaultNotificationDelay       = 500 * time.Millisecond
	DefaultUpstreamSlowThreshold   = 1500 * time.Millisecond
	DefaultNotifications           = true
	DefaultShowPIDs                = false
	DefaultTrimProcessChain        = true
	DefaultIgnoreChromeDummySecret = true
)

// Returns a copy of cfg with zero-value fields filled from program defaults.
// Errors with ErrNoRuntimeDir when a default it must fill needs the runtime
// directory and there is none.
func (cfg *Config) WithDefaults() (*Config, error) {
	out := *cfg
	out.Listen = out.Listen.OrDefault()
	s := &out.Serve
	s.LogLevel = s.LogLevel.OrDefault()
	s.LogFormat = s.LogFormat.OrDefault()
	if s.Timeout == 0 {
		s.Timeout = Duration(DefaultTimeout)
	}
	if s.HistoryLimit == 0 {
		s.HistoryLimit = DefaultHistoryLimit
	}
	if s.Notifications == nil {
		s.Notifications = new(DefaultNotifications)
	}
	if s.ShowPIDs == nil {
		s.ShowPIDs = new(DefaultShowPIDs)
	}
	if s.TrimProcessChain == nil {
		s.TrimProcessChain = new(DefaultTrimProcessChain)
	}
	if s.IgnoreChromeDummySecret == nil {
		s.IgnoreChromeDummySecret = new(DefaultIgnoreChromeDummySecret)
	}
	if s.ApprovalWindow == 0 {
		s.ApprovalWindow = Duration(DefaultApprovalWindow)
	}
	if s.AutoApproveDuration == 0 {
		s.AutoApproveDuration = Duration(DefaultAutoApproveDuration)
	}
	if s.NotificationDelay == 0 {
		s.NotificationDelay = Duration(DefaultNotificationDelay)
	}
	if s.UpstreamSlowThreshold == nil {
		d := Duration(DefaultUpstreamSlowThreshold)
		s.UpstreamSlowThreshold = &d
	}
	if s.Upstream.Bus == nil {
		s.Upstream = UpstreamConfig{Bus: SessionBus{}}
	}
	if len(s.Downstream) == 0 {
		dir, err := LookupRuntimeDir()
		if err != nil {
			return nil, fmt.Errorf("no default for serve.downstream: %w", err)
		}
		s.Downstream = []BusConfig{{Bus: Sockets{Dir: dir.ClientSockets()}}}
	} else {
		s.Downstream = slices.Clone(s.Downstream)
	}
	if len(s.Rules) > 0 {
		s.Rules = slices.Clone(s.Rules)
		for i := range s.Rules {
			if s.Rules[i].Action == "" {
				s.Rules[i].Action = approval.RuleApprove
			}
		}
	}
	if out.SSH != nil {
		ssh := *out.SSH
		if ssh.Upstream == "" {
			ssh.Upstream = unixsock.Path(os.Getenv("SSH_AUTH_SOCK"))
		}
		if ssh.Listen == "" {
			dir, err := LookupRuntimeDir()
			if err != nil {
				return nil, fmt.Errorf("no default for ssh.listen: %w", err)
			}
			ssh.Listen = dir.SSHAgentSocket()
		}
		out.SSH = &ssh
	}
	return &out, nil
}

// Returns a copy of cfg with defaults filled in, ready to run. Errors as
// WithDefaults and validate do.
func (cfg *Config) Resolve() (*Config, error) {
	out, err := cfg.WithDefaults()
	if err != nil {
		return nil, err
	}
	if err := out.validate(); err != nil {
		return nil, err
	}
	return out, nil
}

// Errors when the config is inconsistent.
func (cfg *Config) validate() error {
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
		if err := rule.validate(); err != nil {
			return fmt.Errorf("rules[%d]: %w", i, err)
		}
	}
	return nil
}

// The request types a trust rule may name. Signing requests go through
// trusted_signers instead.
var ruleRequestTypes = []approval.RequestType{
	approval.RequestTypeGetSecret,
	approval.RequestTypeSearch,
	approval.RequestTypeDelete,
	approval.RequestTypeWrite,
	approval.RequestTypeUnlock,
}

// validate checks that r names valid request types for its action and that
// every glob in it parses.
func (r TrustRule) validate() error {
	for _, rt := range r.RequestTypes {
		if !slices.Contains(ruleRequestTypes, rt) {
			return fmt.Errorf("invalid request_type %q", rt)
		}
	}
	if r.Action == approval.RuleIgnore {
		if len(r.RequestTypes) == 0 {
			return fmt.Errorf("action %q requires non-empty request_types", r.Action)
		}
		for _, rt := range r.RequestTypes {
			if rt != approval.RequestTypeWrite {
				return fmt.Errorf("action %q only supports request_types [%q], got %q", r.Action, approval.RequestTypeWrite, rt)
			}
		}
	}
	for _, g := range r.globs() {
		if _, err := path.Match(g.value, "test"); err != nil {
			return fmt.Errorf("invalid glob in %s: %w", g.field, err)
		}
	}
	return nil
}

// namedGlob is one glob pattern of a trust rule and the config field it
// came from, for error messages.
type namedGlob struct{ field, value string }

// globs returns every non-empty glob pattern in r.
func (r TrustRule) globs() []namedGlob {
	var out []namedGlob
	add := func(field, value string) {
		if value != "" {
			out = append(out, namedGlob{field, value})
		}
	}
	add("client", r.Client)
	if p := r.Process; p != nil {
		add("process.exe", p.Exe)
		add("process.name", p.Name)
		add("process.args", p.Args)
		add("process.cwd", p.CWD)
		add("process.unit", p.Unit)
	}
	if sm := r.Secret; sm != nil {
		add("secret.collection", sm.Collection)
		add("secret.label", sm.Label)
		for k, v := range sm.Attributes {
			add("secret.attributes["+k+"]", v)
		}
	}
	for k, v := range r.SearchAttributes {
		add("search_attributes["+k+"]", v)
	}
	return out
}

type Duration time.Duration

func (d Duration) String() string { return time.Duration(d).String() }

func (d Duration) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

// Parses a duration in Go syntax, such as 90s or 5m.
func (d *Duration) UnmarshalText(text []byte) error {
	parsed, err := time.ParseDuration(string(text))
	if err != nil {
		return err
	}
	*d = Duration(parsed)
	return nil
}

// ServeConfig holds serve-subcommand settings.
type ServeConfig struct {
	Upstream                UpstreamConfig  `yaml:"upstream"`
	Downstream              []BusConfig     `yaml:"downstream"`
	LogLevel                LogLevel        `yaml:"log_level"`
	LogFormat               LogFormat       `yaml:"log_format"`
	Timeout                 Duration        `yaml:"timeout"`
	HistoryLimit            int             `yaml:"history_limit"`
	Notifications           *bool           `yaml:"notifications"`
	ShowPIDs                *bool           `yaml:"show_pids"`
	TrimProcessChain        *bool           `yaml:"trim_process_chain"`
	ApprovalWindow          Duration        `yaml:"approval_window"`
	AutoApproveDuration     Duration        `yaml:"auto_approve_duration"`
	NotificationDelay       Duration        `yaml:"notification_delay"`
	TrustedSigners          []TrustedSigner `yaml:"trusted_signers,omitempty"`
	IgnoreChromeDummySecret *bool           `yaml:"ignore_chrome_dummy_secret"`
	// How long the upstream may take before a request is reported as slow.
	// Unset means DefaultUpstreamSlowThreshold, and 0 turns the report off.
	UpstreamSlowThreshold *Duration `yaml:"upstream_slow_threshold"`
	// Reports slow upstream replies for every request. By default only
	// auto-approved requests get the report.
	UpstreamSlowAlways *bool          `yaml:"upstream_slow_always,omitempty"`
	Rules              []TrustRule    `yaml:"rules,omitempty"`
	Remotes            []RemoteConfig `yaml:"remotes,omitempty"`
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

// TrustedSigner defines a process that is auto-approved for GPG signing.
// All three fields must match for auto-approval. Empty optional fields match anything.
type TrustedSigner struct {
	// The absolute path of the signing executable. Required.
	ExePath procutil.Path `yaml:"exe_path"`
	// The basename of the repository's top level, as git rev-parse
	// --show-toplevel prints it.
	RepoPath string `yaml:"repo_path,omitempty"`
	// A prefix that every changed file must have.
	FilePrefix string `yaml:"file_prefix,omitempty"`
}

// TrustRule defines a declarative rule for auto-approving or ignoring requests.
type TrustRule struct {
	Name string `yaml:"name,omitempty"`
	// What the rule does with a matching request. WithDefaults fills in
	// approval.RuleApprove when the file names no action.
	Action       approval.RuleAction    `yaml:"action,omitempty"`
	RequestTypes []approval.RequestType `yaml:"request_types,omitempty"`
	// As approval.TrustRule.Client.
	Client           string            `yaml:"client,omitempty"`
	Process          *ProcessMatcher   `yaml:"process,omitempty"`
	Secret           *SecretMatcher    `yaml:"secret,omitempty"`
	SearchAttributes map[string]string `yaml:"search_attributes,omitempty"`
}

// ProcessMatcher matches against sender process attributes.
//
// Exe is the security-grade matcher: it compares the kernel-resolved
// /proc/PID/exe and cannot be spoofed. Name matches the process comm, which is
// attacker-controllable (prctl(PR_SET_NAME)) — it is advisory only and must not
// be relied on for deny rules. Args matches individual cmdline arguments, which
// are equally self-reported (a process can rewrite its argv after exec) — also
// advisory only. Unit matches the caller's real systemd unit.
type ProcessMatcher struct {
	// A glob matched against /proc/PID/exe of every process in the chain.
	Exe string `yaml:"exe,omitempty"`
	// A glob matched against the comm of every process in the chain.
	// Advisory: a process can set its own comm.
	Name string `yaml:"name,omitempty"`
	// A glob matched against each command-line argument of every process in
	// the chain. Advisory: a process can rewrite its own argv.
	Args string `yaml:"args,omitempty"`
	// A glob matched against the working directory of every process in the
	// chain.
	CWD string `yaml:"cwd,omitempty"`
	// A glob matched against the caller's systemd unit, from GetUnitByPID.
	Unit string `yaml:"unit,omitempty"`
}

// SecretMatcher matches against secret/item attributes.
type SecretMatcher struct {
	// A glob matched against the item's collection.
	Collection string `yaml:"collection,omitempty"`
	// A glob matched against the item's label.
	Label string `yaml:"label,omitempty"`
	// Attributes the item must have, each value a glob.
	Attributes map[string]string `yaml:"attributes,omitempty"`
}

// SSHConfig configures the SSH agent proxy. Nil means disabled.
type SSHConfig struct {
	// The real agent's socket. WithDefaults fills an empty one from
	// $SSH_AUTH_SOCK.
	Upstream unixsock.Path `yaml:"upstream"`
	// The proxy's socket. WithDefaults fills an empty one with the runtime
	// directory's SSHAgentSocket.
	Listen unixsock.Path `yaml:"listen"`
}

// Config is the top-level configuration file structure.
type Config struct {
	StateDir StateDir    `yaml:"state_dir"`
	Listen   ListenAddr  `yaml:"listen"`
	Serve    ServeConfig `yaml:"serve"`
	SSH      *SSHConfig  `yaml:"ssh,omitempty"`
}
