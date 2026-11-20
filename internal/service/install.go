// Package service manages the systemd user service for secrets-dispatcher.
package service

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"syscall"
	"time"

	"github.com/nikicat/secrets-dispatcher/internal/config"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/poll"
	"github.com/nikicat/secrets-dispatcher/internal/systemd"
	"github.com/nikicat/secrets-dispatcher/internal/textfile"
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
ExecStart=%s
`

const backendServiceTemplate = `[Unit]
Description=Secrets Dispatcher - Secret Service backend
Requires=` + string(busSocketUnit) + `
After=` + string(busSocketUnit) + `

[Service]
Type=simple
ExecStart=%s
Environment=DBUS_SESSION_BUS_ADDRESS=%s
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
	dbusServiceFile    = dbustypes.BusName + ".service"
	dbusActivationMask = "[D-BUS Service]\nName=" + dbustypes.BusName + "\nExec=/bin/false\n"
)

type Options struct {
	// The config file that the proxy unit's `serve` command reads. Install
	// rewrites its serve topology and keeps every other setting.
	ConfigPath config.Path
	// Start the service immediately after enabling.
	Start bool
	Mode  Mode
	// A takeover's private backend; nil picks it from the provider.
	Backend *Backend
	// How much of its work Install prints. Plan ignores it.
	Narration Narration
}

// How much of its work the service setup prints. Milestones always print.
type Narration int

const (
	// Prints provider detection, ownership, ✓ summaries and warnings.
	NarrateMilestones Narration = iota
	// Also prints every file and unit change, with the symlinks that enabling
	// and disabling units created or removed.
	NarrateChanges
)

// Prints a per-change line, only under NarrateChanges.
func (n Narration) printf(format string, args ...any) {
	if n == NarrateChanges {
		fmt.Printf(format, args...)
	}
}

// The user's systemd and D-Bus session that the service is installed into.
type host struct {
	// How much of the changes to the session print.
	narration Narration
	units     unitManager
	// The takeover files under the user's XDG homes.
	files takeoverFiles
}

// Returns a host on the user's systemd manager, which the caller closes.
func openHost(ctx context.Context, n Narration) (host, error) {
	files, err := locateTakeoverFiles()
	if err != nil {
		return host{}, err
	}
	units, err := openUnitManager(ctx)
	if err != nil {
		return host{}, err
	}
	return host{narration: n, units: units, files: files}, nil
}

func (h host) close() { h.units.Close() }

func (h host) reload(ctx context.Context) error {
	if err := h.units.Reload(ctx); err != nil {
		return err
	}
	h.narration.printf("Reloaded systemd user units\n")
	return nil
}

func (h host) enable(ctx context.Context, u systemd.Unit) error {
	changes, err := h.units.Unit(u).Enable(ctx)
	if err != nil {
		return err
	}
	h.narrateFileChanges(changes)
	h.narration.printf("Enabled %s\n", u)
	return nil
}

func (h host) start(ctx context.Context, u systemd.Unit) error {
	if err := h.units.Unit(u).Start(ctx); err != nil {
		return err
	}
	h.narration.printf("Started %s\n", u)
	return nil
}

// Stops and disables u. Failures are ignored, since u may not be loaded or
// enabled.
func (h host) retire(ctx context.Context, u systemd.Unit) {
	unit := h.units.Unit(u)
	if unit.Stop(ctx) == nil {
		h.narration.printf("Stopped %s\n", u)
	}
	if changes, err := unit.Disable(ctx); err == nil {
		h.narrateFileChanges(changes)
	}
}

func (h host) narrateFileChanges(changes []unitFileChange) {
	for _, c := range changes {
		h.narration.printf("%s\n", c)
	}
}

// lookPathFunc is the function used to find binaries on PATH.
// Replaced in tests.
var lookPathFunc = exec.LookPath

// Returns the systemd user unit directory, under the XDG config home.
func unitDir() (systemd.UnitDir, error) {
	configHome, err := config.ConfigHome()
	if err != nil {
		return systemd.UnitDir{}, err
	}
	return systemd.UserUnitDir(configHome), nil
}

// Installs and enables the service units for opts.Mode, and starts them when
// opts.Start is set. A local or full install takes org.freedesktop.secrets over
// (ADR 0009). Plan(opts) lists the same changes. ctx bounds each systemd call.
func Install(ctx context.Context, opts Options) error {
	in, err := resolveInstallInputs(ctx, opts)
	if err != nil {
		return err
	}
	h, err := openHost(ctx, opts.Narration)
	if err != nil {
		return err
	}
	defer h.close()

	if err := in.writeTopology(); err != nil {
		return err
	}
	h.narration.printf("Wrote config: %s\n", in.configPath)

	if err := in.topology.prepare(ctx, h); err != nil {
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
	for _, u := range allUnitNames {
		file := dir.UnitFile(u)
		if content, ok := needed[u]; ok {
			if err := file.Write(content); err != nil {
				return fmt.Errorf("write %s: %w", u, err)
			}
			h.narration.printf("Wrote unit file: %s\n", file)
		} else {
			// Clean up units from a previous mode if they exist.
			if file.Exists() {
				h.retire(ctx, u)
				if rmErr := file.Remove(); rmErr != nil {
					return fmt.Errorf("remove stale %s: %w", u, rmErr)
				}
				h.narration.printf("Removed stale unit: %s\n", file)
			}
		}
	}

	// Reload and enable.
	if err := h.reload(ctx); err != nil {
		return err
	}
	for _, u := range in.topology.activated() {
		if err := h.enable(ctx, u); err != nil {
			return err
		}
	}

	if opts.Start {
		for _, u := range in.topology.activated() {
			if err := h.start(ctx, u); err != nil {
				return err
			}
		}
		in.topology.confirm(ctx)
	}

	return nil
}

// ownerWaitTimeout bounds the post-start/post-restore ownership confirmation
// polls. Shortened in tests.
var ownerWaitTimeout = 5 * time.Second

// Returns the Secret Service provider as soon as one of the given kind owns
// org.freedesktop.secrets. Returns the last provider seen when
// ownerWaitTimeout passes or ctx ends first.
func waitForOwner(ctx context.Context, kind ProviderKind) Provider {
	var p Provider
	poll.Until(ctx, ownerWaitTimeout, 250*time.Millisecond, func() bool {
		p = DetectProvider(ctx)
		return p.is(kind)
	})
	return p
}

// Prints who owns org.freedesktop.secrets after a takeover
// start, warning when the dispatcher did not end up in front. Prints nothing
// when ctx ended first.
func reportOwnership(ctx context.Context) {
	p := waitForOwner(ctx, ProviderDispatcher)
	if ctx.Err() != nil {
		return
	}
	if p.is(ProviderDispatcher) {
		fmt.Printf("org.freedesktop.secrets is now owned by secrets-dispatcher (pid %d)\n", p.Owner.PID)
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

// Stops, disables and removes every service unit, and restores the Secret
// Service setup that an install displaced (ADR 0009). ctx bounds each
// systemd call.
func Uninstall(ctx context.Context, n Narration) error {
	h, err := openHost(ctx, n)
	if err != nil {
		return err
	}
	defer h.close()
	for _, u := range allUnitNames {
		h.retire(ctx, u)
	}

	dir, err := unitDir()
	if err != nil {
		return err
	}

	for _, u := range allUnitNames {
		file := dir.UnitFile(u)
		if err := file.Remove(); err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("remove %s: %w", u, err)
			}
			continue
		}
		h.narration.printf("Removed %s\n", file)
	}

	if err := h.leaveTakeover(ctx); err != nil {
		return err
	}

	return h.reload(ctx)
}

// Restarts the proxy unit and waits until it is back, or until ctx ends.
func RestartProxy(ctx context.Context) error {
	units, err := openUnitManager(ctx)
	if err != nil {
		return err
	}
	defer units.Close()
	return units.Unit(proxyServiceUnit).Restart(ctx)
}

// Returns the user D-Bus service directory, under the XDG data home.
func dbusServiceDir() (textfile.Dir, error) {
	dataHome, err := config.DataHome()
	if err != nil {
		return textfile.Dir{}, err
	}
	return textfile.DirAt(dataHome, "dbus-1", "services"), nil
}

// Returns the D-Bus activation mask of org.freedesktop.secrets, in the user
// D-Bus service directory.
func dbusMask() (managedFile, error) {
	dir, err := dbusServiceDir()
	if err != nil {
		return managedFile{}, err
	}
	return managedFile{
		file:    dir.File(dbusServiceFile),
		name:    "D-Bus activation mask",
		purpose: "mask D-Bus activation of org.freedesktop.secrets",
		content: dbusActivationMask,
	}, nil
}

func (h host) maskDBusActivation(ctx context.Context) error {
	if err := h.files.mask.write(h.narration); err != nil {
		return err
	}
	_ = session.reloadConfig(ctx)
	return nil
}

func (h host) unmaskDBusActivation(ctx context.Context) error {
	restored, err := h.files.mask.restore(h.narration)
	if err != nil || !restored {
		return err
	}
	_ = session.reloadConfig(ctx)
	return nil
}

// Gives org.freedesktop.secrets back to the desktop's own provider. Does
// nothing to what no takeover changed.
func (h host) leaveTakeover(ctx context.Context) error {
	if err := h.unmaskDBusActivation(ctx); err != nil {
		return err
	}
	return h.restoreGnomeKeyring(ctx)
}

func (h host) stopDBusActivatedService(ctx context.Context) {
	pid, err := session.secretsOwner(ctx)
	if err != nil {
		return // name not owned
	}
	h.narration.printf("Stopping D-Bus activated owner of %s (PID %d)\n", dbusServiceFile, pid)
	_ = pid.Signal(syscall.SIGTERM)
}
