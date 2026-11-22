package proxy

import (
	"github.com/godbus/dbus/v5"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/procutil"
	"github.com/nikicat/secrets-dispatcher/internal/sender"
)

// The bus daemon that one connection is attached to.
type busDaemon struct {
	conn *dbus.Conn
}

func (d busDaemon) call(method string, arg string) *dbus.Call {
	return d.conn.BusObject().Call(dbustypes.BusDaemonInterface.Member(method), 0, arg)
}

// Checks whether the given name has an owner on the bus. An activatable name
// that is not running has none, and asking does not start it. False when the
// bus cannot be asked.
func (d busDaemon) nameHasOwner(name string) bool {
	var has bool
	if err := d.call("NameHasOwner", name).Store(&has); err != nil {
		return false
	}
	return has
}

// Returns the unique name that owns the given name. Errors when the name has no
// owner.
func (d busDaemon) nameOwner(name string) (sender.UniqueName, error) {
	var owner sender.UniqueName
	err := d.call("GetNameOwner", name).Store(&owner)
	return owner, err
}

func (d busDaemon) connectionPID(name sender.UniqueName) (procutil.PID, error) {
	var pid procutil.PID
	err := d.call("GetConnectionUnixProcessID", string(name)).Store(&pid)
	return pid, err
}

func (d busDaemon) connectionUser(name sender.UniqueName) (procutil.UID, error) {
	var uid procutil.UID
	err := d.call("GetConnectionUnixUser", string(name)).Store(&uid)
	return uid, err
}
