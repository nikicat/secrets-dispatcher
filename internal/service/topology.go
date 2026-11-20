package service

import (
	"context"
	"fmt"

	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/nikicat/secrets-dispatcher/internal/systemd"
)

type topology interface {
	// The upstream and downstream that serve runs with.
	serve(config.RuntimeDir) (upstream config.UpstreamConfig, downstream []config.BusConfig)
	// Returns the unit files to install. Errors when a program that a unit runs
	// is not installed.
	units(execStart systemd.Command) (unitFiles, error)
	// Returns what the dry run notes beside the units it writes. Errors when a
	// program that a unit runs is not installed.
	unitNotes() (map[systemd.Unit]string, error)
	// The units to enable and start, in order.
	activated() []systemd.Unit
	// Puts this desktop's Secret Service in place.
	prepare(ctx context.Context, h host) error
	// The changes that prepare would make, in its order.
	planPrepare(takeoverFiles) []Change
	// Prints whether the dispatcher now owns org.freedesktop.secrets, after a
	// start.
	confirm(ctx context.Context)
}

type remote struct{}

func (remote) serve(rd config.RuntimeDir) (config.UpstreamConfig, []config.BusConfig) {
	return config.UpstreamConfig{Bus: config.SessionBus{}},
		[]config.BusConfig{{Bus: config.Sockets{Dir: rd.ClientSockets()}}}
}

func (remote) units(execStart systemd.Command) (unitFiles, error) {
	return unitFiles{proxyServiceUnit: fmt.Sprintf(unitTemplate, execStart.ExecLine())}, nil
}

func (remote) unitNotes() (map[systemd.Unit]string, error) { return nil, nil }

func (remote) activated() []systemd.Unit { return []systemd.Unit{proxyServiceUnit} }

func (remote) prepare(ctx context.Context, h host) error { return h.leaveTakeover(ctx) }

func (remote) planPrepare(f takeoverFiles) []Change {
	// unmaskDBusActivation
	changes := f.mask.planRestore()
	// restoreGnomeKeyring
	var keyring []Change
	for _, file := range f.demotion() {
		keyring = append(keyring, file.planRestore()...)
	}
	if len(keyring) > 0 {
		changes = append(changes, keyring...)
		changes = append(changes, restartKeyringChanges("restart with all components")...)
	}
	return changes
}

func (remote) confirm(context.Context) {}

// Returns the changes that host.restartKeyring makes, as a dry run lists
// them. The note goes on the restart.
func restartKeyringChanges(note string) []Change {
	return []Change{reloadChange(), unitChange(opTryRestart, gkServiceUnit, note)}
}

// The dispatcher sits in front of this desktop's Secret Service, with a private
// backend behind it (ADR 0001).
type takeover struct {
	backend Backend
	// The owner of org.freedesktop.secrets before the takeover.
	provider Provider
}

// Returns the takeover from provider with b as its backend. A nil b means
// gnome-keyring when gnome-keyring is the provider, and gopass otherwise
// (ADR 0009).
func newTakeover(b *Backend, provider Provider) takeover {
	t := takeover{provider: provider}
	switch {
	case b != nil:
		t.backend = *b
	case t.displacesGnomeKeyring():
		t.backend = BackendGnomeKeyring
	default:
		t.backend = BackendGopass
	}
	return t
}

func (t takeover) displacesGnomeKeyring() bool {
	return t.provider.is(ProviderGnomeKeyring)
}

func (takeover) serve(rd config.RuntimeDir) (config.UpstreamConfig, []config.BusConfig) {
	return config.UpstreamConfig{Bus: config.Socket{Path: rd.BackendBusSocket()}},
		[]config.BusConfig{{Bus: config.SessionBus{}}}
}

func (t takeover) units(execStart systemd.Command) (unitFiles, error) {
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
		busServiceUnit:     fmt.Sprintf(busServiceTemplate, systemd.NewCommand(dbusDaemon, "--session", "--nofork", "--nopidfile", "--address=systemd:").ExecLine()),
		backendServiceUnit: fmt.Sprintf(backendServiceTemplate, backendExec.ExecLine(), config.SystemdRuntimeDir.BackendBusSocket().Address()),
		proxyServiceUnit:   fmt.Sprintf(localProxyTemplate, execStart.ExecLine()),
	}, nil
}

// Surfaces the resolved backend, the one choice in the units' content that is
// not obvious.
func (t takeover) unitNotes() (map[systemd.Unit]string, error) {
	backend, err := t.backend.exec()
	if err != nil {
		return nil, err
	}
	return map[systemd.Unit]string{backendServiceUnit: "private backend: " + backend.ExecLine()}, nil
}

func (takeover) activated() []systemd.Unit { return []systemd.Unit{busSocketUnit, proxyServiceUnit} }

func (t takeover) prepare(ctx context.Context, h host) error {
	fmt.Printf("Detected Secret Service provider: %s\n", t.provider)
	if err := h.maskDBusActivation(ctx); err != nil {
		return err
	}
	if t.displacesGnomeKeyring() {
		if err := h.demoteGnomeKeyring(ctx); err != nil {
			return err
		}
	}
	h.stopDBusActivatedService(ctx)
	return nil
}

func (t takeover) planPrepare(f takeoverFiles) []Change {
	// maskDBusActivation
	changes := f.mask.planWrite()

	// demoteGnomeKeyring
	if t.displacesGnomeKeyring() {
		for _, file := range f.demotion() {
			changes = append(changes, file.planWrite()...)
		}
		changes = append(changes, restartKeyringChanges("restart without the secrets component")...)
	}

	// stopDBusActivatedService
	if o := t.provider.displaced(); o != nil {
		changes = append(changes, o.sigtermChange())
	}
	return changes
}

func (takeover) confirm(ctx context.Context) { reportOwnership(ctx) }

type full struct{ takeover }

func (f full) serve(rd config.RuntimeDir) (config.UpstreamConfig, []config.BusConfig) {
	upstream, downstream := f.takeover.serve(rd)
	return upstream, append(downstream, config.BusConfig{Bus: config.Sockets{Dir: rd.ClientSockets()}})
}
