package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/nikicat/secrets-dispatcher/internal/service"
)

// The flags of a subcommand that reads its settings from the config file. A
// flag that the user sets wins over the file's value for the same setting.
type settingsFlags struct {
	*configFlags
	cfg config.Config
}

// Returns the flags of the given subcommand, with --config and --state-dir.
func newSettingsFlags(name string) *settingsFlags {
	s := &settingsFlags{configFlags: newConfigFlags(name)}
	s.Var(&s.cfg.StateDir, "state-dir", "State `directory`")
	showDefault(s.FlagSet, "state-dir", config.DefaultStateDirHelp)
	return s
}

// Registers a flag with the given name that sets the address of serve's API.
func (s *settingsFlags) listenFlag(name, usage string) {
	s.Var(&s.cfg.Listen, name, usage)
	showDefault(s.FlagSet, name, config.DefaultListenAddr)
}

// Returns the settings that args set, over those of the config file that
// --config names. Exits when either fails to parse. A setting that neither
// sets is left zero, and config.Config.WithDefaults fills it.
func (s *settingsFlags) parse(args []string) *config.Config {
	// The first parse finds --config.
	s.Parse(args)
	source, err := s.path.OrDefault()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	file, err := source.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	s.cfg = *file
	s.Parse(args)
	return &s.cfg
}

// The flags of a subcommand that works on one config file.
type configFlags struct {
	*flag.FlagSet
	path config.Path
}

// Returns the flags of the given subcommand, with --config.
func newConfigFlags(name string) *configFlags {
	c := &configFlags{FlagSet: flag.NewFlagSet(name, flag.ExitOnError)}
	configFlag(c.FlagSet, &c.path, "Path to config `file`")
	return c
}

// Registers --config on fs, which sets p. The given what starts the help text.
// A word in backquotes in it names the flag's argument in the help.
func configFlag(fs *flag.FlagSet, p *config.Path, what string) {
	fs.Var(p, config.ConfigFlag, what)
	showDefault(fs, config.ConfigFlag, config.DefaultPathHelp)
}

// Makes the help of the flag with the given name show def as its default. It
// is for a flag whose zero value means that the setting is unset.
func showDefault(fs *flag.FlagSet, name string, def any) {
	fs.Lookup(name).DefValue = fmt.Sprint(def)
}

// Registers --log-level and --log-format on fs. They set *level and *format.
func logFlags(fs *flag.FlagSet, level *config.LogLevel, format *config.LogFormat) {
	fs.Var(level, "log-level", "Log `level`: "+config.Alternatives(config.LogLevels))
	showDefault(fs, "log-level", config.DefaultLogLevel)
	fs.Var(format, "log-format", "Log `format`: "+config.Alternatives(config.LogFormats))
	showDefault(fs, "log-format", config.DefaultLogFormat)
}

// Registers --backend on fs, which sets b. The given what names the backend at
// the start of the help text.
func backendFlag(fs *flag.FlagSet, b *service.Backend, what string) {
	fs.Var(b, "backend", what+": a `path` or one of "+service.BackendKeywords()+" (default: detected from the current provider)")
}

// Registers --verbose, which sets n.
func narrationFlag(fs *flag.FlagSet, n *service.Narration) {
	fs.Var(n, "verbose", "Narrate every file and unit change (default: milestones only)")
}

// Registers --start and its negation --no-start, which set start.
func startFlags(fs *flag.FlagSet, start *bool) {
	fs.BoolVar(start, "start", true, "Start the service immediately after installing")
	fs.Var(negatedBool{start}, "no-start", "Install and enable only — don't start now (starts on next login)")
}

// A bool flag for a *bool setting. The setting stays nil unless the flag is
// given.
type optionalBool struct{ p **bool }

func (b optionalBool) String() string {
	if b.p == nil || *b.p == nil {
		return ""
	}
	return strconv.FormatBool(**b.p)
}

func (b optionalBool) Set(value string) error {
	v, err := strconv.ParseBool(value)
	if err != nil {
		return err
	}
	*b.p = &v
	return nil
}

func (b optionalBool) IsBoolFlag() bool { return true }

// A bool flag that stores the opposite of its value in *p.
type negatedBool struct{ p *bool }

func (b negatedBool) String() string {
	if b.p == nil {
		return "false"
	}
	return strconv.FormatBool(!*b.p)
}

func (b negatedBool) Set(value string) error {
	v, err := strconv.ParseBool(value)
	if err != nil {
		return err
	}
	*b.p = !v
	return nil
}

func (b negatedBool) IsBoolFlag() bool { return true }
