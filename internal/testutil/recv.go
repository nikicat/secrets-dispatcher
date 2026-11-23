package testutil

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Returns the next value from the channel, and stops the test with a failure
// when none arrives within the given time.
func Recv[T any](t testing.TB, ch <-chan T, within time.Duration, msgAndArgs ...any) T {
	t.Helper()
	return RecvMatching(t, ch, within, func(T) bool { return true }, msgAndArgs...)
}

// Works as Recv does, but skips values until one satisfies the predicate. The
// bound covers the whole wait, not each value.
func RecvMatching[T any](t testing.TB, ch <-chan T, within time.Duration, match func(T) bool, msgAndArgs ...any) T {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case v := <-ch:
			if match(v) {
				return v
			}
		case <-deadline:
			require.FailNow(t, fmt.Sprintf("nothing received within %v", within), msgAndArgs...)
			var zero T
			return zero
		}
	}
}

// Fails the test at once when the channel is ready to receive from, without
// waiting.
func NoRecv[T any](t testing.TB, ch <-chan T, msgAndArgs ...any) {
	t.Helper()
	select {
	case <-ch:
		require.FailNow(t, "channel ready to receive from", msgAndArgs...)
	default:
	}
}
