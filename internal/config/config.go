package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// Duration wraps time.Duration with YAML unmarshalling for human-readable strings.
type Duration time.Duration

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
	SocketsDir   string   `yaml:"sockets_dir"`
	RemoteSocket string   `yaml:"remote_socket"`
	Client       string   `yaml:"client"`
	LogLevel     string   `yaml:"log_level"`
	LogFormat    string   `yaml:"log_format"`
	Timeout      Duration `yaml:"timeout"`
	HistoryLimit int      `yaml:"history_limit"`
	Notifications *bool   `yaml:"notifications"`
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
