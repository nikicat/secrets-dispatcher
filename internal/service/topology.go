package service

import (
	"fmt"
	"os"

	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/nikicat/secrets-dispatcher/internal/systemd"
)

type topology interface {
	// The upstream and downstream that serve runs with.
	serve(config.RuntimeDir) (upstream config.BusConfig, downstream []config.BusConfig)
	// Returns the unit files to install. Errors when a program that a unit runs
	// is not installed.
	units(execStart string) (unitFiles, error)
	// The units to enable and start, in order.
	activated() []systemd.Unit
	// Puts this desktop's Secret Service in place.
	prepare(host) error
	// The changes that prepare would make, in its order.
	planPrepare(takeoverFiles) []Change
	// Reports whether the dispatcher now owns org.freedesktop.secrets.
	confirm()
}

// The files that entering takeover writes and leaving it removes.
type takeoverFiles struct {
	mask, dropIn, shadow string
}

type remote struct{}

func (remote) serve(rd config.RuntimeDir) (config.BusConfig, []config.BusConfig) {
	return config.BusConfig{Type: config.BusSessionBus},
		[]config.BusConfig{{Type: config.BusSockets, Path: rd.ClientSockets()}}
}

func (remote) units(execStart string) (unitFiles, error) {
	return unitFiles{proxyServiceUnit: fmt.Sprintf(unitTemplate, execStart)}, nil
}

func (remote) activated() []systemd.Unit { return []systemd.Unit{proxyServiceUnit} }

func (remote) prepare(h host) error { return h.leaveTakeover() }

func (remote) planPrepare(f takeoverFiles) []Change {
	var changes []Change
	// unmaskDBusActivation
	if existing, err := os.ReadFile(f.mask); err == nil && string(existing) == dbusActivationMask {
		if _, err := os.Stat(f.mask + dbusBackupSuffix); err == nil {
			changes = append(changes, Change{"restore", f.mask + dbusBackupSuffix, "-> " + f.mask})
		} else {
			changes = append(changes, Change{"remove", f.mask, "D-Bus activation mask"})
		}
	}
	// restoreGnomeKeyring
	restored := false
	if _, err := os.Stat(f.dropIn); err == nil {
		changes = append(changes, Change{"remove", f.dropIn, "gnome-keyring demotion drop-in"})
		restored = true
	}
	if existing, err := os.ReadFile(f.shadow); err == nil && string(existing) == gkAutostartShadow {
		changes = append(changes, Change{"remove", f.shadow, "autostart shadow"})
		if _, err := os.Stat(f.shadow + dbusBackupSuffix); err == nil {
			changes = append(changes, Change{"restore", f.shadow + dbusBackupSuffix, "-> " + f.shadow})
		}
		restored = true
	}
	if restored {
		changes = append(changes,
			daemonReloadChange(),
			systemctlChange(verbTryRestart, gkServiceUnit, "restart with all components"))
	}
	return changes
}

func (remote) confirm() {}

// The dispatcher sits in front of this desktop's Secret Service, with a private
// backend behind it (ADR 0001).
type takeover struct {
	// Never empty.
	backend Backend
	// The owner of org.freedesktop.secrets before the takeover.
	provider Provider
}

// An empty b means gnome-keyring when gnome-keyring is the provider, and
// gopass otherwise (ADR 0009).
func newTakeover(b Backend, provider Provider) takeover {
	t := takeover{backend: b, provider: provider}
	if t.backend == "" {
		t.backend = BackendGopass
		if t.displacesGnomeKeyring() {
			t.backend = BackendGnomeKeyring
		}
	}
	return t
}

func (t takeover) displacesGnomeKeyring() bool {
	return t.provider.Kind == ProviderGnomeKeyring
}

func (takeover) serve(rd config.RuntimeDir) (config.BusConfig, []config.BusConfig) {
	return config.BusConfig{Type: config.BusSocket, Path: rd.BackendBusSocket()},
		[]config.BusConfig{{Type: config.BusSessionBus}}
}

func (t takeover) units(execStart string) (unitFiles, error) {
	dbusDaemon, err := lookPathFunc("dbus-daemon")
	if err != nil {
		return nil, fmt.Errorf("find dbus-daemon: %w", err)
	}
	backendExec, err := t.backend.exec()
	if err != nil {
		return nil, err
	}
	return unitFiles{
		busSocketUnit:      fmt.Sprintf(busSocketTemplate, config.SystemdRuntimeDir.BackendBusSocket()),
		busServiceUnit:     fmt.Sprintf(busServiceTemplate, dbusDaemon),
		backendServiceUnit: fmt.Sprintf(backendServiceTemplate, backendExec, config.SystemdRuntimeDir.BackendBusSocket()),
		proxyServiceUnit:   fmt.Sprintf(localProxyTemplate, execStart),
	}, nil
}

func (takeover) activated() []systemd.Unit { return []systemd.Unit{busSocketUnit, proxyServiceUnit} }

func (t takeover) prepare(h host) error {
	fmt.Printf("Detected Secret Service provider: %s\n", t.provider)
	if err := h.maskDBusActivation(); err != nil {
		return err
	}
	if t.displacesGnomeKeyring() {
		if err := h.demoteGnomeKeyring(); err != nil {
			return err
		}
	}
	h.stopDBusActivatedService()
	return nil
}

func (t takeover) planPrepare(f takeoverFiles) []Change {
	var changes []Change
	// maskDBusActivation
	if existing, err := os.ReadFile(f.mask); err == nil && string(existing) != dbusActivationMask {
		changes = append(changes, Change{"backup", f.mask, "-> " + f.mask + dbusBackupSuffix})
	}
	changes = append(changes, Change{"write", f.mask, "mask D-Bus activation of org.freedesktop.secrets"})

	// demoteGnomeKeyring
	if t.displacesGnomeKeyring() {
		changes = append(changes, Change{"write", f.dropIn, "demote gnome-keyring to pkcs11-only (secrets move to the private backend)"})
		if existing, err := os.ReadFile(f.shadow); err == nil && string(existing) != gkAutostartShadow {
			changes = append(changes, Change{"backup", f.shadow, "-> " + f.shadow + dbusBackupSuffix})
		}
		changes = append(changes, Change{"write", f.shadow, "hide the gnome-keyring secrets autostart kicker (Hidden=true)"})
		changes = append(changes,
			daemonReloadChange(),
			systemctlChange(verbTryRestart, gkServiceUnit, "restart without the secrets component"))
	}

	// stopDBusActivatedService
	if t.provider.PID > 0 && t.provider.Kind != ProviderDispatcher {
		changes = append(changes, Change{"signal", fmt.Sprintf("SIGTERM -> pid %d (%s)", t.provider.PID, t.provider.Kind), "current owner of org.freedesktop.secrets"})
	}
	return changes
}

func (takeover) confirm() { reportOwnership() }

type full struct{ takeover }

func (f full) serve(rd config.RuntimeDir) (config.BusConfig, []config.BusConfig) {
	upstream, downstream := f.takeover.serve(rd)
	return upstream, append(downstream, config.BusConfig{Type: config.BusSockets, Path: rd.ClientSockets()})
}
