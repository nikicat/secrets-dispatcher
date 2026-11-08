// Package service manages the systemd user service for secrets-dispatcher.
package service

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/nikicat/secrets-dispatcher/internal/config"
	"gopkg.in/yaml.v3"
)

const (
	proxyServiceUnit   = "secrets-dispatcher.service"
	busSocketUnit      = "secrets-dispatcher-bus.socket"
	busServiceUnit     = "secrets-dispatcher-bus.service"
	backendServiceUnit = "secrets-dispatcher-backend.service"
)

// allUnitNames lists every unit file the package may install.
var allUnitNames = []string{
	busSocketUnit,
	busServiceUnit,
	backendServiceUnit,
	proxyServiceUnit,
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
ListenStream=%s
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
Requires=` + busSocketUnit + `
After=` + busSocketUnit + `

[Service]
Type=simple
ExecStart=%s
Environment=DBUS_SESSION_BUS_ADDRESS=unix:path=%s
`

const localProxyTemplate = `[Unit]
Description=Secrets Dispatcher - D-Bus Secret Service approval proxy
Documentation=https://github.com/nikicat/secrets-dispatcher
Requires=` + backendServiceUnit + `
After=` + backendServiceUnit + `

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
	// The topology: remote, local or full. Empty means remote.
	Mode string
	// BackendPath selects the private backend for local/full modes: a binary
	// path, a keyword ("gopass", "gnome-keyring"), or empty for the
	// detection-driven default. See resolveBackendExec.
	BackendPath string
	// How much of its work Install prints. Plan ignores it.
	Narration Narration
}

// How much of its work the service setup prints. Milestones always print.
type Narration int

const (
	// Prints provider detection, ownership, ✓ summaries and warnings.
	NarrateMilestones Narration = iota
	// Also prints every file and unit change and systemctl's own output.
	NarrateChanges
)

// Returns "true" under NarrateChanges, "false" otherwise, as a bool flag shows.
func (n Narration) String() string {
	return strconv.FormatBool(n.changes())
}

// Parses a bool flag value: true selects NarrateChanges, false
// NarrateMilestones. Errors when s is not a bool.
func (n *Narration) Set(s string) error {
	changes, err := strconv.ParseBool(s)
	if err != nil {
		return err
	}
	*n = NarrateMilestones
	if changes {
		*n = NarrateChanges
	}
	return nil
}

// Reports true, so the flag works without a value.
func (*Narration) IsBoolFlag() bool { return true }

// Reports whether every change prints, not only the milestones.
func (n Narration) changes() bool {
	return n == NarrateChanges
}

// Prints a per-change line, only under NarrateChanges.
func (n Narration) printf(format string, args ...any) {
	if n.changes() {
		fmt.Printf(format, args...)
	}
}

// The user's systemd and D-Bus session that the service is installed into.
type host struct {
	// How much of the changes to the session print.
	narration Narration
}

// Runs `systemctl --user` with args.
func (h host) systemctl(args ...string) error {
	return systemctlFunc(h.narration, args...)
}

// lookPathFunc is the function used to find binaries on PATH.
// Replaced in tests.
var lookPathFunc = exec.LookPath

// execOutputFunc runs a command and returns stdout. Replaced in tests.
var execOutputFunc = func(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).Output()
}

// Returns the systemd user unit directory, under the XDG config home.
func unitDir() (string, error) {
	configHome, err := config.ConfigHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(configHome, "systemd", "user"), nil
}

// UnitPath returns the full path where the proxy unit file is (or would be) installed.
func UnitPath() (string, error) {
	dir, err := unitDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, proxyServiceUnit), nil
}

// Install writes systemd user unit files, updates the config topology,
// reloads systemd, and enables the service. Plan(opts) previews the same
// changes without making them.
func Install(opts Options) error {
	in, err := resolveInstallInputs(opts)
	if err != nil {
		return err
	}
	h := host{narration: opts.Narration}

	if err := in.writeTopology(); err != nil {
		return err
	}
	h.narration.printf("Wrote config: %s\n", in.configPath)

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
		if err := h.maskDBusActivation(); err != nil {
			return err
		}
		if provider.Kind == ProviderGnomeKeyring {
			if err := h.demoteGnomeKeyring(); err != nil {
				return err
			}
		}
		h.stopDBusActivatedService()
	case "remote":
		if err := h.unmaskDBusActivation(); err != nil {
			return err
		}
		if err := h.restoreGnomeKeyring(); err != nil {
			return err
		}
	}

	// Build unit file contents for this mode.
	needed, err := in.units(provider)
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
			h.narration.printf("Wrote unit file: %s\n", path)
		} else {
			// Clean up units from a previous mode if they exist.
			if _, statErr := os.Stat(path); statErr == nil {
				_ = h.systemctl("stop", name)
				_ = h.systemctl("disable", name)
				if rmErr := os.Remove(path); rmErr != nil {
					return fmt.Errorf("remove stale %s: %w", name, rmErr)
				}
				h.narration.printf("Removed stale unit: %s\n", path)
			}
		}
	}

	// Reload and enable.
	if err := h.systemctl("daemon-reload"); err != nil {
		return err
	}
	if in.mode != "remote" {
		if err := h.systemctl("enable", busSocketUnit); err != nil {
			return err
		}
		h.narration.printf("Enabled %s\n", busSocketUnit)
	}
	if err := h.systemctl("enable", proxyServiceUnit); err != nil {
		return err
	}
	h.narration.printf("Enabled %s\n", proxyServiceUnit)

	if opts.Start {
		if in.mode != "remote" {
			if err := h.systemctl("start", busSocketUnit); err != nil {
				return err
			}
			h.narration.printf("Started %s\n", busSocketUnit)
		}
		if err := h.systemctl("start", proxyServiceUnit); err != nil {
			return err
		}
		h.narration.printf("Started %s\n", proxyServiceUnit)

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

// Sets the serve upstream and downstream in the config file at in.configPath
// to the topology of in.mode. Every other setting in the file is kept.
func (in installInputs) writeTopology() error {
	// A missing file is the first install, which writes a fresh config.
	cfg, err := config.Load(in.configPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		cfg = &config.Config{}
	case err != nil:
		return fmt.Errorf("load config: %w", err)
	}

	busSocket := in.runtimeDir.BackendBusSocket()
	socketsDir := in.runtimeDir.ClientSockets()

	switch in.mode {
	case "remote":
		cfg.Serve.Upstream = config.BusConfig{Type: config.BusSessionBus}
		cfg.Serve.Downstream = []config.BusConfig{
			{Type: config.BusSockets, Path: socketsDir},
		}
	case "local":
		cfg.Serve.Upstream = config.BusConfig{Type: config.BusSocket, Path: busSocket}
		cfg.Serve.Downstream = []config.BusConfig{
			{Type: config.BusSessionBus},
		}
	case "full":
		cfg.Serve.Upstream = config.BusConfig{Type: config.BusSocket, Path: busSocket}
		cfg.Serve.Downstream = []config.BusConfig{
			{Type: config.BusSessionBus},
			{Type: config.BusSockets, Path: socketsDir},
		}
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(in.configPath), 0755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	return os.WriteFile(in.configPath, data, 0644)
}

// Uninstall stops and disables all service units, removes unit files, and reloads systemd.
func Uninstall(n Narration) error {
	h := host{narration: n}
	// Stop and disable all known units (ignore errors for missing ones).
	for _, name := range allUnitNames {
		_ = h.systemctl("stop", name)
		_ = h.systemctl("disable", name)
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
		h.narration.printf("Removed %s\n", path)
	}

	if err := h.unmaskDBusActivation(); err != nil {
		return err
	}

	if err := h.restoreGnomeKeyring(); err != nil {
		return err
	}

	return h.systemctl("daemon-reload")
}

// systemctlFunc is the function used to run systemctl commands.
// Replaced in tests to avoid requiring a real systemd.
var systemctlFunc = systemctlExec

func systemctlExec(n Narration, args ...string) error {
	fullArgs := append([]string{"--user"}, args...)
	cmd := exec.Command("systemctl", fullArgs...)
	if n.changes() {
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

// Returns the user D-Bus service directory, under the XDG data home.
func dbusServiceDir() (string, error) {
	dataHome, err := config.DataHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(dataHome, "dbus-1", "services"), nil
}

func reloadDBus() {
	_, _ = execOutputFunc("busctl", "--user", "call",
		"org.freedesktop.DBus", "/org/freedesktop/DBus",
		"org.freedesktop.DBus", "ReloadConfig")
}

func (h host) maskDBusActivation() error {
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
		h.narration.printf("Backed up %s\n", path)
	}

	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create dbus service dir: %w", err)
	}

	if err := os.WriteFile(path, []byte(dbusActivationMask), 0644); err != nil {
		return fmt.Errorf("write dbus mask: %w", err)
	}
	h.narration.printf("Masked D-Bus activation: %s\n", path)

	reloadDBus()
	return nil
}

func (h host) unmaskDBusActivation() error {
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
		h.narration.printf("Restored %s from backup\n", path)
	} else {
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("remove dbus mask: %w", err)
		}
		h.narration.printf("Removed D-Bus activation mask: %s\n", path)
	}

	reloadDBus()
	return nil
}

func (h host) stopDBusActivatedService() {
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
	h.narration.printf("Stopping D-Bus activated owner of %s (PID %d)\n", dbusServiceFile, pid)
	_ = p.Signal(syscall.SIGTERM)
}
