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
	clientName approval.ClientName
	local      *sender.ChainReader
	base       handlerBase
	conn       *connection
}

// What a connected proxy serves, and the buses it serves it on.
type connection struct {
	frontConn *dbus.Conn // clients connect here (session bus or remote socket)
	h         *handler
	signals   *signalForwarder
	prompter  *prompterBridge
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
		clientName: clientName,
		local:      cfg.Local,
		base: handlerBase{
			sessions:     NewSessionManager(),
			logger:       logging.New(clientName),
			approval:     approvalMgr,
			slowUpstream: cfg.SlowUpstream,
		},
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
// When it errors, it closes both connections.
func (p *Proxy) ConnectWith(frontConn, backendConn *dbus.Conn) error {
	c, err := p.connect(frontConn, backendConn)
	if err != nil {
		frontConn.Close()
		backendConn.Close()
		return err
	}
	err = c.export()
	if err == nil {
		err = p.claimBusName(frontConn)
	}
	if err != nil {
		c.close()
		return err
	}
	p.conn = c
	return nil
}

// Returns the connection between the given buses, with its background work
// started. Errors when any part cannot start, and leaves nothing running.
func (p *Proxy) connect(frontConn, backendConn *dbus.Conn) (*connection, error) {
	tracker, err := newClientTracker(frontConn)
	if err != nil {
		return nil, fmt.Errorf("create client tracker: %w", err)
	}
	signals, err := newSignalForwarder(backendConn, frontConn, p.base.logger)
	if err != nil {
		tracker.close()
		return nil, fmt.Errorf("start signal forwarder: %w", err)
	}
	prompter := newPrompterBridge(frontConn, backendConn, p.base.logger)
	// The bridge carries prompts to gnome-shell on the session bus (ADR 0009). A
	// forwarded bus's backend may be the session bus itself.
	if p.servesSessionBus() {
		if err := prompter.claim(); err != nil {
			signals.close()
			tracker.close()
			return nil, fmt.Errorf("start prompter bridge: %w", err)
		}
	}
	return &connection{
		frontConn: frontConn,
		h: &handler{
			handlerBase: p.base,
			backendConn: backendConn,
			tracker:     tracker,
			resolver:    newSenderInfoResolver(frontConn, p.clientName, p.local),
		},
		signals:  signals,
		prompter: prompter,
	}, nil
}

// Exports the Secret Service on the front bus.
func (c *connection) export() error {
	h := c.h
	service := &Service{properties{h, serviceObject{}}}
	collection := &CollectionHandler{properties{h, collectionObjects{}}}
	item := &ItemHandler{properties{h, itemObjects{}}}
	prompt := NewPromptHandler(h.backendConn, h.logger)
	subtreeProperties := NewSubtreePropertiesHandler(h.backendConn, h.sessions, h.logger)

	if err := c.frontConn.Export(service, dbustypes.ServicePath, string(dbustypes.ServiceInterface)); err != nil {
		return fmt.Errorf("export Service interface: %w", err)
	}

	if err := c.frontConn.Export(service, dbustypes.ServicePath, string(dbustypes.PropertiesInterface)); err != nil {
		return fmt.Errorf("export Properties interface for Service: %w", err)
	}

	if err := c.frontConn.Export(introspectable{service.Introspect}, dbustypes.ServicePath, string(dbustypes.IntrospectableInterface)); err != nil {
		return fmt.Errorf("export Introspectable interface: %w", err)
	}

	// libsecret (secret-tool) calls methods on alias paths such as
	// /org/freedesktop/secrets/aliases/default directly.
	for _, prefix := range []dbus.ObjectPath{dbustypes.CollectionsPath, dbustypes.AliasesPath} {
		if err := c.frontConn.ExportSubtree(collection, prefix, string(dbustypes.CollectionInterface)); err != nil {
			return fmt.Errorf("export Collection subtree at %s: %w", prefix, err)
		}

		if err := c.frontConn.ExportSubtree(subtreeProperties, prefix, string(dbustypes.PropertiesInterface)); err != nil {
			return fmt.Errorf("export Properties subtree at %s: %w", prefix, err)
		}

		if err := c.frontConn.ExportSubtree(item, prefix, string(dbustypes.ItemInterface)); err != nil {
			return fmt.Errorf("export Item subtree at %s: %w", prefix, err)
		}
	}

	// Unlock, Lock, CreateCollection, CreateItem and Delete return prompt paths.
	// Without this export a client cannot act on them, and a locked collection
	// is a dead end.
	if err := c.frontConn.ExportSubtree(prompt, "/org/freedesktop/secrets/prompt", string(dbustypes.PromptInterface)); err != nil {
		return fmt.Errorf("export Prompt subtree: %w", err)
	}

	return nil
}

// Claims org.freedesktop.secrets on the front bus, or queues for it on a forwarded bus (ADR 0001).
// Errors with errBusNameTaken when another connection holds it on the session bus.
func (p *Proxy) claimBusName(frontConn *dbus.Conn) error {
	reply, err := frontConn.RequestName(dbustypes.BusName, dbus.NameFlagReplaceExisting)
	if err != nil {
		return fmt.Errorf("request bus name: %w", err)
	}
	switch reply {
	case dbus.RequestNameReplyPrimaryOwner, dbus.RequestNameReplyAlreadyOwner:
		return nil
	case dbus.RequestNameReplyInQueue:
		if !p.servesSessionBus() {
			p.base.logger.Warn("bus name taken, queued until its owner leaves", "name", dbustypes.BusName)
			return nil
		}
	}
	return fmt.Errorf("%w of %s (reply=%d)", errBusNameTaken, dbustypes.BusName, reply)
}

var errBusNameTaken = errors.New("failed to become primary owner")

// Checks whether the proxy serves the session bus rather than a forwarded bus.
func (p *Proxy) servesSessionBus() bool { return p.local != nil }

// Run blocks until the context is cancelled or the front connection is closed.
func (p *Proxy) Run(ctx context.Context) error {
	if p.conn == nil {
		return fmt.Errorf("not connected")
	}

	frontCtx := p.conn.frontConn.Context()
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
	p.base.logger.Info("shutting down")
	if p.conn != nil {
		p.conn.close()
		p.conn = nil
	}
	return nil
}

// Stops what the connection serves and closes both connections.
func (c *connection) close() {
	c.prompter.close()
	c.signals.close()
	c.h.close()
	c.frontConn.Close()
}

// introspectable implements org.freedesktop.DBus.Introspectable.
type introspectable struct {
	introspectFunc func() string
}

func (i introspectable) Introspect() (string, *dbus.Error) {
	return i.introspectFunc(), nil
}
