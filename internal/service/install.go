// Package service manages the systemd user service for secrets-dispatcher.
package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"github.com/nikicat/secrets-dispatcher/internal/config"
	"gopkg.in/yaml.v3"
)

const unitFileName = "secrets-dispatcher.service"

// allUnitNames lists every unit file the package may install.
var allUnitNames = []string{
	unitFileName,
	// Legacy local/full topology units. New installs no longer create these
	// because the backend bus is supervised privately inside secrets-dispatcher.
	"secrets-dispatcher-bus.socket",
	"secrets-dispatcher-bus.service",
	"secrets-dispatcher-backend.service",
}

const unitTemplate = `[Unit]
Description=Secrets Dispatcher - D-Bus Secret Service approval proxy
Documentation=https://github.com/nikicat/secrets-dispatcher

[Service]
Type=simple
ExecStart=%s
Restart=on-failure
RestartSec=5

[Install]
WantedBy=graphical-session.target
`

const localProxyTemplate = `[Unit]
Description=Secrets Dispatcher - D-Bus Secret Service approval proxy
Documentation=https://github.com/nikicat/secrets-dispatcher

[Service]
Type=simple
ExecStart=%s
Restart=on-failure
RestartSec=5

[Install]
WantedBy=graphical-session.target
`

const (
	dbusServiceFile    = "org.freedesktop.secrets.service"
	dbusBackupSuffix   = ".pre-dispatcher"
	dbusActivationMask = "[D-BUS Service]\nName=org.freedesktop.secrets\nExec=/bin/false\n"

	gnomeKeyringStateFile = "gnome-keyring-units.pre-dispatcher.yaml"
)

var gnomeKeyringPublicUnits = []string{
	"gnome-keyring-daemon.service",
	"gnome-keyring-daemon.socket",
}

type gnomeKeyringState struct {
	Version int                     `yaml:"version"`
	Units   []gnomeKeyringUnitState `yaml:"units"`
}

type gnomeKeyringUnitState struct {
	Name    string `yaml:"name"`
	Enabled string `yaml:"enabled"`
	Active  string `yaml:"active"`
}

// Options configures service installation.
type Options struct {
	// ConfigPath overrides the config file location (default: config.DefaultPath()).
	ConfigPath string
	// Start the service immediately after enabling.
	Start bool
	// Mode selects the topology: "remote", "local", or "full" (default: "remote").
	Mode string
	// BackendPath overrides the backend command for local/full modes.
	// Special values: "gnome-keyring" or "gnome-keyring-daemon" use a GNOME Keyring preset.
	BackendPath string
}

// lookPathFunc is the function used to find binaries on PATH.
// Replaced in tests.
var lookPathFunc = exec.LookPath

// execOutputFunc runs a command and returns stdout. Replaced in tests.
var execOutputFunc = func(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

// systemctlOutputFunc runs systemctl --user commands and returns their output.
// Replaced in tests.
var systemctlOutputFunc = func(args ...string) ([]byte, error) {
	fullArgs := append([]string{"--user"}, args...)
	return exec.Command("systemctl", fullArgs...).CombinedOutput()
}

// unitDir returns the systemd user unit directory.
// Uses $XDG_CONFIG_HOME/systemd/user/ with fallback to ~/.config/systemd/user/.
func unitDir() (string, error) {
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("get home dir: %w", err)
		}
		configHome = filepath.Join(home, ".config")
	}
	return filepath.Join(configHome, "systemd", "user"), nil
}

// UnitPath returns the full path where the proxy unit file is (or would be) installed.
func UnitPath() (string, error) {
	dir, err := unitDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, unitFileName), nil
}

// Install writes systemd user unit files, updates the config topology,
// reloads systemd, and enables the service.
func Install(opts Options) error {
	mode := opts.Mode
	if mode == "" {
		mode = "remote"
	}
	switch mode {
	case "remote", "local", "full":
	default:
		return fmt.Errorf("unknown mode %q (must be remote, local, or full)", mode)
	}

	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		return fmt.Errorf("XDG_RUNTIME_DIR must be set")
	}

	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}

	// Resolve config path.
	configPath := opts.ConfigPath
	if configPath == "" {
		configPath = config.DefaultPath()
		if configPath == "" {
			return fmt.Errorf("cannot determine config path (is HOME set?)")
		}
	}

	var backendCommand string
	usesGnomeKeyringBackend := false
	if mode == "local" || mode == "full" {
		var lpErr error
		_, lpErr = lookPathFunc("dbus-daemon")
		if lpErr != nil {
			return fmt.Errorf("find dbus-daemon: %w", lpErr)
		}
		backendCommand, lpErr = resolveBackendCommand(opts.BackendPath)
		if lpErr != nil {
			return lpErr
		}
		usesGnomeKeyringBackend = isGnomeKeyringBackend(opts.BackendPath, backendCommand)
	}

	// Update only the topology fields in the config, preserving everything else.
	if err := updateTopologyConfig(configPath, runtimeDir, mode, backendCommand); err != nil {
		return err
	}
	fmt.Printf("Wrote config: %s\n", configPath)

	// Mask/unmask D-Bus activation based on mode.
	switch mode {
	case "local", "full":
		if err := maskDBusActivation(); err != nil {
			return err
		}
		if usesGnomeKeyringBackend {
			if err := saveGnomeKeyringState(); err != nil {
				return err
			}
			if err := maskPublicGnomeKeyringUnits(); err != nil {
				return err
			}
		}
		stopDBusActivatedService()
	case "remote":
		if err := unmaskDBusActivation(); err != nil {
			return err
		}
		if err := restoreGnomeKeyringState(); err != nil {
			return err
		}
	}

	// Build unit file contents for this mode.
	execStart, err := systemdExecStart(self, "serve", "--config", configPath)
	if err != nil {
		return err
	}
	needed := make(map[string]string)

	switch mode {
	case "remote":
		needed[unitFileName] = fmt.Sprintf(unitTemplate, execStart)
	case "local", "full":
		needed[unitFileName] = fmt.Sprintf(localProxyTemplate, execStart)
	}

	// Write needed units; stop/disable/remove stale ones from a different mode.
	dir, err := unitDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create unit dir: %w", err)
	}

	for _, name := range allUnitNames {
		path := filepath.Join(dir, name)
		if content, ok := needed[name]; ok {
			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				return fmt.Errorf("write %s: %w", name, err)
			}
			fmt.Printf("Wrote unit file: %s\n", path)
		} else {
			// Clean up units from a previous mode if they exist.
			if _, statErr := os.Stat(path); statErr == nil {
				_ = systemctlFunc("stop", name)
				_ = systemctlFunc("disable", name)
				if rmErr := os.Remove(path); rmErr != nil {
					return fmt.Errorf("remove stale %s: %w", name, rmErr)
				}
				fmt.Printf("Removed stale unit: %s\n", path)
			}
		}
	}

	// Reload and enable.
	if err := systemctlFunc("daemon-reload"); err != nil {
		return err
	}
	if err := systemctlFunc("enable", unitFileName); err != nil {
		return err
	}
	fmt.Printf("Enabled %s\n", unitFileName)

	if opts.Start {
		if err := systemctlFunc("start", unitFileName); err != nil {
			return err
		}
		fmt.Printf("Started %s\n", unitFileName)
	}

	return nil
}

// updateTopologyConfig loads the config, sets upstream/downstream for the given mode,
// and writes it back. All other fields are preserved.
func updateTopologyConfig(configPath, runtimeDir, mode, backendCommand string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	socketsDir := filepath.Join(runtimeDir, "secrets-dispatcher", "sockets")

	switch mode {
	case "remote":
		cfg.Serve.Upstream = config.BusConfig{Type: "session_bus"}
		cfg.Serve.BackendCommand = ""
		cfg.Serve.Downstream = []config.BusConfig{
			{Type: "sockets", Path: socketsDir},
		}
	case "local":
		cfg.Serve.Upstream = config.BusConfig{Type: "managed"}
		cfg.Serve.BackendCommand = backendCommand
		cfg.Serve.Downstream = []config.BusConfig{
			{Type: "session_bus"},
		}
	case "full":
		cfg.Serve.Upstream = config.BusConfig{Type: "managed"}
		cfg.Serve.BackendCommand = backendCommand
		cfg.Serve.Downstream = []config.BusConfig{
			{Type: "session_bus"},
			{Type: "sockets", Path: socketsDir},
		}
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	return os.WriteFile(configPath, data, 0644)
}

// Uninstall stops and disables all service units, removes unit files, and reloads systemd.
func Uninstall() error {
	// Stop and disable all known units (ignore errors for missing ones).
	for _, name := range allUnitNames {
		_ = systemctlFunc("stop", name)
		_ = systemctlFunc("disable", name)
	}

	dir, err := unitDir()
	if err != nil {
		return err
	}

	for _, name := range allUnitNames {
		path := filepath.Join(dir, name)
		if err := os.Remove(path); err != nil {
			if !os.IsNotExist(err) {
				return fmt.Errorf("remove %s: %w", name, err)
			}
			continue
		}
		fmt.Printf("Removed %s\n", path)
	}

	if err := unmaskDBusActivation(); err != nil {
		return err
	}

	if err := systemctlFunc("daemon-reload"); err != nil {
		return err
	}

	return restoreGnomeKeyringState()
}

// Status runs systemctl --user status for all installed unit files.
func Status() error {
	dir, err := unitDir()
	if err != nil {
		return err
	}

	var found []string
	for _, name := range allUnitNames {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			found = append(found, name)
		}
	}

	if len(found) == 0 {
		fmt.Println("No secrets-dispatcher unit files found.")
		return nil
	}

	args := append([]string{"--user", "status"}, found...)
	cmd := exec.Command("systemctl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// systemctl status exits non-zero when inactive — not an error for us.
	cmd.Run()
	return nil
}

// systemctlFunc is the function used to run systemctl commands.
// Replaced in tests to avoid requiring a real systemd.
var systemctlFunc = systemctlExec

func systemctlExec(args ...string) error {
	fullArgs := append([]string{"--user"}, args...)
	cmd := exec.Command("systemctl", fullArgs...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("systemctl %s: %w", args[0], err)
	}
	return nil
}

func resolveBackendCommand(backend string) (string, error) {
	switch backend {
	case "":
		path, err := lookPathFunc("gopass-secret-service")
		if err != nil {
			return "", fmt.Errorf("find gopass-secret-service: %w", err)
		}
		return commandLine(path)
	case "gnome-keyring", "gnome-keyring-daemon":
		path, err := lookPathFunc("gnome-keyring-daemon")
		if err != nil {
			return "", fmt.Errorf("find gnome-keyring-daemon: %w", err)
		}
		return commandLine(path, "--foreground", "--components=secrets", "--control-directory=%B")
	default:
		if err := validateUnitLine("backend command", backend); err != nil {
			return "", err
		}
		return backend, nil
	}
}

func commandLine(args ...string) (string, error) {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		q, err := quoteCommandArg(arg)
		if err != nil {
			return "", err
		}
		quoted = append(quoted, q)
	}
	return strings.Join(quoted, " "), nil
}

func quoteCommandArg(arg string) (string, error) {
	if err := validateUnitLine("backend command argument", arg); err != nil {
		return "", err
	}
	if arg != "" && !strings.ContainsAny(arg, " \t\\\"") {
		return arg, nil
	}
	arg = strings.ReplaceAll(arg, `\`, `\\`)
	arg = strings.ReplaceAll(arg, `"`, `\"`)
	return `"` + arg + `"`, nil
}

func isGnomeKeyringBackend(original, resolved string) bool {
	for _, command := range []string{original, resolved} {
		name := firstCommandWord(command)
		if name == "" {
			continue
		}
		if slices.Contains([]string{"gnome-keyring", "gnome-keyring-daemon"}, name) || filepath.Base(name) == "gnome-keyring-daemon" {
			return true
		}
	}
	return false
}

func firstCommandWord(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	if command[0] != '"' {
		return strings.Fields(command)[0]
	}
	var b strings.Builder
	escaped := false
	for _, r := range command[1:] {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if r == '"' {
			return b.String()
		}
		b.WriteRune(r)
	}
	return b.String()
}

func systemdExecStart(args ...string) (string, error) {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		q, err := quoteSystemdArg(arg)
		if err != nil {
			return "", err
		}
		quoted = append(quoted, q)
	}
	return strings.Join(quoted, " "), nil
}

func quoteSystemdArg(arg string) (string, error) {
	if err := validateUnitLine("ExecStart argument", arg); err != nil {
		return "", err
	}
	arg = strings.ReplaceAll(arg, "%", "%%")
	if arg != "" && !strings.ContainsAny(arg, " \t\\\"") {
		return arg, nil
	}
	arg = strings.ReplaceAll(arg, `\`, `\\`)
	arg = strings.ReplaceAll(arg, `"`, `\"`)
	return `"` + arg + `"`, nil
}

func validateUnitLine(name, value string) error {
	if strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("invalid %s: contains a newline", name)
	}
	return nil
}

func gnomeKeyringStatePath() (string, error) {
	configPath := config.DefaultPath()
	if configPath == "" {
		return "", fmt.Errorf("cannot determine config path (is HOME set?)")
	}
	return filepath.Join(filepath.Dir(configPath), gnomeKeyringStateFile), nil
}

func systemctlState(args ...string) string {
	out, _ := systemctlOutputFunc(args...)
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return "unknown"
	}
	return fields[0]
}

func saveGnomeKeyringState() error {
	path, err := gnomeKeyringStatePath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		fmt.Printf("Preserved existing GNOME Keyring state backup: %s\n", path)
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("check GNOME Keyring state backup: %w", err)
	}

	state := gnomeKeyringState{Version: 1}
	for _, unit := range gnomeKeyringPublicUnits {
		state.Units = append(state.Units, gnomeKeyringUnitState{
			Name:    unit,
			Enabled: systemctlState("is-enabled", unit),
			Active:  systemctlState("is-active", unit),
		})
	}

	data, err := yaml.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshal GNOME Keyring state backup: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create GNOME Keyring state dir: %w", err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write GNOME Keyring state backup: %w", err)
	}
	fmt.Printf("Saved GNOME Keyring state: %s\n", path)
	return nil
}

func restoreGnomeKeyringState() error {
	path, err := gnomeKeyringStatePath()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read GNOME Keyring state backup: %w", err)
	}

	var state gnomeKeyringState
	if err := yaml.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("parse GNOME Keyring state backup: %w", err)
	}
	for _, unit := range state.Units {
		if err := restoreGnomeKeyringUnit(unit); err != nil {
			return err
		}
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove GNOME Keyring state backup: %w", err)
	}
	fmt.Printf("Restored GNOME Keyring state from: %s\n", path)
	return nil
}

func restoreGnomeKeyringUnit(unit gnomeKeyringUnitState) error {
	switch unit.Enabled {
	case "masked", "masked-runtime":
		if err := systemctlFunc("mask", unit.Name); err != nil {
			return fmt.Errorf("restore %s masked state: %w", unit.Name, err)
		}
		return nil
	default:
		if err := systemctlFunc("unmask", unit.Name); err != nil {
			return fmt.Errorf("restore %s unmasked state: %w", unit.Name, err)
		}
	}

	switch unit.Enabled {
	case "enabled", "enabled-runtime", "linked", "linked-runtime", "alias":
		if err := systemctlFunc("enable", unit.Name); err != nil {
			return fmt.Errorf("restore %s enabled state: %w", unit.Name, err)
		}
	case "disabled":
		if err := systemctlFunc("disable", unit.Name); err != nil {
			return fmt.Errorf("restore %s disabled state: %w", unit.Name, err)
		}
	}

	switch unit.Active {
	case "active", "activating", "reloading":
		if err := systemctlFunc("start", unit.Name); err != nil {
			return fmt.Errorf("restore %s active state: %w", unit.Name, err)
		}
	case "inactive", "failed", "deactivating":
		if err := systemctlFunc("stop", unit.Name); err != nil {
			return fmt.Errorf("restore %s inactive state: %w", unit.Name, err)
		}
	}
	return nil
}

func maskPublicGnomeKeyringUnits() error {
	if err := systemctlFunc("mask", "--now", "gnome-keyring-daemon.service", "gnome-keyring-daemon.socket"); err != nil {
		return fmt.Errorf("mask public GNOME Keyring units: %w", err)
	}
	fmt.Println("Masked public GNOME Keyring units")
	return nil
}

// dbusServiceDir returns the user D-Bus service directory.
// Uses $XDG_DATA_HOME/dbus-1/services/ with fallback to ~/.local/share/dbus-1/services/.
func dbusServiceDir() (string, error) {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("get home dir: %w", err)
		}
		dataHome = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(dataHome, "dbus-1", "services"), nil
}

func reloadDBus() {
	_, _ = execOutputFunc("busctl", "--user", "call",
		"org.freedesktop.DBus", "/org/freedesktop/DBus",
		"org.freedesktop.DBus", "ReloadConfig")
}

func maskDBusActivation() error {
	dir, err := dbusServiceDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, dbusServiceFile)
	backupPath := path + dbusBackupSuffix

	existing, err := os.ReadFile(path)
	if err == nil && string(existing) != dbusActivationMask {
		if err := os.Rename(path, backupPath); err != nil {
			return fmt.Errorf("backup %s: %w", dbusServiceFile, err)
		}
		fmt.Printf("Backed up %s\n", path)
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create dbus service dir: %w", err)
	}

	if err := os.WriteFile(path, []byte(dbusActivationMask), 0644); err != nil {
		return fmt.Errorf("write dbus mask: %w", err)
	}
	fmt.Printf("Masked D-Bus activation: %s\n", path)

	reloadDBus()
	return nil
}

func unmaskDBusActivation() error {
	dir, err := dbusServiceDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, dbusServiceFile)
	backupPath := path + dbusBackupSuffix

	existing, err := os.ReadFile(path)
	if err != nil {
		return nil // no mask file
	}
	if string(existing) != dbusActivationMask {
		return nil // not our mask
	}

	if _, err := os.Stat(backupPath); err == nil {
		if err := os.Rename(backupPath, path); err != nil {
			return fmt.Errorf("restore %s: %w", dbusServiceFile, err)
		}
		fmt.Printf("Restored %s from backup\n", path)
	} else {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove dbus mask: %w", err)
		}
		fmt.Printf("Removed D-Bus activation mask: %s\n", path)
	}

	reloadDBus()
	return nil
}

func stopDBusActivatedService() {
	// Find the PID of the process currently owning org.freedesktop.secrets on the session bus.
	out, err := execOutputFunc("busctl", "--user", "call",
		"org.freedesktop.DBus", "/org/freedesktop/DBus",
		"org.freedesktop.DBus", "GetConnectionUnixProcessID",
		"s", "org.freedesktop.secrets")
	if err != nil {
		return // name not owned
	}
	// busctl output: "u <pid>\n"
	var pid int
	if _, err := fmt.Sscanf(string(out), "u %d", &pid); err != nil || pid <= 0 {
		return
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	fmt.Printf("Stopping D-Bus activated owner of %s (PID %d)\n", dbusServiceFile, pid)
	_ = p.Signal(syscall.SIGTERM)
}
