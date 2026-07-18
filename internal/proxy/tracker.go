// Package proxy implements the Secret Service proxy.
package proxy

import (
	"context"
	"sync"

	"github.com/godbus/dbus/v5"
)

// senderName is a D-Bus sender's unique connection name (e.g., ":1.123").
type senderName string

// requestID identifies a single in-flight request within a sender's set.
type requestID uint64

// clientTracker tracks D-Bus clients and provides contexts that get cancelled
// when the client disconnects.
type clientTracker struct {
	conn *dbus.Conn
	mu   sync.Mutex
	// clients maps a sender unique name to the set of its in-flight requests,
	// each keyed by a tracker-assigned id. A single sender can have several
	// approval requests pending at once — a client may issue overlapping Secret
	// Service calls on one connection — and each is tracked and cancelled
	// independently. All of a sender's requests are cancelled together only when
	// that sender actually disconnects.
	clients map[senderName]map[requestID]context.CancelFunc
	// nextID assigns a unique id to each request; only accessed under mu.
	nextID    requestID
	signals   chan *dbus.Signal
	done      chan struct{}
	closeOnce sync.Once
}

// newClientTracker creates a new tracker and starts listening for NameOwnerChanged signals.
func newClientTracker(conn *dbus.Conn) (*clientTracker, error) {
	t := &clientTracker{
		conn:    conn,
		clients: make(map[senderName]map[requestID]context.CancelFunc),
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
				t.clientDisconnected(senderName(oldOwner))
			}
		}
	}
}

// clientDisconnected is called when a client disconnects. It cancels every
// request still pending for that sender — this is the only place that cancels a
// sender's requests wholesale.
func (t *clientTracker) clientDisconnected(sender senderName) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for _, cancel := range t.clients[sender] {
		cancel()
	}
	delete(t.clients, sender)
}

// contextForSender registers a single request for the given sender and returns
// its context together with a release function. The context is cancelled when
// the sender disconnects (or when release is called); release cancels and
// forgets only this one request, leaving the sender's other in-flight requests
// untouched. Each call registers an independent request, so overlapping
// requests from the same sender never cancel one another.
//
// The context should be used for the duration of a single request, and release
// called (typically deferred) when it completes. If the client has already
// disconnected, the returned context is already cancelled.
func (t *clientTracker) contextForSender(parent context.Context, sender senderName) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)

	t.mu.Lock()
	id := t.nextID
	t.nextID++
	reqs := t.clients[sender]
	if reqs == nil {
		reqs = make(map[requestID]context.CancelFunc)
		t.clients[sender] = reqs
	}
	reqs[id] = cancel
	t.mu.Unlock()

	release := func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		reqs, ok := t.clients[sender]
		if !ok {
			return
		}
		if c, ok := reqs[id]; ok {
			c()
			delete(reqs, id)
			if len(reqs) == 0 {
				delete(t.clients, sender)
			}
		}
	}

	// Check if the client is still connected. This handles the race where:
	// 1. Client sends request then immediately disconnects
	// 2. NameOwnerChanged signal is processed before we added sender to clients map
	// 3. We miss the disconnect signal because sender wasn't tracked yet
	// By checking now, we detect this case and cancel this request immediately.
	var owner string
	err := t.conn.Object("org.freedesktop.DBus", "/org/freedesktop/DBus").
		Call("org.freedesktop.DBus.GetNameOwner", 0, string(sender)).
		Store(&owner)
	if err != nil {
		// Client already disconnected - cancel this request immediately.
		release()
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
	defer t.mu.Unlock()

	// Cancel all pending contexts
	for _, reqs := range t.clients {
		for _, cancel := range reqs {
			cancel()
		}
	}
	t.clients = nil
}
