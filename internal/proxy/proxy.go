// Package proxy implements the Secret Service proxy.
package proxy

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/godbus/dbus/v5"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/logging"
)

// Proxy connects to a remote D-Bus (via SSH tunnel socket) and the local
// session bus, registering as org.freedesktop.secrets on the remote bus
// and proxying requests to the local Secret Service.
type Proxy struct {
	remoteSocketPath string
	clientName       string

	remoteConn *dbus.Conn
	localConn  *dbus.Conn

	sessions *SessionManager
	logger   *logging.Logger

	service           *Service
	collection        *CollectionHandler
	item              *ItemHandler
	subtreeProperties *SubtreePropertiesHandler
}

// Config holds configuration for the proxy.
type Config struct {
	RemoteSocketPath string
	ClientName       string
	LogLevel         slog.Level
}

// New creates a new Proxy with the given configuration.
func New(cfg Config) *Proxy {
	clientName := cfg.ClientName
	if clientName == "" {
		clientName = "unknown"
	}

	return &Proxy{
		remoteSocketPath: cfg.RemoteSocketPath,
		clientName:       clientName,
		sessions:         NewSessionManager(),
		logger:           logging.New(cfg.LogLevel, clientName),
	}
}

// Connect establishes connections to both the remote socket and local session bus.
func (p *Proxy) Connect(ctx context.Context) error {
	var err error

	// Connect to local session bus
	p.localConn, err = dbus.ConnectSessionBus()
	if err != nil {
		return fmt.Errorf("connect to local session bus: %w", err)
	}

	// Connect to remote D-Bus via socket
	p.remoteConn, err = dbus.Connect("unix:path=" + p.remoteSocketPath)
	if err != nil {
		p.localConn.Close()
		return fmt.Errorf("connect to remote socket %s: %w", p.remoteSocketPath, err)
	}

	// Create handlers
	p.service = NewService(p.localConn, p.sessions, p.logger)
	p.collection = NewCollectionHandler(p.localConn, p.sessions, p.logger)
	p.item = NewItemHandler(p.localConn, p.sessions, p.logger)
	p.subtreeProperties = NewSubtreePropertiesHandler(p.localConn, p.sessions, p.logger)

	// Export the Service interface on the remote connection
	if err := p.remoteConn.Export(p.service, dbustypes.ServicePath, dbustypes.ServiceInterface); err != nil {
		p.Close()
		return fmt.Errorf("export Service interface: %w", err)
	}

	// Export Properties interface for Service
	if err := p.remoteConn.Export(p.service, dbustypes.ServicePath, "org.freedesktop.DBus.Properties"); err != nil {
		p.Close()
		return fmt.Errorf("export Properties interface for Service: %w", err)
	}

	// Export introspectable interface
	if err := p.remoteConn.Export(introspectable{p.service.Introspect}, dbustypes.ServicePath, "org.freedesktop.DBus.Introspectable"); err != nil {
		p.Close()
		return fmt.Errorf("export Introspectable interface: %w", err)
	}

	// Export collection handler using subtree for /org/freedesktop/secrets/collection/*
	if err := p.remoteConn.ExportSubtree(p.collection, "/org/freedesktop/secrets/collection", dbustypes.CollectionInterface); err != nil {
		p.Close()
		return fmt.Errorf("export Collection subtree: %w", err)
	}

	// Export unified Properties handler for collections and items
	if err := p.remoteConn.ExportSubtree(p.subtreeProperties, "/org/freedesktop/secrets/collection", "org.freedesktop.DBus.Properties"); err != nil {
		p.Close()
		return fmt.Errorf("export Properties subtree: %w", err)
	}

	// Export item handler - items are at paths like /org/freedesktop/secrets/collection/xxx/yyy
	if err := p.remoteConn.ExportSubtree(p.item, "/org/freedesktop/secrets/collection", dbustypes.ItemInterface); err != nil {
		p.Close()
		return fmt.Errorf("export Item subtree: %w", err)
	}

	// Request the bus name
	reply, err := p.remoteConn.RequestName(dbustypes.BusName, dbus.NameFlagReplaceExisting)
	if err != nil {
		p.Close()
		return fmt.Errorf("request bus name: %w", err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		p.Close()
		return fmt.Errorf("failed to become primary owner of %s (reply=%d)", dbustypes.BusName, reply)
	}

	p.logger.Info("connected and registered",
		"remote_socket", p.remoteSocketPath,
		"bus_name", dbustypes.BusName)

	return nil
}

// Run blocks until the context is cancelled.
func (p *Proxy) Run(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

// Close shuts down the proxy and closes all connections.
func (p *Proxy) Close() error {
	p.logger.Info("shutting down")

	if p.sessions != nil && p.localConn != nil {
		p.sessions.CloseAll(p.localConn)
	}

	if p.remoteConn != nil {
		p.remoteConn.Close()
	}
	if p.localConn != nil {
		p.localConn.Close()
	}

	return nil
}

// introspectable implements org.freedesktop.DBus.Introspectable.
type introspectable struct {
	introspectFunc func() string
}

func (i introspectable) Introspect() (string, *dbus.Error) {
	return i.introspectFunc(), nil
}
