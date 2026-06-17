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
	// clients maps sender unique name (e.g., ":1.123") to active request cancel functions.
	clients   map[string]map[uint64]context.CancelFunc
	signals   chan *dbus.Signal
	done      chan struct{}
	closeOnce sync.Once
	nextID    uint64
}

// newClientTracker creates a new tracker and starts listening for NameOwnerChanged signals.
func newClientTracker(conn *dbus.Conn) (*clientTracker, error) {
	t := &clientTracker{
		conn:    conn,
		clients: make(map[string]map[uint64]context.CancelFunc),
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
	contexts := t.clients[sender]
	delete(t.clients, sender)
	t.mu.Unlock()

	for _, cancel := range contexts {
		cancel()
	}
}

// contextForSender returns a context that will be cancelled when the given sender disconnects,
// plus a release function that removes only this request's tracker entry when it completes.
// If the client has already disconnected, returns an already-cancelled context.
func (t *clientTracker) contextForSender(parent context.Context, sender string) (context.Context, func()) {
	t.mu.Lock()
	t.nextID++
	id := t.nextID
	ctx, cancel := context.WithCancel(parent)
	if t.clients == nil {
		t.clients = make(map[string]map[uint64]context.CancelFunc)
	}
	if t.clients[sender] == nil {
		t.clients[sender] = make(map[uint64]context.CancelFunc)
	}
	t.clients[sender][id] = cancel
	t.mu.Unlock()

	var releaseOnce sync.Once
	release := func() {
		releaseOnce.Do(func() {
			t.mu.Lock()
			defer t.mu.Unlock()
			contexts, ok := t.clients[sender]
			if !ok {
				return
			}
			delete(contexts, id)
			if len(contexts) == 0 {
				delete(t.clients, sender)
			}
		})
	}

	// Check if the client is still connected. This handles the race where:
	// 1. Client sends request then immediately disconnects
	// 2. NameOwnerChanged signal is processed before we added sender to clients map
	// 3. We miss the disconnect signal because sender wasn't tracked yet
	// By checking now, we detect this case and cancel immediately.
	var owner string
	err := t.conn.Object("org.freedesktop.DBus", "/org/freedesktop/DBus").
		Call("org.freedesktop.DBus.GetNameOwner", 0, sender).
		Store(&owner)
	if err != nil {
		// Client already disconnected - cancel the context immediately
		release()
		cancel()
	}

	return ctx, release
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
	// Cancel all pending contexts
	var cancels []context.CancelFunc
	for _, contexts := range t.clients {
		for _, cancel := range contexts {
			cancels = append(cancels, cancel)
		}
	}
	t.clients = nil
	t.mu.Unlock()

	for _, cancel := range cancels {
		cancel()
	}
}
