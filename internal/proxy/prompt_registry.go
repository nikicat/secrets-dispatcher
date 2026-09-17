package proxy

import (
	"context"
	"fmt"
	"sync"

	"github.com/godbus/dbus/v5"
)

// promptToken identifies one lifetime of a backend prompt path. Backend object
// paths may be reused after a prompt completes, so asynchronous cleanup must
// not remove a later registration at the same path.
type promptToken uint64

type promptLease struct {
	token   promptToken
	owner   senderName
	release func()
}

// promptRegistry binds backend prompt objects to the front-bus connection that
// received them. Each lease is also registered with clientTracker so it is
// removed when that connection disappears.
type promptRegistry struct {
	mu      sync.Mutex
	tracker *clientTracker
	next    promptToken
	owners  map[dbus.ObjectPath]promptLease
	closed  bool
}

func newPromptRegistry(tracker *clientTracker) *promptRegistry {
	return &promptRegistry{
		tracker: tracker,
		owners:  make(map[dbus.ObjectPath]promptLease),
	}
}

// register binds path to owner. The root path means that no prompt is needed.
// A live path is never transferred to a different sender: if a backend reuses
// an active path, fail closed rather than allowing one client to steal another
// client's prompt.
func (r *promptRegistry) register(path dbus.ObjectPath, owner senderName) error {
	if path == "/" {
		return nil
	}
	if !isPromptPath(path) {
		return fmt.Errorf("backend returned invalid prompt path %q", path)
	}
	if owner == "" {
		return fmt.Errorf("cannot register prompt %q without a D-Bus sender", path)
	}

	// Avoid adding a tracker lifetime for the common duplicate/conflict case.
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return fmt.Errorf("prompt registry is closed")
	}
	if current, ok := r.owners[path]; ok {
		r.mu.Unlock()
		if current.owner == owner {
			return nil
		}
		return fmt.Errorf("prompt %q is already owned by a different sender", path)
	}
	r.mu.Unlock()

	ctx, release := r.tracker.contextForSender(context.Background(), owner)

	// Recheck after contextForSender: another prompt-producing call may have
	// registered the path while GetNameOwner was in flight.
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		release()
		return fmt.Errorf("prompt registry is closed")
	}
	if current, ok := r.owners[path]; ok {
		r.mu.Unlock()
		release()
		if current.owner == owner {
			return nil
		}
		return fmt.Errorf("prompt %q is already owned by a different sender", path)
	}
	r.next++
	lease := promptLease{token: r.next, owner: owner, release: release}
	r.owners[path] = lease
	r.mu.Unlock()

	// contextForSender also handles the disconnect-before-registration race.
	// The token prevents this callback from removing a later reuse of path.
	context.AfterFunc(ctx, func() {
		r.unregisterIf(path, lease.token)
	})
	return nil
}

func (r *promptRegistry) lookup(path dbus.ObjectPath) (promptLease, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	lease, ok := r.owners[path]
	return lease, ok
}

// complete removes the current lifetime for path. Prompt.Completed is the
// authoritative normal end of a prompt, including prompts dismissed by the
// client.
func (r *promptRegistry) complete(path dbus.ObjectPath) {
	r.mu.Lock()
	lease, ok := r.owners[path]
	if ok {
		delete(r.owners, path)
	}
	r.mu.Unlock()
	if ok {
		lease.release()
	}
}

func (r *promptRegistry) unregisterIf(path dbus.ObjectPath, token promptToken) {
	r.mu.Lock()
	lease, ok := r.owners[path]
	if ok && lease.token == token {
		delete(r.owners, path)
	} else {
		ok = false
	}
	r.mu.Unlock()
	if ok {
		lease.release()
	}
}

func (r *promptRegistry) close() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	leases := make([]promptLease, 0, len(r.owners))
	for path, lease := range r.owners {
		leases = append(leases, lease)
		delete(r.owners, path)
	}
	r.mu.Unlock()

	for _, lease := range leases {
		lease.release()
	}
}
