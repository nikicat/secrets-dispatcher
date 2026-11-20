// Package poll waits for a condition by checking it at a fixed interval.
package poll

import "time"

// Reports whether cond returned true. cond is checked every interval, and the
// last check can come up to one interval after timeout, so Until can return
// that much late.
func Until(timeout, interval time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(interval)
	}
}
