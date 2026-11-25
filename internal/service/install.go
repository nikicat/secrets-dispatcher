// Package service manages the systemd user service for secrets-dispatcher.
package service

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
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

// The note is what the dry run prints beside writing the unit file.
type plannedUnit struct{ content, note string }

// The unit files to install, by unit.
type unitFiles map[systemd.Unit]plannedUnit

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
`

// Returns the backend unit that runs cmd with the assignments in env.
func backendServiceContent(cmd systemd.Command, env []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, backendServiceTemplate, cmd.ExecLine())
	for _, v := range env {
		fmt.Fprintf(&b, "Environment=%s\n", v)
	}
	return b.String()
}

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
	dbusActivationMask = "[D-BUS Service]\nName=" + dbustypes.BusName + "\nExec=/bin/false\nSystemdService=" + string(proxyServiceUnit) + "\n"
	// The mask that earlier versions of the dispatcher installed. It has no
	// SystemdService line.
	formerDBusActivationMask = "[D-BUS Service]\nName=" + dbustypes.BusName + "\nExec=/bin/false\n"
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
}

// Returns a host on the user's systemd manager, which the caller closes.
func openHost(ctx context.Context, n Narration) (host, error) {
	units, err := openUnitManager(ctx)
	if err != nil {
		return host{}, err
	}
	return host{narration: n, units: units}, nil
}

func (h host) close() { h.units.Close() }

// Makes each step's change in order. Errors with the first that fails.
func (h host) run(ctx context.Context, steps []step) error {
	for _, s := range steps {
		if err := s.apply(ctx, h); err != nil {
			return err
		}
	}
	return nil
}

func (h host) narrateFileChanges(changes []unitFileChange) {
	for _, c := range changes {
		h.narration.printf("%s\n", c)
	}
}

// lookPathFunc is the function used to find binaries on PATH.
// Replaced in tests.
var lookPathFunc = exec.LookPath

// Installs and enables the units of opts.Mode, and starts them when opts.Start
// is set. A local or full install takes org.freedesktop.secrets over (ADR
// 0001). It makes the changes that Plan lists (ARCHITECTURE.md, "Service setup").
func Install(ctx context.Context, opts Options) error {
	l, err := locateLayout()
	if err != nil {
		return err
	}
	p, err := l.planInstall(ctx, opts)
	if err != nil {
		return err
	}
	return p.run(ctx, opts.Narration)
}

func (p installPlan) run(ctx context.Context, n Narration) error {
	h, err := openHost(ctx, n)
	if err != nil {
		return err
	}
	defer h.close()

	p.topology.announce()
	if err := h.run(ctx, p.steps); err != nil {
		return err
	}
	if p.start {
		p.topology.confirm(ctx)
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

// Returns the step that writes the topology's serve upstream and downstream
// into the config file.
func (in installInputs) writeTopologyStep() step {
	note := "set serve topology: " + in.mode.String() + " (other settings preserved)"
	return step{Change{changeWrite, in.configPath.String(), note}, func(_ context.Context, h host) error {
		cfg, err := in.configPath.LoadOrEmpty()
		if err != nil {
			return fmt.Errorf("load config: %w", err)
		}
		cfg.Serve.Upstream, cfg.Serve.Downstream = in.topology.serve(in.runtimeDir)
		if err := in.configPath.Save(cfg); err != nil {
			return err
		}
		h.narration.printf("Wrote config: %s\n", in.configPath)
		return nil
	}}
}

// Stops, disables and removes every service unit, and restores the Secret
// Service setup that an install displaced (ADR 0009).
func Uninstall(ctx context.Context, n Narration) error {
	l, err := locateLayout()
	if err != nil {
		return err
	}
	return l.uninstall(ctx, n)
}

func (l layout) uninstall(ctx context.Context, n Narration) error {
	steps := l.uninstallSteps()
	h, err := openHost(ctx, n)
	if err != nil {
		return err
	}
	defer h.close()
	return h.run(ctx, steps)
}

// Returns the steps of an uninstall from l, in order. The mask is removed before
// any unit stops, because while it is in place the next call starts the proxy
// again (ADR 0001).
func (l layout) uninstallSteps() []step {
	steps := l.files.unmaskSteps()
	for _, u := range allUnitNames {
		steps = append(steps, unitStep(opStop, u, ""), unitStep(opDisable, u, ""))
	}
	for _, u := range l.installed() {
		steps = append(steps, l.unitFile(u, "").removeStep())
	}
	steps = append(steps, l.files.restoreKeyringSteps()...)
	return append(steps, reloadStep())
}

// Restarts the proxy unit and waits until it is back, or until ctx ends.
func RestartProxy(ctx context.Context) error {
	h, err := openHost(ctx, NarrateMilestones)
	if err != nil {
		return err
	}
	defer h.close()
	return h.apply(ctx, opRestart, proxyServiceUnit)
}

// Returns the D-Bus activation mask of org.freedesktop.secrets in dir. The mask
// starts the dispatcher's own unit and no other provider (ADR 0001).
func dbusMask(dir textfile.Dir) managedFile {
	return managedFile{
		file:    dir.File(dbusServiceFile),
		name:    "D-Bus activation mask",
		purpose: "mask D-Bus activation of org.freedesktop.secrets",
		content: dbusActivationMask,
		former:  []string{formerDBusActivationMask},
	}
}

// Returns the step that has the session bus reread its service files, so that
// a written or removed activation mask applies. A failure is ignored.
func reloadBusConfigStep() step {
	return step{Change{changeRun, reloadConfigCommand, "reread the D-Bus activation files"}, func(ctx context.Context, _ host) error {
		_ = session.reloadConfig(ctx)
		return nil
	}}
}

// Returns the steps that write the D-Bus activation mask and have the session
// bus reread its service files.
func (f takeoverFiles) maskSteps() []step {
	return append(f.mask.writeSteps(), reloadBusConfigStep())
}

// Returns the steps that take the D-Bus activation mask away and have the session
// bus reread its service files. A file that the mask replaced comes back. Returns
// none when the mask is not ours.
func (f takeoverFiles) unmaskSteps() []step {
	steps := f.mask.restoreSteps()
	if len(steps) == 0 {
		return nil
	}
	return append(steps, reloadBusConfigStep())
}

// Returns the steps that give org.freedesktop.secrets back to the desktop's own
// provider (ADR 0009). The steps leave alone what no takeover changed.
func (f takeoverFiles) leaveSteps() []step {
	return append(f.unmaskSteps(), f.restoreKeyringSteps()...)
}
