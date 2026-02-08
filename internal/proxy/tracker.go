// Package proxy implements the Secret Service proxy.
package proxy

import (
	"context"
	"sync"

	"github.com/godbus/dbus/v5"
)

// clientTracker tracks D-Bus clients and provides contexts that get cancelled
// when the client disconnects.
type clientTracker struct {
	conn *dbus.Conn
	mu   sync.Mutex
	// clients maps sender unique name (e.g., ":1.123") to cancel function
	clients   map[string]context.CancelFunc
	signals   chan *dbus.Signal
	done      chan struct{}
	closeOnce sync.Once
}

// newClientTracker creates a new tracker and starts listening for NameOwnerChanged signals.
func newClientTracker(conn *dbus.Conn) (*clientTracker, error) {
	t := &clientTracker{
		conn:    conn,
		clients: make(map[string]context.CancelFunc),
		signals: make(chan *dbus.Signal, 16),
		done:    make(chan struct{}),
	}

	// Subscribe to NameOwnerChanged signals to detect client disconnects
	if err := conn.AddMatchSignal(
		dbus.WithMatchInterface("org.freedesktop.DBus"),
		dbus.WithMatchMember("NameOwnerChanged"),
		dbus.WithMatchSender("org.freedesktop.DBus"),
	); err != nil {
		return nil, err
	}

	conn.Signal(t.signals)

	// Start goroutine to process signals
	go t.processSignals()

	return t, nil
}

// processSignals handles NameOwnerChanged signals.
func (t *clientTracker) processSignals() {
	for {
		select {
		case <-t.done:
			return
		case signal, ok := <-t.signals:
			if !ok {
				// Channel closed by D-Bus library when connection closes
				return
			}

			if signal.Name != "org.freedesktop.DBus.NameOwnerChanged" {
				continue
			}

			// NameOwnerChanged(name string, old_owner string, new_owner string)
			if len(signal.Body) != 3 {
				continue
			}

			name, ok1 := signal.Body[0].(string)
			oldOwner, ok2 := signal.Body[1].(string)
			newOwner, ok3 := signal.Body[2].(string)

			if !ok1 || !ok2 || !ok3 {
				continue
			}

			// Client disconnected when: name is a unique name, old_owner is non-empty, new_owner is empty
			if name != "" && name[0] == ':' && oldOwner != "" && newOwner == "" {
				t.clientDisconnected(oldOwner)
			}
		}
	}
}

// clientDisconnected is called when a client disconnects.
func (t *clientTracker) clientDisconnected(sender string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if cancel, ok := t.clients[sender]; ok {
		cancel()
		delete(t.clients, sender)
	}
}

// contextForSender returns a context that will be cancelled when the given sender disconnects.
// The context should be used for the duration of a single request.
func (t *clientTracker) contextForSender(parent context.Context, sender string) context.Context {
	t.mu.Lock()
	defer t.mu.Unlock()

	// If there's already a context for this sender, cancel it first
	// (shouldn't happen normally, but be safe)
	if cancel, ok := t.clients[sender]; ok {
		cancel()
	}

	ctx, cancel := context.WithCancel(parent)
	t.clients[sender] = cancel

	return ctx
}

// remove cleans up tracking for a sender after their request completes.
func (t *clientTracker) remove(sender string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if cancel, ok := t.clients[sender]; ok {
		cancel()
		delete(t.clients, sender)
	}
}

// close stops the tracker.
func (t *clientTracker) close() {
	t.closeOnce.Do(func() {
		// Signal the processSignals goroutine to stop
		close(t.done)
		// Unregister from receiving signals (don't close signals channel -
		// the D-Bus library may have already closed it or will close it)
		t.conn.RemoveSignal(t.signals)
	})

	t.mu.Lock()
	defer t.mu.Unlock()

	// Cancel all pending contexts
	for _, cancel := range t.clients {
		cancel()
	}
	t.clients = nil
}
