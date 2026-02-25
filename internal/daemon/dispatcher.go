// Package daemon implements the secrets-dispatcher companion daemon.
// The daemon registers on the system D-Bus as net.mowaka.SecretsDispatcher1
// and serves stub methods. Phase 5 replaces the stubs with real business logic.
package daemon

import "github.com/godbus/dbus/v5"

// D-Bus interface constants for the companion daemon.
const (
	BusName    = "net.mowaka.SecretsDispatcher1"
	ObjectPath = dbus.ObjectPath("/net/mowaka/SecretsDispatcher1")
	Interface  = "net.mowaka.SecretsDispatcher1"
)

// Dispatcher is the D-Bus object exported under ObjectPath/Interface.
// Phase 4 provides stub responses only; Phase 5 adds real logic.
type Dispatcher struct {
	version string
}

// NewDispatcher creates a Dispatcher that reports the given version string.
func NewDispatcher(version string) *Dispatcher {
	return &Dispatcher{version: version}
}

// Ping is a health-check stub. Returns "pong" to confirm the daemon is alive.
func (d *Dispatcher) Ping() (string, *dbus.Error) {
	return "pong", nil
}

// GetVersion returns the daemon version string.
func (d *Dispatcher) GetVersion() (string, *dbus.Error) {
	return d.version, nil
}
