package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// Defaults for serve-subcommand settings.
const (
	DefaultListenAddr          = "127.0.0.1:8484"
	DefaultTimeout             = 5 * time.Minute
	DefaultHistoryLimit        = 100
	DefaultLogLevel            = "info"
	DefaultLogFormat           = "text"
	DefaultApprovalWindow      = 5 * time.Second
	DefaultAutoApproveDuration = 2 * time.Minute
)

var defaultNotifications = true
var defaultShowPIDs = false
var defaultTrimProcessChain = true

// BusConfig describes a D-Bus endpoint (upstream backend or downstream front).
type BusConfig struct {
	Type string `yaml:"type"`           // "session_bus", "socket", "sockets"
	Path string `yaml:"path,omitempty"` // required for "socket" and "sockets" types
}

// WithDefaults returns a copy of cfg with zero-value fields filled from program defaults.
func (cfg *Config) WithDefaults() *Config {
	out := *cfg
	if out.Listen == "" {
		out.Listen = DefaultListenAddr
	}
	s := &out.Serve
	if s.LogLevel == "" {
		s.LogLevel = DefaultLogLevel
	}
	if s.LogFormat == "" {
		s.LogFormat = DefaultLogFormat
	}
	if s.Timeout == 0 {
		s.Timeout = Duration(DefaultTimeout)
	}
	if s.HistoryLimit == 0 {
		s.HistoryLimit = DefaultHistoryLimit
	}
	if s.Notifications == nil {
		s.Notifications = &defaultNotifications
	}
	if s.ShowPIDs == nil {
		s.ShowPIDs = &defaultShowPIDs
	}
	if s.TrimProcessChain == nil {
		s.TrimProcessChain = &defaultTrimProcessChain
	}
	if s.ApprovalWindow == 0 {
		s.ApprovalWindow = Duration(DefaultApprovalWindow)
	}
	if s.AutoApproveDuration == 0 {
		s.AutoApproveDuration = Duration(DefaultAutoApproveDuration)
	}
	if s.Upstream.Type == "" {
		s.Upstream = BusConfig{Type: "session_bus"}
	}
	if len(s.Downstream) == 0 {
		s.Downstream = []BusConfig{{Type: "sockets", Path: defaultSocketsDir()}}
	}
	return &out
}

// Validate checks the config for logical errors.
func (cfg *Config) Validate() error {
	s := &cfg.Serve

	switch s.Upstream.Type {
	case "session_bus", "socket":
	default:
		return fmt.Errorf("upstream type must be \"session_bus\" or \"socket\", got %q", s.Upstream.Type)
	}
	if s.Upstream.Type == "socket" && s.Upstream.Path == "" {
		return fmt.Errorf("upstream type \"socket\" requires a non-empty path")
	}

	hasSessionBusDown := false
	for i, d := range s.Downstream {
		switch d.Type {
		case "session_bus":
			if hasSessionBusDown {
				return fmt.Errorf("downstream[%d]: at most one session_bus downstream is allowed", i)
			}
			hasSessionBusDown = true
		case "socket", "sockets":
			if d.Path == "" {
				return fmt.Errorf("downstream[%d]: type %q requires a non-empty path", i, d.Type)
			}
		default:
			return fmt.Errorf("downstream[%d]: type must be \"session_bus\", \"socket\", or \"sockets\", got %q", i, d.Type)
		}
	}

	if s.Upstream.Type == "session_bus" && hasSessionBusDown {
		return fmt.Errorf("upstream and downstream cannot both be session_bus (same bus)")
	}

	return nil
}

// defaultSocketsDir returns $XDG_RUNTIME_DIR/secrets-dispatcher/sockets.
func defaultSocketsDir() string {
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		return ""
	}
	return filepath.Join(runtimeDir, "secrets-dispatcher", "sockets")
}

// Duration wraps time.Duration with YAML unmarshalling for human-readable strings.
type Duration time.Duration

func (d Duration) MarshalYAML() (interface{}, error) {
	return time.Duration(d).String(), nil
}

func (d *Duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = Duration(parsed)
	return nil
}

// ServeConfig holds serve-subcommand settings.
type ServeConfig struct {
	Upstream       BusConfig   `yaml:"upstream"`
	Downstream     []BusConfig `yaml:"downstream"`
	LogLevel       string      `yaml:"log_level"`
	LogFormat      string      `yaml:"log_format"`
	Timeout        Duration    `yaml:"timeout"`
	HistoryLimit   int         `yaml:"history_limit"`
	Notifications  *bool       `yaml:"notifications"`
	ShowPIDs          *bool       `yaml:"show_pids"`
	TrimProcessChain    *bool       `yaml:"trim_process_chain"`
	ApprovalWindow      Duration    `yaml:"approval_window"`
	AutoApproveDuration Duration         `yaml:"auto_approve_duration"`
	TrustedSigners      []TrustedSigner  `yaml:"trusted_signers,omitempty"`
}

// TrustedSigner defines a process that is auto-approved for GPG signing.
// All three fields must match for auto-approval. Empty optional fields match anything.
type TrustedSigner struct {
	ExePath    string `yaml:"exe_path"`              // Required: absolute path to the executable
	RepoPath   string `yaml:"repo_path,omitempty"`   // Optional: repo basename (from git rev-parse --show-toplevel)
	FilePrefix string `yaml:"file_prefix,omitempty"` // Optional: all changed files must have this prefix
}

// Config is the top-level configuration file structure.
type Config struct {
	StateDir string      `yaml:"state_dir"`
	Listen   string      `yaml:"listen"`
	Serve    ServeConfig `yaml:"serve"`
}

// DefaultPath returns the default config file path using XDG_CONFIG_HOME.
func DefaultPath() string {
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		configHome = filepath.Join(home, ".config")
	}
	return filepath.Join(configHome, "secrets-dispatcher", "config.yaml")
}

// Load reads and parses a YAML config file. If the file does not exist,
// it returns an empty Config and a nil error.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, err
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}
	return &cfg, nil
}
