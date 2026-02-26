// Package service manages the systemd user service for secrets-dispatcher.
package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

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
WantedBy=default.target
`

const busSocketTemplate = `[Unit]
Description=Secrets Dispatcher - Private D-Bus for backend

[Socket]
ListenStream=%%t/secrets-dispatcher/backend-bus.sock
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
WantedBy=default.target
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
	// BackendPath overrides the backend binary path (for local/full modes).
	BackendPath string
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

	// Update only the topology fields in the config, preserving everything else.
	if err := updateTopologyConfig(configPath, runtimeDir, mode); err != nil {
		return err
	}
	fmt.Printf("Wrote config: %s\n", configPath)

	// Mask/unmask D-Bus activation based on mode.
	switch mode {
	case "local", "full":
		if err := maskDBusActivation(); err != nil {
			return err
		}
		stopDBusActivatedService()
	case "remote":
		if err := unmaskDBusActivation(); err != nil {
			return err
		}
	}

	// Build unit file contents for this mode.
	execStart := self + " serve --config " + configPath
	needed := make(map[string]string)

	switch mode {
	case "remote":
		needed[unitFileName] = fmt.Sprintf(unitTemplate, execStart)
	case "local", "full":
		dbusDaemon, lpErr := lookPathFunc("dbus-daemon")
		if lpErr != nil {
			return fmt.Errorf("find dbus-daemon: %w", lpErr)
		}
		backendPath := opts.BackendPath
		if backendPath == "" {
			backendPath, lpErr = lookPathFunc("gopass-secret-service")
			if lpErr != nil {
				return fmt.Errorf("find gopass-secret-service: %w", lpErr)
			}
		}
		needed["secrets-dispatcher-bus.socket"] = fmt.Sprintf(busSocketTemplate)
		needed["secrets-dispatcher-bus.service"] = fmt.Sprintf(busServiceTemplate, dbusDaemon)
		needed["secrets-dispatcher-backend.service"] = fmt.Sprintf(backendServiceTemplate, backendPath)
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
	if mode != "remote" {
		if err := systemctlFunc("enable", "secrets-dispatcher-bus.socket"); err != nil {
			return err
		}
		fmt.Println("Enabled secrets-dispatcher-bus.socket")
	}
	if err := systemctlFunc("enable", unitFileName); err != nil {
		return err
	}
	fmt.Printf("Enabled %s\n", unitFileName)

	if opts.Start {
		if mode != "remote" {
			if err := systemctlFunc("start", "secrets-dispatcher-bus.socket"); err != nil {
				return err
			}
			fmt.Println("Started secrets-dispatcher-bus.socket")
		}
		if err := systemctlFunc("start", unitFileName); err != nil {
			return err
		}
		fmt.Printf("Started %s\n", unitFileName)
	}

	return nil
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
		fmt.Printf("Removed %s\n", path)
	}

	if err := unmaskDBusActivation(); err != nil {
		return err
	}

	return systemctlFunc("daemon-reload")
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
