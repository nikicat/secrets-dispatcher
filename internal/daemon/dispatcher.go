// Package daemon is the companion daemon of the privilege separation
// experiment (ADR 0002). Its methods are stubs.
package daemon

import (
	"github.com/godbus/dbus/v5"

	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
)

// D-Bus interface constants for the companion daemon.
const (
	BusName                        = "net.mowaka.SecretsDispatcher1"
	ObjectPath                     = dbus.ObjectPath("/net/mowaka/SecretsDispatcher1")
	Interface  dbustypes.Interface = "net.mowaka.SecretsDispatcher1"
)

// The D-Bus object exported under ObjectPath and Interface.
type Dispatcher struct {
	version string
}

func NewDispatcher(version string) *Dispatcher {
	return &Dispatcher{version: version}
}

// Answers "pong" and has no other effect.
func (d *Dispatcher) Ping() (string, *dbus.Error) {
	return "pong", nil
}

func (d *Dispatcher) GetVersion() (string, *dbus.Error) {
	return d.version, nil
}
