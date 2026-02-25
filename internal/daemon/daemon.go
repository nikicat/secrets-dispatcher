package daemon

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/introspect"
)

// Config holds daemon startup parameters.
type Config struct {
	// BusAddress is the D-Bus address to connect to.
	// Empty means the system bus (production). Non-empty connects to a custom
	// address — used by integration tests to point at a private dbus-daemon.
	BusAddress string

	// Version is the string reported by GetVersion().
	Version string
}

// Run starts the daemon, registers on D-Bus, sends READY=1 via sd-notify,
// and blocks until ctx is cancelled. Returns nil on clean shutdown.
func Run(ctx context.Context, cfg Config) error {
	// Connect to the appropriate D-Bus bus.
	var conn *dbus.Conn
	var err error
	if cfg.BusAddress == "" {
		conn, err = dbus.ConnectSystemBus()
	} else {
		conn, err = dbus.Connect(cfg.BusAddress)
	}
	if err != nil {
		return fmt.Errorf("connect to D-Bus: %w", err)
	}
	defer conn.Close()

	// Create and export the dispatcher.
	dispatcher := NewDispatcher(cfg.Version)

	if err := conn.Export(dispatcher, ObjectPath, Interface); err != nil {
		return fmt.Errorf("export dispatcher: %w", err)
	}

	// Always export Introspectable — without it busctl introspect gives opaque errors.
	node := &introspect.Node{
		Interfaces: []introspect.Interface{
			introspect.IntrospectData,
			{
				Name:    Interface,
				Methods: introspect.Methods(dispatcher),
			},
		},
	}
	if err := conn.Export(introspect.NewIntrospectable(node), ObjectPath, "org.freedesktop.DBus.Introspectable"); err != nil {
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
