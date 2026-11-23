package daemon

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
)

// The daemon's startup parameters.
type Config struct {
	Bus Bus

	// The version that GetVersion reports.
	Version string
}

// The bus that the daemon registers on.
type Bus interface {
	Connect() (*dbus.Conn, error)
}

type SystemBus struct{}

// The bus at a D-Bus address, such as unix:path=/run/dbus/system_bus_socket.
type BusAt string

func (SystemBus) Connect() (*dbus.Conn, error) { return dbus.ConnectSystemBus() }
func (a BusAt) Connect() (*dbus.Conn, error)   { return dbus.Connect(string(a)) }

// Registers the daemon on D-Bus, sends READY=1 via sd-notify, and serves until
// the given context ends.
func Run(ctx context.Context, cfg Config) error {
	conn, err := cfg.Bus.Connect()
	if err != nil {
		return fmt.Errorf("connect to D-Bus: %w", err)
	}
	defer conn.Close()

	// Create and export the dispatcher.
	dispatcher := NewDispatcher(cfg.Version)

	if err := conn.Export(dispatcher, ObjectPath, string(Interface)); err != nil {
		return fmt.Errorf("export dispatcher: %w", err)
	}

	// Always export Introspectable — without it busctl introspect gives opaque errors.
	node := &introspect.Node{
		Interfaces: []introspect.Interface{
			introspect.IntrospectData,
			{
				Name:    string(Interface),
				Methods: introspect.Methods(dispatcher),
			},
		},
	}
	if err := conn.Export(introspect.NewIntrospectable(node), ObjectPath, string(dbustypes.IntrospectableInterface)); err != nil {
		return fmt.Errorf("export introspectable: %w", err)
	}

	// Request the well-known bus name.
	reply, err := conn.RequestName(BusName, dbus.NameFlagDoNotQueue)
	if err != nil {
		return fmt.Errorf("request bus name %q: %w", BusName, err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return fmt.Errorf("not primary owner of %q (reply=%d); policy rejected or name already taken", BusName, reply)
	}

	slog.Info("daemon ready", "bus_name", BusName)

	// Notify systemd that startup is complete.
	SdNotify("READY=1")

	// Block until context is cancelled (SIGTERM/SIGINT handled by caller).
	<-ctx.Done()

	slog.Info("daemon shutting down")
	return nil
}
