package service

import (
	"context"
	"fmt"

	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/nikicat/secrets-dispatcher/internal/systemd"
)

type topology interface {
	// The buses that the serve command runs with.
	serve(config.RuntimeDir) config.TopologyConfig
	// Returns the unit files to install. Errors when a program that a unit runs
	// is not installed.
	units(execStart systemd.Command) (unitFiles, error)
	// The units to enable and start, in order.
	activated() []systemd.Unit
	// Prints the detected Secret Service provider when the topology takes
	// org.freedesktop.secrets over. Prints nothing otherwise.
	announce()
	// Returns the steps that put this desktop's Secret Service in place.
	// Errors when a program that a step runs is not installed.
	prepare(takeoverFiles) ([]step, error)
	// Prints whether the dispatcher now owns org.freedesktop.secrets, after a
	// start.
	confirm(ctx context.Context)
}

type remote struct{}

func (remote) serve(rd config.RuntimeDir) config.TopologyConfig {
	return config.TopologyConfig{Upstream: config.SessionUpstream(), Downstream: []config.BusConfig{rd.ClientsDownstream()}}
}

func (remote) units(execStart systemd.Command) (unitFiles, error) {
	return unitFiles{proxyServiceUnit: {content: fmt.Sprintf(unitTemplate, execStart.ExecLine())}}, nil
}

func (remote) activated() []systemd.Unit { return []systemd.Unit{proxyServiceUnit} }

func (remote) announce() {}

func (remote) prepare(f takeoverFiles) ([]step, error) { return f.leaveSteps(), nil }

func (remote) confirm(context.Context) {}

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

func (takeover) serve(rd config.RuntimeDir) config.TopologyConfig {
	return config.TopologyConfig{
		Upstream:   config.UpstreamConfig{Bus: config.Socket{Path: rd.BackendBusSocket()}},
		Downstream: []config.BusConfig{{Bus: config.SessionBus{}}},
	}
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
		busSocketUnit:  {content: fmt.Sprintf(busSocketTemplate, backendBus)},
		busServiceUnit: {content: fmt.Sprintf(busServiceTemplate, systemd.NewCommand(dbusDaemon, "--session", "--nofork", "--nopidfile", "--address=systemd:").ExecLine())},
		backendServiceUnit: {
			content: backendServiceContent(backendExec, t.backend.environment()),
			note:    "private backend: " + backendExec.ExecLine(),
		},
		proxyServiceUnit: {content: fmt.Sprintf(localProxyTemplate, execStart.ExecLine())},
	}, nil
}

func (takeover) activated() []systemd.Unit { return []systemd.Unit{busSocketUnit, proxyServiceUnit} }

func (t takeover) announce() { fmt.Printf("Detected Secret Service provider: %s\n", t.provider) }

func (t takeover) prepare(f takeoverFiles) ([]step, error) {
	steps := f.maskSteps()
	if t.displacesGnomeKeyring() {
		demotion, err := f.demoteSteps()
		if err != nil {
			return nil, err
		}
		steps = append(steps, demotion...)
	}
	if o := t.provider.displaced(); o != nil {
		steps = append(steps, o.stopStep())
	}
	return steps, nil
}

func (takeover) confirm(ctx context.Context) { reportOwnership(ctx) }

type full struct{ takeover }

func (f full) serve(rd config.RuntimeDir) config.TopologyConfig {
	t := f.takeover.serve(rd)
	t.Downstream = append(t.Downstream, rd.ClientsDownstream())
	return t
}
