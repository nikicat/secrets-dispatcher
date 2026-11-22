// Package delaygroup provides delayed callbacks, one per key, that can be
// cancelled before they run.
package delaygroup

import (
	"sync"
	"time"
)

// A set of delayed callbacks, at most one per key. Safe for concurrent use.
type Group[K comparable] struct {
	mu     sync.Mutex
	timers map[K]*time.Timer
}

func New[K comparable]() *Group[K] {
	return &Group[K]{timers: make(map[K]*time.Timer)}
}

// Runs the given callback after the delay, unless the key is cancelled first.
// It replaces the callback already scheduled for the key, which then does not
// run.
func (g *Group[K]) Schedule(key K, delay time.Duration, fn func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if old, ok := g.timers[key]; ok {
		old.Stop()
	}
	var t *time.Timer
	t = time.AfterFunc(delay, func() {
		g.mu.Lock()
		current := g.timers[key] == t
		if current {
			delete(g.timers, key)
		}
		g.mu.Unlock()
		if current {
			fn()
		}
	})
	g.timers[key] = t
}

// Stops the callback scheduled for the key. Reports whether one was waiting to
// run.
func (g *Group[K]) Cancel(key K) bool {
	g.mu.Lock()
	timer, ok := g.timers[key]
	if ok {
		delete(g.timers, key)
	}
	g.mu.Unlock()
	if ok {
		timer.Stop()
	}
	return ok
}
