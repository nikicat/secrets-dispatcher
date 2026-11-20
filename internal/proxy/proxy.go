// Package proxy implements the Secret Service proxy.
package proxy

import (
	"context"
	"errors"
	"fmt"

	"github.com/godbus/dbus/v5"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/logging"
	"github.com/nikicat/secrets-dispatcher/internal/sender"
)

// Proxy connects to a front-facing D-Bus (where clients connect) and a backend
// D-Bus (where the real Secret Service lives), registering as
// org.freedesktop.secrets on the front bus and proxying requests to the backend.
type Proxy struct {
	clientName   approval.ClientName
	local        *sender.ChainReader
	slowUpstream SlowUpstream

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
	// Reads the callers' process chains. Nil for a forwarded bus, as
	// SenderInfoResolver.local explains.
	Local        *sender.ChainReader
	SlowUpstream SlowUpstream
}

// Returns a proxy that serves the bus of clientName with cfg and gates its
// requests through approvalMgr.
func New(clientName approval.ClientName, approvalMgr *approval.Manager, cfg Config) *Proxy {
	return &Proxy{
		clientName:   clientName,
		local:        cfg.Local,
		sessions:     NewSessionManager(),
		logger:       logging.New(clientName),
		approval:     approvalMgr,
		slowUpstream: cfg.SlowUpstream,
	}
}

// Opens a connection to one bus.
type Connector func() (*dbus.Conn, error)

// Makes the proxies that serve one upstream to the downstream buses, one proxy
// per client name.
type Factory struct {
	upstream     Connector
	approval     *approval.Manager
	slowUpstream SlowUpstream
	chains       sender.ChainReader
}

// Returns a factory whose proxies serve upstream and gate requests through
// approvalMgr. The session bus's proxy reads its callers' chains with chains.
func NewFactory(upstream Connector, approvalMgr *approval.Manager, slowUpstream SlowUpstream, chains sender.ChainReader) Factory {
	return Factory{upstream: upstream, approval: approvalMgr, slowUpstream: slowUpstream, chains: chains}
}

// Claims client for a forwarded bus. Errors when another bus holds it.
func (f Factory) Claim(client approval.ClientName) (Claim, error) {
	return f.claim(client, nil)
}

// Claims ClientLocal for the session bus. Errors when another bus holds it.
func (f Factory) ClaimSessionBus() (Claim, error) {
	return f.claim(approval.ClientLocal, &f.chains)
}

func (f Factory) claim(client approval.ClientName, local *sender.ChainReader) (Claim, error) {
	if !f.approval.ClaimClient(client) {
		return Claim{}, fmt.Errorf("another bus has the client name %q", client)
	}
	return Claim{client: client, local: local, factory: f}, nil
}

// A client name that a Factory has claimed, which no other bus can take until
// Release.
type Claim struct {
	client  approval.ClientName
	local   *sender.ChainReader
	factory Factory
}

// Returns a proxy for the claimed client, serving the factory's upstream to the
// bus that front opens. Errors as Proxy.Dial does.
func (c Claim) Dial(front Connector) (*Proxy, error) {
	f := c.factory
	p := New(c.client, f.approval, Config{Local: c.local, SlowUpstream: f.slowUpstream})
	if err := p.Dial(front, f.upstream); err != nil {
		return nil, err
	}
	return p, nil
}

// Gives the client name back once its proxy has stopped.
func (c Claim) Release() { c.factory.approval.ReleaseClient(c.client) }

// Connects to the front and backend buses and serves as ConnectWith does.
// Errors when either connection fails, closing the one already open.
func (p *Proxy) Dial(front, backend Connector) error {
	frontConn, err := front()
	if err != nil {
		return fmt.Errorf("connect to downstream: %w", err)
	}
	backendConn, err := backend()
	if err != nil {
		frontConn.Close()
		return fmt.Errorf("connect to upstream: %w", err)
	}
	return p.ConnectWith(frontConn, backendConn)
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

	p.service = NewService(p.backendConn, p.sessions, p.logger, p.approval, p.tracker, p.resolver, p.slowUpstream)
	p.collection = NewCollectionHandler(p.backendConn, p.sessions, p.logger, p.approval, p.tracker, p.resolver, p.slowUpstream)
	p.item = NewItemHandler(p.backendConn, p.sessions, p.logger, p.approval, p.tracker, p.resolver, p.slowUpstream)
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
