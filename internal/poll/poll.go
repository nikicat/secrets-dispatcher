// Package poll waits for a condition by checking it at a fixed interval.
package poll

import (
	"context"
	"time"
)

// Reports whether cond returned true. cond is checked every interval, and the
// last check can come up to one interval after timeout, so Until can return
// that much late. Returns false as soon as ctx is done.
func Until(ctx context.Context, timeout, interval time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-tick.C:
		}
	}
}
