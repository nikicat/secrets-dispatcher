package main

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/nikicat/secrets-dispatcher/internal/api"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/nikicat/secrets-dispatcher/internal/proxy"
	"github.com/nikicat/secrets-dispatcher/internal/unixsock"
)

// Serves until ctx ends.
type runner func(context.Context) error

type downstreams struct {
	factory   proxy.Factory
	providers []api.ClientProvider
	managers  []*proxy.Manager
	runners   []runner
}

// Returns the downstreams of buses, each served by a proxy from factory. Errors
// when a sockets directory cannot be watched or a client name is claimed twice.
func newDownstreams(buses []config.BusConfig, factory proxy.Factory) (*downstreams, error) {
	d := &downstreams{factory: factory}
	for _, b := range buses {
		if err := b.Bus.Visit(d); err != nil {
			return nil, err
		}
	}
	return d, nil
}

func (d *downstreams) Sockets(s config.Sockets) error {
	mgr, err := proxy.NewManager(s.Dir, d.factory)
	if err != nil {
		return fmt.Errorf("create proxy manager: %w", err)
	}
	d.providers = append(d.providers, mgr)
	d.managers = append(d.managers, mgr)
	d.runners = append(d.runners, mgr.Run)
	return nil
}

func (d *downstreams) SessionBus() error {
	claim, err := d.factory.ClaimSessionBus()
	if err != nil {
		return err
	}
	d.providers = append(d.providers, &staticProvider{info: proxy.ClientInfo{Name: approval.ClientLocal, SocketPath: unixsock.Path(config.BusSessionBus)}})
	d.runners = append(d.runners, func(ctx context.Context) error { return runSessionBus(ctx, claim) })
	return nil
}

func (d *downstreams) Socket(s config.Socket) error {
	client, err := s.Client()
	if err != nil {
		return err
	}
	claim, err := d.factory.Claim(client)
	if err != nil {
		return err
	}
	d.providers = append(d.providers, &staticProvider{info: proxy.ClientInfo{Name: client, SocketPath: s.Path}})
	d.runners = append(d.runners, func(ctx context.Context) error {
		p, err := claim.Dial(s.Connect)
		if err != nil {
			return fmt.Errorf("socket %s: %w", s.Path, err)
		}
		defer p.Close()
		return p.Run(ctx)
	})
	return nil
}

// Returns the clients of every downstream.
func (d *downstreams) Clients() []proxy.ClientInfo {
	var all []proxy.ClientInfo
	for _, p := range d.providers {
		all = append(all, p.Clients()...)
	}
	return all
}

// Tells obs about the clients that come and go on sockets directories.
func (d *downstreams) Subscribe(obs proxy.ClientObserver) {
	for _, mgr := range d.managers {
		mgr.Subscribe(obs)
	}
}

// Returns what serves each downstream.
func (d *downstreams) Runners() []runner { return slices.Clone(d.runners) }

// Serves the session bus through claim until ctx ends, and reconnects with
// backoff each time the proxy stops.
func runSessionBus(ctx context.Context, claim proxy.Claim) error {
	const maxBackoff = 30 * time.Second
	backoff := time.Second
	for {
		p, err := claim.Dial(config.SessionBus{}.Connect)
		if err == nil {
			backoff = time.Second
			err = p.Run(ctx)
			p.Close()
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		slog.Warn("session bus downstream disconnected, reconnecting",
			"error", err, "after", backoff)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

// Lists one client that stays for the life of serve.
type staticProvider struct {
	info proxy.ClientInfo
}

func (s *staticProvider) Clients() []proxy.ClientInfo {
	return []proxy.ClientInfo{s.info}
}
