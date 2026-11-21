package main

import (
	"cmp"
	"fmt"

	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/nikicat/secrets-dispatcher/internal/service"
)

// The --config flag of a command that reads one config file.
type configFlag struct {
	Config config.Path `placeholder:"FILE" help:"Config file (default: ${default_config})."`
}

// Returns the config file that --config names, or the default one. Errors
// when --config is unset and there is no default.
func (f configFlag) source() (config.Source, error) {
	source, err := f.Config.OrDefault()
	if err != nil {
		return config.Source{}, fmt.Errorf("%w (set XDG_CONFIG_HOME or use --%s)", err, config.ConfigFlag)
	}
	return source, nil
}

// Returns the config in the file that --config names, or in the default one.
// Errors when that file fails to load, or as source does.
func (f configFlag) load() (*config.Config, error) {
	source, err := f.source()
	if err != nil {
		return nil, err
	}
	return source.Load()
}

// Errors when the config fails to load, or loads but fails to resolve.
func (f configFlag) validate() error {
	cfg, err := f.load()
	if err != nil {
		return err
	}
	_, err = cfg.Resolve()
	return err
}

// The flags of a command that reads its settings from the config file. A flag
// that the user gives wins over the file (ARCHITECTURE.md, "Command line and
// settings").
type settingsFlags struct {
	configFlag
	StateDir config.StateDir `type:"path" default:"" placeholder:"DIR" help:"State directory (default: ${default_state_dir})."`
}

// Returns the settings in the config file, with --state-dir over them. Errors
// when the file fails to load.
func (f settingsFlags) load() (*config.Config, error) {
	cfg, err := f.configFlag.load()
	if err != nil {
		return nil, err
	}
	cfg.StateDir = cmp.Or(f.StateDir, cfg.StateDir)
	return cfg, nil
}

// Returns the endpoint of serve's API that the settings name, at addr when it
// is set. Errors as loadAPIEndpoint does.
func (f settingsFlags) endpoint(addr config.ListenAddr) (apiEndpoint, error) {
	cfg, err := f.load()
	if err != nil {
		return apiEndpoint{}, err
	}
	cfg.Listen = cmp.Or(addr, cfg.Listen)
	return loadAPIEndpoint(cfg)
}

type listenFlag struct {
	Listen config.ListenAddr `placeholder:"ADDRESS" help:"HTTP API listen address (default: ${default_listen})."`
}

// The flags of serve. Each one wins over the config file, as settingsFlags'
// flags do.
type serveFlags struct {
	settingsFlags
	listenFlag
	logFlags
	Timeout       config.Duration `placeholder:"DURATION" help:"Approval timeout (default: ${default_timeout})."`
	HistoryLimit  int             `placeholder:"N" help:"Maximum number of resolved requests to keep in history (default: ${default_history_limit})."`
	Notifications *bool           `negatable:"" help:"Desktop notifications for approval requests (default: ${default_notifications})."`
}

// Returns the config file merged with the flags the user gave, before Resolve.
func (f serveFlags) merged() (*config.Config, error) {
	cfg, err := f.load()
	if err != nil {
		return nil, err
	}
	cfg.Listen = cmp.Or(f.Listen, cfg.Listen)
	s := &cfg.Serve
	s.LogLevel = cmp.Or(f.LogLevel, s.LogLevel)
	s.LogFormat = cmp.Or(f.LogFormat, s.LogFormat)
	s.Timeout = cmp.Or(f.Timeout, s.Timeout)
	s.HistoryLimit = cmp.Or(f.HistoryLimit, s.HistoryLimit)
	s.Notifications = cmp.Or(f.Notifications, s.Notifications)
	return cfg, nil
}

// The --log-level and --log-format flags. Their types validate the text
// (ARCHITECTURE.md, "Command line and settings").
type logFlags struct {
	LogLevel  config.LogLevel  `placeholder:"LEVEL" help:"Log level: ${log_levels} (default: ${default_log_level})."`
	LogFormat config.LogFormat `placeholder:"FORMAT" help:"Log format: ${log_formats} (default: ${default_log_format})."`
}

// The --backend flag of a command that sets up a private backend.
type backendFlag struct {
	Backend *service.Backend `placeholder:"BACKEND" help:"Private backend: ${backends}, or a command line that contains a slash (default: detected from the current provider)."`
}

// The --verbose flag of a command that changes the user's service setup.
type narrationFlag struct {
	Verbose bool `help:"Narrate every file and unit change (default: milestones only)."`
}

// Returns how much of its work the command prints.
func (f narrationFlag) narration() service.Narration {
	if f.Verbose {
		return service.NarrateChanges
	}
	return service.NarrateMilestones
}
