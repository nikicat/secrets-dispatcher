// Package service manages the systemd user service for secrets-dispatcher.
package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/nikicat/secrets-dispatcher/internal/config"
	"gopkg.in/yaml.v3"
)

const unitFileName = "secrets-dispatcher.service"

// allUnitNames lists every unit file the package may install.
var allUnitNames = []string{
	"secrets-dispatcher-bus.socket",
	"secrets-dispatcher-bus.service",
	"secrets-dispatcher-backend.service",
	unitFileName,
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

const busSocketTemplate = `[Unit]
Description=Secrets Dispatcher - Private D-Bus for backend

[Socket]
ListenStream=%t/secrets-dispatcher/backend-bus.sock
DirectoryMode=0700

[Install]
WantedBy=sockets.target
`

const busServiceTemplate = `[Unit]
Description=Secrets Dispatcher - Private D-Bus daemon

[Service]
Type=simple
ExecStart=%s --session --nofork --nopidfile --address=systemd:
`

const backendServiceTemplate = `[Unit]
Description=Secrets Dispatcher - Secret Service backend
Requires=secrets-dispatcher-bus.socket
After=secrets-dispatcher-bus.socket

[Service]
Type=simple
ExecStart=%s
Environment=DBUS_SESSION_BUS_ADDRESS=unix:path=%%t/secrets-dispatcher/backend-bus.sock
`

const localProxyTemplate = `[Unit]
Description=Secrets Dispatcher - D-Bus Secret Service approval proxy
Documentation=https://github.com/nikicat/secrets-dispatcher
Requires=secrets-dispatcher-backend.service
After=secrets-dispatcher-backend.service

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
)

// Options configures service installation.
type Options struct {
	// ConfigPath overrides the config file location (default: config.DefaultPath()).
	ConfigPath string
	// Start the service immediately after enabling.
	Start bool
	// Mode selects the topology: "remote", "local", or "full" (default: "remote").
	Mode string
	// BackendPath selects the private backend for local/full modes: a binary
	// path, a keyword ("gopass", "gnome-keyring"), or empty for the
	// detection-driven default. See resolveBackendExec.
	BackendPath string
}

// Verbose gates the per-change narration (Wrote/Enabled/Removed/… lines and
// systemctl's own output) printed by Install, Uninstall, and the trial.
// Milestone lines (provider detection, ownership, ✓ summaries, warnings)
// always print. Set from the CLI's --verbose flag.
var Verbose bool

// verbosef prints per-change narration only when Verbose is set.
func verbosef(format string, args ...any) {
	if Verbose {
		fmt.Printf(format, args...)
	}
}

// lookPathFunc is the function used to find binaries on PATH.
// Replaced in tests.
var lookPathFunc = exec.LookPath

// execOutputFunc runs a command and returns stdout. Replaced in tests.
var execOutputFunc = func(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
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
// reloads systemd, and enables the service. Plan(opts) previews the same
// changes without making them.
func Install(opts Options) error {
	in, err := resolveInstallInputs(opts)
	if err != nil {
		return err
	}

	// Update only the topology fields in the config, preserving everything else.
	if err := updateTopologyConfig(in.configPath, in.runtimeDir, in.mode); err != nil {
		return err
	}
	verbosef("Wrote config: %s\n", in.configPath)

	// Detect the current provider BEFORE taking over — masking and stopping
	// the D-Bus-activated owner below destroy the evidence.
	var provider Provider
	if in.mode != "remote" {
		provider = DetectProvider()
		fmt.Printf("Detected Secret Service provider: %s\n", provider)
	}

	// Mask/unmask D-Bus activation based on mode.
	switch in.mode {
	case "local", "full":
		if err := maskDBusActivation(); err != nil {
			return err
		}
		if provider.Kind == ProviderGnomeKeyring {
			if err := demoteGnomeKeyring(); err != nil {
				return err
			}
		}
		stopDBusActivatedService()
	case "remote":
		if err := unmaskDBusActivation(); err != nil {
			return err
		}
		if err := restoreGnomeKeyring(); err != nil {
			return err
		}
	}

	// Build unit file contents for this mode.
	needed, err := buildUnits(in.mode, in.execStart, provider, opts.BackendPath)
	if err != nil {
		return err
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
			verbosef("Wrote unit file: %s\n", path)
		} else {
			// Clean up units from a previous mode if they exist.
			if _, statErr := os.Stat(path); statErr == nil {
				_ = systemctlFunc("stop", name)
				_ = systemctlFunc("disable", name)
				if rmErr := os.Remove(path); rmErr != nil {
					return fmt.Errorf("remove stale %s: %w", name, rmErr)
				}
				verbosef("Removed stale unit: %s\n", path)
			}
		}
	}

	// Reload and enable.
	if err := systemctlFunc("daemon-reload"); err != nil {
		return err
	}
	if in.mode != "remote" {
		if err := systemctlFunc("enable", "secrets-dispatcher-bus.socket"); err != nil {
			return err
		}
		verbosef("Enabled secrets-dispatcher-bus.socket\n")
	}
	if err := systemctlFunc("enable", unitFileName); err != nil {
		return err
	}
	verbosef("Enabled %s\n", unitFileName)

	if opts.Start {
		if in.mode != "remote" {
			if err := systemctlFunc("start", "secrets-dispatcher-bus.socket"); err != nil {
				return err
			}
			verbosef("Started secrets-dispatcher-bus.socket\n")
		}
		if err := systemctlFunc("start", unitFileName); err != nil {
			return err
		}
		verbosef("Started %s\n", unitFileName)

		if in.mode != "remote" {
			reportOwnership()
		}
	}

	return nil
}

// ownerWaitTimeout bounds the post-start/post-restore ownership confirmation
// polls. Shortened in tests.
var ownerWaitTimeout = 5 * time.Second

// waitForOwner polls until kind owns org.freedesktop.secrets or the timeout
// expires, returning the last-seen provider either way.
func waitForOwner(kind ProviderKind, timeout time.Duration) (Provider, bool) {
	deadline := time.Now().Add(timeout)
	for {
		p := DetectProvider()
		if p.Kind == kind {
			return p, true
		}
		if time.Now().After(deadline) {
			return p, false
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// reportOwnership prints who owns org.freedesktop.secrets after a takeover
// start, warning when the dispatcher did not end up in front (US-11).
func reportOwnership() {
	p, ok := waitForOwner(ProviderDispatcher, ownerWaitTimeout)
	if ok {
		fmt.Printf("org.freedesktop.secrets is now owned by secrets-dispatcher (pid %d)\n", p.PID)
		return
	}
	fmt.Printf("WARNING: org.freedesktop.secrets is owned by %s — secrets-dispatcher is NOT in front\n", p)
}

// updateTopologyConfig loads the config, sets upstream/downstream for the given mode,
// and writes it back. All other fields are preserved.
func updateTopologyConfig(configPath, runtimeDir, mode string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	busSocket := filepath.Join(runtimeDir, "secrets-dispatcher", "backend-bus.sock")
	socketsDir := filepath.Join(runtimeDir, "secrets-dispatcher", "sockets")

	switch mode {
	case "remote":
		cfg.Serve.Upstream = config.BusConfig{Type: "session_bus"}
		cfg.Serve.Downstream = []config.BusConfig{
			{Type: "sockets", Path: socketsDir},
		}
	case "local":
		cfg.Serve.Upstream = config.BusConfig{Type: "socket", Path: busSocket}
		cfg.Serve.Downstream = []config.BusConfig{
			{Type: "session_bus"},
		}
	case "full":
		cfg.Serve.Upstream = config.BusConfig{Type: "socket", Path: busSocket}
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
		verbosef("Removed %s\n", path)
	}

	if err := unmaskDBusActivation(); err != nil {
		return err
	}

	if err := restoreGnomeKeyring(); err != nil {
		return err
	}

	return systemctlFunc("daemon-reload")
}

// systemctlFunc is the function used to run systemctl commands.
// Replaced in tests to avoid requiring a real systemd.
var systemctlFunc = systemctlExec

func systemctlExec(args ...string) error {
	fullArgs := append([]string{"--user"}, args...)
	cmd := exec.Command("systemctl", fullArgs...)
	if Verbose {
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("systemctl %s: %w", args[0], err)
		}
		return nil
	}
	// Quiet mode: swallow systemctl's narration ("Created symlink …"), but a
	// failure must still show everything it said.
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %w\n%s", args[0], err, out)
	}
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
		verbosef("Backed up %s\n", path)
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create dbus service dir: %w", err)
	}

	if err := os.WriteFile(path, []byte(dbusActivationMask), 0644); err != nil {
		return fmt.Errorf("write dbus mask: %w", err)
	}
	verbosef("Masked D-Bus activation: %s\n", path)

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
		verbosef("Restored %s from backup\n", path)
	} else {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove dbus mask: %w", err)
		}
		verbosef("Removed D-Bus activation mask: %s\n", path)
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
	verbosef("Stopping D-Bus activated owner of %s (PID %d)\n", dbusServiceFile, pid)
	_ = p.Signal(syscall.SIGTERM)
}
