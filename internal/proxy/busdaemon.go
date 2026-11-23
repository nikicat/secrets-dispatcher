package proxy

import (
	"context"

	"github.com/godbus/dbus/v5"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/procutil"
	"github.com/nikicat/secrets-dispatcher/internal/sender"
)

// The bus daemon that one connection is attached to.
type busDaemon struct {
	conn *dbus.Conn
}

func (d busDaemon) call(ctx context.Context, method string, arg string) *dbus.Call {
	return d.conn.BusObject().CallWithContext(ctx, dbustypes.BusDaemonInterface.Member(method), 0, arg)
}

// Checks whether the given name has an owner on the bus. An activatable name
// that is not running has none, and asking does not start it. False when the
// bus cannot be asked.
func (d busDaemon) nameHasOwner(name string) bool {
	var has bool
	if err := d.call(context.Background(), "NameHasOwner", name).Store(&has); err != nil {
		return false
	}
	return has
}

// Returns the unique name that owns the given name. Errors when the name has no
// owner.
func (d busDaemon) nameOwner(name string) (sender.UniqueName, error) {
	var owner sender.UniqueName
	err := d.call(context.Background(), "GetNameOwner", name).Store(&owner)
	return owner, err
}

// Releases the given name the connection owns, giving up when the given context
// ends.
func (d busDaemon) releaseName(ctx context.Context, name string) {
	d.call(ctx, "ReleaseName", name)
}

func (d busDaemon) connectionPID(name sender.UniqueName) (procutil.PID, error) {
	var pid procutil.PID
	err := d.call(context.Background(), "GetConnectionUnixProcessID", string(name)).Store(&pid)
	return pid, err
}

func (d busDaemon) connectionUser(name sender.UniqueName) (procutil.UID, error) {
	var uid procutil.UID
	err := d.call(context.Background(), "GetConnectionUnixUser", string(name)).Store(&uid)
	return uid, err
}
