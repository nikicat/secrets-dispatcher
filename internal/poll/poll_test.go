package poll

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestUntilReturnsAtOnceWhenCondHolds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		calls := 0
		assert.True(t, Until(t.Context(), time.Second, 100*time.Millisecond, func() bool { calls++; return true }))
		assert.Equal(t, 1, calls)
		assert.Zero(t, time.Since(start))
	})
}

func TestUntilChecksEveryInterval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		calls := 0
		assert.True(t, Until(t.Context(), time.Second, 100*time.Millisecond, func() bool { calls++; return calls == 3 }))
		assert.Equal(t, 200*time.Millisecond, time.Since(start))
	})
}

func TestUntilGivesUpAfterTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		calls := 0
		assert.False(t, Until(t.Context(), time.Second, 300*time.Millisecond, func() bool { calls++; return false }))
		// Checks at 0, 300, 600, 900 and 1200ms; the last is past the timeout.
		assert.Equal(t, 5, calls)
		assert.Equal(t, 1200*time.Millisecond, time.Since(start))
	})
}

func TestUntilChecksOnceAfterTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		assert.True(t, Until(t.Context(), time.Second, 300*time.Millisecond, func() bool { return time.Since(start) > time.Second }))
	})
}

func TestUntilStopsWhenContextEnds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 250*time.Millisecond)
		defer cancel()
		start := time.Now()
		assert.False(t, Until(ctx, time.Second, 100*time.Millisecond, func() bool { return false }))
		assert.Equal(t, 250*time.Millisecond, time.Since(start))
	})
}
