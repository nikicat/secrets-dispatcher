package service

import (
	"context"
	"fmt"
	"time"

	"github.com/godbus/dbus/v5"

	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/procutil"
)

// The calls the service setup makes on the user's D-Bus session bus.
type sessionBus interface {
	// Returns the process that owns org.freedesktop.secrets. Errors when
	// nothing owns it.
	secretsOwner(ctx context.Context) (procutil.PID, error)
	// Has the bus daemon reread its configuration and service files.
	reloadConfig(ctx context.Context) error
	// Starts the D-Bus-activatable provider of org.freedesktop.secrets when
	// nothing owns the name.
	pokeSecrets(ctx context.Context) error
}

// The user's session bus. Replaced in tests.
var session sessionBus = godbusSession{}

// The session bus through godbus, on a connection per call.
type godbusSession struct{}

// How long pokeSecrets waits for the activated provider to answer.
const pokeTimeout = 10 * time.Second

func (godbusSession) secretsOwner(ctx context.Context) (procutil.PID, error) {
	var pid procutil.PID
	err := callSession(ctx, busDaemon, busDaemonInterface+".GetConnectionUnixProcessID", []any{secretsService.dest}, &pid)
	return pid, err
}

func (godbusSession) reloadConfig(ctx context.Context) error {
	return callSession(ctx, busDaemon, busDaemonInterface+"."+reloadConfigMethod, nil)
}

const reloadConfigMethod = "ReloadConfig"

// The busctl command line that the dry run prints for reloadConfig
// (ARCHITECTURE.md, "Service setup").
var reloadConfigCommand = fmt.Sprintf("busctl --user call %s %s %s %s", busDaemon.dest, busDaemon.path, busDaemonInterface, reloadConfigMethod)

func (godbusSession) pokeSecrets(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, pokeTimeout)
	defer cancel()
	var collections dbus.Variant
	return callSession(ctx, secretsService, "org.freedesktop.DBus.Properties.Get",
		[]any{dbustypes.ServiceInterface, "Collections"}, &collections)
}

// An object on the session bus.
type busObject struct {
	dest string
	path dbus.ObjectPath
}

const busDaemonInterface = "org.freedesktop.DBus"

var (
	busDaemon      = busObject{"org.freedesktop.DBus", "/org/freedesktop/DBus"}
	secretsService = busObject{dbustypes.BusName, dbustypes.ServicePath}
)

// Calls method on obj over a new session bus connection and stores the reply
// in reply. Errors when the bus is unreachable or the call fails.
func callSession(ctx context.Context, obj busObject, method string, args []any, reply ...any) error {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Object(obj.dest, obj.path).CallWithContext(ctx, method, 0, args...).Store(reply...)
}
