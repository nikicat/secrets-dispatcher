// Package service manages the systemd user service for secrets-dispatcher.
package service

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/nikicat/secrets-dispatcher/internal/poll"
	"github.com/nikicat/secrets-dispatcher/internal/systemd"
)

const (
	proxyServiceUnit   systemd.Unit = "secrets-dispatcher.service"
	busSocketUnit      systemd.Unit = "secrets-dispatcher-bus.socket"
	busServiceUnit     systemd.Unit = "secrets-dispatcher-bus.service"
	backendServiceUnit systemd.Unit = "secrets-dispatcher-backend.service"
)

// The content of each unit file, by unit.
type unitFiles map[systemd.Unit]string

// allUnitNames lists every unit file the package may install.
var allUnitNames = []systemd.Unit{
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
Requires=` + string(busSocketUnit) + `
After=` + string(busSocketUnit) + `

[Service]
Type=simple
ExecStart=%s
Environment=DBUS_SESSION_BUS_ADDRESS=unix:path=%s
`

const localProxyTemplate = `[Unit]
Description=Secrets Dispatcher - D-Bus Secret Service approval proxy
Documentation=https://github.com/nikicat/secrets-dispatcher
Requires=` + string(backendServiceUnit) + `
After=` + string(backendServiceUnit) + `

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
	// The config file that the proxy unit's `serve` command reads. Install
	// rewrites its serve topology and keeps every other setting.
	ConfigPath config.Path
	// Start the service immediately after enabling.
	Start   bool
	Mode    Mode
	Backend Backend
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

// A systemctl command that the service package runs.
type systemctlVerb string

const (
	verbStop         systemctlVerb = "stop"
	verbDisable      systemctlVerb = "disable"
	verbEnable       systemctlVerb = "enable"
	verbStart        systemctlVerb = "start"
	verbTryRestart   systemctlVerb = "try-restart"
	verbRestart      systemctlVerb = "restart"
	verbDaemonReload systemctlVerb = "daemon-reload"
	verbIsActive     systemctlVerb = "is-active"
)

// Runs `systemctl --user verb u`.
func (h host) systemctlUnit(verb systemctlVerb, u systemd.Unit) error {
	return h.systemctl(string(verb), string(u))
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

	if err := in.topology.prepare(h); err != nil {
		return err
	}

	needed, err := in.topology.units(in.execStart)
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

	for _, u := range allUnitNames {
		path := u.Path(dir)
		if content, ok := needed[u]; ok {
			if err := os.WriteFile(path, []byte(content), 0644); err != nil {
				return fmt.Errorf("write %s: %w", u, err)
			}
			h.narration.printf("Wrote unit file: %s\n", path)
		} else {
			// Clean up units from a previous mode if they exist.
			if _, statErr := os.Stat(path); statErr == nil {
				_ = h.systemctlUnit(verbStop, u)
				_ = h.systemctlUnit(verbDisable, u)
				if rmErr := os.Remove(path); rmErr != nil {
					return fmt.Errorf("remove stale %s: %w", u, rmErr)
				}
				h.narration.printf("Removed stale unit: %s\n", path)
			}
		}
	}

	// Reload and enable.
	if err := h.systemctl(string(verbDaemonReload)); err != nil {
		return err
	}
	for _, u := range in.topology.activated() {
		if err := h.systemctlUnit(verbEnable, u); err != nil {
			return err
		}
		h.narration.printf("Enabled %s\n", u)
	}

	if opts.Start {
		for _, u := range in.topology.activated() {
			if err := h.systemctlUnit(verbStart, u); err != nil {
				return err
			}
			h.narration.printf("Started %s\n", u)
		}
		in.topology.confirm()
	}

	return nil
}

// ownerWaitTimeout bounds the post-start/post-restore ownership confirmation
// polls. Shortened in tests.
var ownerWaitTimeout = 5 * time.Second

// waitForOwner polls until kind owns org.freedesktop.secrets or the timeout
// expires, returning the last-seen provider either way.
func waitForOwner(kind ProviderKind, timeout time.Duration) (Provider, bool) {
	var p Provider
	ok := poll.Until(timeout, 250*time.Millisecond, func() bool {
		p = DetectProvider()
		return p.Kind == kind
	})
	return p, ok
}

// reportOwnership prints who owns org.freedesktop.secrets after a takeover
// start, warning when the dispatcher did not end up in front.
func reportOwnership() {
	p, ok := waitForOwner(ProviderDispatcher, ownerWaitTimeout)
	if ok {
		fmt.Printf("org.freedesktop.secrets is now owned by secrets-dispatcher (pid %d)\n", p.PID)
		return
	}
	fmt.Printf("WARNING: org.freedesktop.secrets is owned by %s — secrets-dispatcher is NOT in front\n", p)
}

// Sets the serve upstream and downstream in the config file at in.configPath
// to in.topology. Every other setting in the file is kept.
func (in installInputs) writeTopology() error {
	cfg, err := in.configPath.LoadOrEmpty()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	cfg.Serve.Upstream, cfg.Serve.Downstream = in.topology.serve(in.runtimeDir)
	return in.configPath.Save(cfg)
}

// Uninstall stops and disables all service units, removes unit files, and reloads systemd.
func Uninstall(n Narration) error {
	h := host{narration: n}
	// Stop and disable all known units (ignore errors for missing ones).
	for _, u := range allUnitNames {
		_ = h.systemctlUnit(verbStop, u)
		_ = h.systemctlUnit(verbDisable, u)
	}

	dir, err := unitDir()
	if err != nil {
		return err
	}

	for _, u := range allUnitNames {
		path := u.Path(dir)
		if err := os.Remove(path); err != nil {
			if !os.IsNotExist(err) {
				return fmt.Errorf("remove %s: %w", u, err)
			}
			continue
		}
		h.narration.printf("Removed %s\n", path)
	}

	if err := h.leaveTakeover(); err != nil {
		return err
	}

	return h.systemctl(string(verbDaemonReload))
}

// Restarts the proxy unit, with systemctl's output on the terminal.
func RestartProxy() error {
	return host{narration: NarrateChanges}.systemctlUnit(verbRestart, proxyServiceUnit)
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

// Gives org.freedesktop.secrets back to the desktop's own provider. Does
// nothing to what no takeover changed.
func (h host) leaveTakeover() error {
	if err := h.unmaskDBusActivation(); err != nil {
		return err
	}
	return h.restoreGnomeKeyring()
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
