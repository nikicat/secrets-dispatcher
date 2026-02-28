// Package proxy implements the Secret Service proxy.
package proxy

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/godbus/dbus/v5"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/logging"
)

// Proxy connects to a front-facing D-Bus (where clients connect) and a backend
// D-Bus (where the real Secret Service lives), registering as
// org.freedesktop.secrets on the front bus and proxying requests to the backend.
type Proxy struct {
	clientName string

	frontConn   *dbus.Conn // clients connect here (session bus or remote socket)
	backendConn *dbus.Conn // real Secret Service lives here (session bus or private bus)

	sessions *SessionManager
	logger   *logging.Logger
	approval *approval.Manager
	tracker  *clientTracker
	resolver *SenderInfoResolver

	service           *Service
	collection        *CollectionHandler
	item              *ItemHandler
	subtreeProperties *SubtreePropertiesHandler
}

// Config holds configuration for the proxy.
type Config struct {
	ClientName string
	LogLevel   slog.Level
	Approval   *approval.Manager
}

// New creates a new Proxy with the given configuration.
func New(cfg Config) *Proxy {
	clientName := cfg.ClientName
	if clientName == "" {
		clientName = "unknown"
	}

	approvalMgr := cfg.Approval
	if approvalMgr == nil {
		// Create a disabled manager if none provided (auto-approve all)
		approvalMgr = approval.NewDisabledManager()
	}

	return &Proxy{
		clientName: clientName,
		sessions:   NewSessionManager(),
		logger:     logging.New(cfg.LogLevel, clientName),
		approval:   approvalMgr,
	}
}

// ConnectWith sets up the proxy using pre-created D-Bus connections.
// frontConn is where clients connect; backendConn is where the real Secret Service lives.
func (p *Proxy) ConnectWith(frontConn, backendConn *dbus.Conn) error {
	p.frontConn = frontConn
	p.backendConn = backendConn

	// Create client tracker to detect disconnects
	var err error
	p.tracker, err = newClientTracker(p.frontConn)
	if err != nil {
		p.Close()
		return fmt.Errorf("create client tracker: %w", err)
	}

	// Create sender info resolver
	p.resolver = NewSenderInfoResolver(p.frontConn)

	// Create handlers — they talk to the backend
	p.service = NewService(p.backendConn, p.sessions, p.logger, p.approval, p.clientName, p.tracker, p.resolver)
	p.collection = NewCollectionHandler(p.backendConn, p.sessions, p.logger, p.approval, p.clientName, p.tracker, p.resolver)
	p.item = NewItemHandler(p.backendConn, p.sessions, p.logger, p.approval, p.clientName, p.tracker, p.resolver)
	p.subtreeProperties = NewSubtreePropertiesHandler(p.backendConn, p.sessions, p.logger)

	// Export interfaces on the front connection (where clients call us)
	if err := p.frontConn.Export(p.service, dbustypes.ServicePath, dbustypes.ServiceInterface); err != nil {
		p.Close()
		return fmt.Errorf("export Service interface: %w", err)
	}

	if err := p.frontConn.Export(p.service, dbustypes.ServicePath, "org.freedesktop.DBus.Properties"); err != nil {
		p.Close()
		return fmt.Errorf("export Properties interface for Service: %w", err)
	}

	if err := p.frontConn.Export(introspectable{p.service.Introspect}, dbustypes.ServicePath, "org.freedesktop.DBus.Introspectable"); err != nil {
		p.Close()
		return fmt.Errorf("export Introspectable interface: %w", err)
	}

	// Export subtree handlers for both /collection and /aliases prefixes.
	// libsecret (secret-tool) calls methods on alias paths like
	// /org/freedesktop/secrets/aliases/default directly.
	for _, prefix := range []string{"/org/freedesktop/secrets/collection", "/org/freedesktop/secrets/aliases"} {
		if err := p.frontConn.ExportSubtree(p.collection, dbus.ObjectPath(prefix), dbustypes.CollectionInterface); err != nil {
			p.Close()
			return fmt.Errorf("export Collection subtree at %s: %w", prefix, err)
		}

		if err := p.frontConn.ExportSubtree(p.subtreeProperties, dbus.ObjectPath(prefix), "org.freedesktop.DBus.Properties"); err != nil {
			p.Close()
			return fmt.Errorf("export Properties subtree at %s: %w", prefix, err)
		}

		if err := p.frontConn.ExportSubtree(p.item, dbus.ObjectPath(prefix), dbustypes.ItemInterface); err != nil {
			p.Close()
			return fmt.Errorf("export Item subtree at %s: %w", prefix, err)
		}
	}

	// Request the bus name on the front connection
	reply, err := p.frontConn.RequestName(dbustypes.BusName, dbus.NameFlagReplaceExisting)
	if err != nil {
		p.Close()
		return fmt.Errorf("request bus name: %w", err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		p.Close()
		return fmt.Errorf("failed to become primary owner of %s (reply=%d)", dbustypes.BusName, reply)
	}

	return nil
}

// Run blocks until the context is cancelled or the front connection is closed.
func (p *Proxy) Run(ctx context.Context) error {
	if p.frontConn == nil {
		return fmt.Errorf("not connected")
	}

	frontCtx := p.frontConn.Context()
	if frontCtx == nil {
		// Fallback if context is not available
		<-ctx.Done()
		return ctx.Err()
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-frontCtx.Done():
		// Front connection closed (e.g., SSH tunnel disconnected)
		return fmt.Errorf("front connection closed")
	}
}

// Close shuts down the proxy and closes all connections.
func (p *Proxy) Close() error {
	p.logger.Info("shutting down")

	if p.tracker != nil {
		p.tracker.close()
	}

	if p.sessions != nil && p.backendConn != nil {
		p.sessions.CloseAll(p.backendConn)
	}

	if p.frontConn != nil {
		p.frontConn.Close()
	}
	if p.backendConn != nil {
		p.backendConn.Close()
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
