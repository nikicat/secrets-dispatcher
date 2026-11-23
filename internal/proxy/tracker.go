package proxy

import (
	"context"
	"sync"

	"github.com/godbus/dbus/v5"

	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/sender"
)

// The bus daemon's signal that a name changed owner.
const nameOwnerChanged = "NameOwnerChanged"

// Identifies a single in-flight request within a sender's set.
type requestID uint64

type clientTracker struct {
	conn      *dbus.Conn
	signals   chan *dbus.Signal
	done      chan struct{}
	closeOnce sync.Once

	mu sync.Mutex
	// In-flight requests by sender. A sender can have several at once, since a
	// client may make overlapping calls on one connection.
	clients map[sender.UniqueName]map[requestID]context.CancelFunc
	nextID  requestID
	closed  bool
}

// Returns a tracker that watches the given connection for NameOwnerChanged.
func newClientTracker(conn *dbus.Conn) (*clientTracker, error) {
	t := &clientTracker{
		conn:    conn,
		clients: make(map[sender.UniqueName]map[requestID]context.CancelFunc),
		signals: make(chan *dbus.Signal, 16),
		done:    make(chan struct{}),
	}

	// Subscribe to NameOwnerChanged signals to detect client disconnects
	if err := conn.AddMatchSignal(
		dbus.WithMatchInterface(string(dbustypes.BusDaemonInterface)),
		dbus.WithMatchMember(nameOwnerChanged),
		dbus.WithMatchSender(dbustypes.BusDaemonName),
	); err != nil {
		return nil, err
	}

	conn.Signal(t.signals)

	// Start goroutine to process signals
	go t.processSignals()

	return t, nil
}

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

			if signal.Name != dbustypes.BusDaemonInterface.Member(nameOwnerChanged) {
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
				t.clientDisconnected(sender.UniqueName(oldOwner))
			}
		}
	}
}

// Cancels every request still pending for sender once it disconnects. Nothing
// else cancels a sender's requests wholesale.
func (t *clientTracker) clientDisconnected(sender sender.UniqueName) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for _, cancel := range t.clients[sender] {
		cancel()
	}
	delete(t.clients, sender)
}

// Returns a context for one request from the sender, and a release that cancels
// only that request. The context is also cancelled when the sender disconnects.
// When the sender has already gone or the tracker is closed, the context is
// cancelled from the start.
func (t *clientTracker) contextForSender(parent context.Context, sender sender.UniqueName) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)

	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		cancel()
		return ctx, cancel
	}
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

	// Its NameOwnerChanged may have come before the sender was tracked.
	if _, err := (busDaemon{t.conn}).nameOwner(string(sender)); err != nil {
		release()
	}

	return ctx, release
}

// Stops watching and cancels all requests in flight.
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
	clear(t.clients)
	t.closed = true
}
