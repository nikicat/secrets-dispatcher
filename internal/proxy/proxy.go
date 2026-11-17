// Package proxy implements the Secret Service proxy.
package proxy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/logging"
)

// Proxy connects to a front-facing D-Bus (where clients connect) and a backend
// D-Bus (where the real Secret Service lives), registering as
// org.freedesktop.secrets on the front bus and proxying requests to the backend.
type Proxy struct {
	clientName            string
	local                 *localProcs
	upstreamNotifier      UpstreamNotifier
	upstreamSlowThreshold time.Duration

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
	prompt            *PromptHandler
	subtreeProperties *SubtreePropertiesHandler
	signals           *signalForwarder
	prompter          *prompterBridge
}

// Config holds configuration for the proxy.
type Config struct {
	ClientName string
	// True only for the session bus, whose callers are processes on this
	// machine. Any other front bus is forwarded (see SenderInfoResolver).
	LocalProcesses bool
	// Applies only with LocalProcesses.
	TrimProcessChain      bool
	LogLevel              slog.Level
	Approval              *approval.Manager
	UpstreamNotifier      UpstreamNotifier
	UpstreamSlowThreshold time.Duration
}

// Returns what the resolver needs to describe local processes, or nil for a
// forwarded bus.
func (cfg Config) localProcs() *localProcs {
	if !cfg.LocalProcesses {
		return nil
	}
	selfExe, _ := os.Executable()
	return &localProcs{trimProcessChain: cfg.TrimProcessChain, selfExe: selfExe}
}

// New creates a new Proxy with the given configuration.
func New(cfg Config) *Proxy {
	clientName := cfg.ClientName
	if clientName == "" {
		clientName = approval.ClientUnknown
	}

	approvalMgr := cfg.Approval
	if approvalMgr == nil {
		// Create a disabled manager if none provided (auto-approve all)
		approvalMgr = approval.NewDisabledManager()
	}

	return &Proxy{
		clientName:            clientName,
		local:                 cfg.localProcs(),
		sessions:              NewSessionManager(),
		logger:                logging.New(cfg.LogLevel, clientName),
		approval:              approvalMgr,
		upstreamNotifier:      cfg.UpstreamNotifier,
		upstreamSlowThreshold: cfg.UpstreamSlowThreshold,
	}
}

// Serves the Secret Service from backendConn to clients on frontConn.
// On a forwarded bus it may return while the bus name is still queued (ADR 0001).
// When it errors, it closes the proxy and both connections.
func (p *Proxy) ConnectWith(frontConn, backendConn *dbus.Conn) (err error) {
	defer func() {
		if err != nil {
			p.Close()
		}
	}()
	p.frontConn = frontConn
	p.backendConn = backendConn

	p.tracker, err = newClientTracker(p.frontConn)
	if err != nil {
		return fmt.Errorf("create client tracker: %w", err)
	}

	p.resolver = newSenderInfoResolver(p.frontConn, p.clientName, p.local)

	p.service = NewService(p.backendConn, p.sessions, p.logger, p.approval, p.tracker, p.resolver, p.upstreamNotifier, p.upstreamSlowThreshold)
	p.collection = NewCollectionHandler(p.backendConn, p.sessions, p.logger, p.approval, p.tracker, p.resolver, p.upstreamNotifier, p.upstreamSlowThreshold)
	p.item = NewItemHandler(p.backendConn, p.sessions, p.logger, p.approval, p.tracker, p.resolver, p.upstreamNotifier, p.upstreamSlowThreshold)
	p.prompt = NewPromptHandler(p.backendConn, p.logger)
	p.subtreeProperties = NewSubtreePropertiesHandler(p.backendConn, p.sessions, p.logger)

	if err := p.frontConn.Export(p.service, dbustypes.ServicePath, dbustypes.ServiceInterface); err != nil {
		return fmt.Errorf("export Service interface: %w", err)
	}

	if err := p.frontConn.Export(p.service, dbustypes.ServicePath, "org.freedesktop.DBus.Properties"); err != nil {
		return fmt.Errorf("export Properties interface for Service: %w", err)
	}

	if err := p.frontConn.Export(introspectable{p.service.Introspect}, dbustypes.ServicePath, "org.freedesktop.DBus.Introspectable"); err != nil {
		return fmt.Errorf("export Introspectable interface: %w", err)
	}

	// libsecret (secret-tool) calls methods on alias paths such as
	// /org/freedesktop/secrets/aliases/default directly.
	for _, prefix := range []string{"/org/freedesktop/secrets/collection", "/org/freedesktop/secrets/aliases"} {
		if err := p.frontConn.ExportSubtree(p.collection, dbus.ObjectPath(prefix), dbustypes.CollectionInterface); err != nil {
			return fmt.Errorf("export Collection subtree at %s: %w", prefix, err)
		}

		if err := p.frontConn.ExportSubtree(p.subtreeProperties, dbus.ObjectPath(prefix), "org.freedesktop.DBus.Properties"); err != nil {
			return fmt.Errorf("export Properties subtree at %s: %w", prefix, err)
		}

		if err := p.frontConn.ExportSubtree(p.item, dbus.ObjectPath(prefix), dbustypes.ItemInterface); err != nil {
			return fmt.Errorf("export Item subtree at %s: %w", prefix, err)
		}
	}

	// Unlock, Lock, CreateCollection, CreateItem and Delete return prompt paths.
	// Without this export a client cannot act on them, and a locked collection
	// is a dead end.
	if err := p.frontConn.ExportSubtree(p.prompt, "/org/freedesktop/secrets/prompt", dbustypes.PromptInterface); err != nil {
		return fmt.Errorf("export Prompt subtree: %w", err)
	}

	if err := p.claimBusName(); err != nil {
		return err
	}

	p.signals, err = newSignalForwarder(p.backendConn, p.frontConn, p.logger)
	if err != nil {
		return fmt.Errorf("start signal forwarder: %w", err)
	}

	p.prompter, err = newPrompterBridge(p.frontConn, p.backendConn, p.logger)
	if err != nil {
		return fmt.Errorf("start prompter bridge: %w", err)
	}

	return nil
}

// Claims org.freedesktop.secrets on the front bus, or queues for it on a forwarded bus (ADR 0001).
// Errors with errBusNameTaken when another connection holds it on the session bus.
func (p *Proxy) claimBusName() error {
	reply, err := p.frontConn.RequestName(dbustypes.BusName, dbus.NameFlagReplaceExisting)
	if err != nil {
		return fmt.Errorf("request bus name: %w", err)
	}
	switch reply {
	case dbus.RequestNameReplyPrimaryOwner, dbus.RequestNameReplyAlreadyOwner:
		return nil
	case dbus.RequestNameReplyInQueue:
		if p.local == nil {
			p.logger.Warn("bus name taken, queued until its owner leaves", "name", dbustypes.BusName)
			return nil
		}
	}
	return fmt.Errorf("%w of %s (reply=%d)", errBusNameTaken, dbustypes.BusName, reply)
}

var errBusNameTaken = errors.New("failed to become primary owner")

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

	if p.prompter != nil {
		p.prompter.close()
	}

	if p.signals != nil {
		p.signals.close()
	}

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
