package delaygroup

import (
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScheduleRunsAfterDelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := New[string]()
		var ran atomic.Bool
		g.Schedule("a", time.Second, func() { ran.Store(true) })
		time.Sleep(time.Second - time.Nanosecond)
		synctest.Wait()
		require.False(t, ran.Load(), "callback ran before the delay")
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		require.True(t, ran.Load(), "callback did not run after the delay")
		assert.False(t, g.Cancel("a"), "Cancel after the callback ran reported true")
	})
}

func TestCancelSuppresses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := New[string]()
		var ran atomic.Bool
		g.Schedule("a", time.Second, func() { ran.Store(true) })
		assert.True(t, g.Cancel("a"), "Cancel of a waiting callback reported false")
		assert.False(t, g.Cancel("a"), "second Cancel reported true")
		time.Sleep(2 * time.Second)
		synctest.Wait()
		assert.False(t, ran.Load(), "cancelled callback ran")
	})
}

func TestScheduleReplaces(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := New[string]()
		var oldRan, newRan atomic.Bool
		g.Schedule("a", time.Second, func() { oldRan.Store(true) })
		g.Schedule("a", 2*time.Second, func() { newRan.Store(true) })
		time.Sleep(3 * time.Second)
		synctest.Wait()
		assert.False(t, oldRan.Load(), "replaced callback ran")
		assert.True(t, newRan.Load(), "replacing callback did not run")
	})
}

func TestKeysAreIndependent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := New[int]()
		var ran1, ran2 atomic.Bool
		g.Schedule(1, time.Second, func() { ran1.Store(true) })
		g.Schedule(2, time.Second, func() { ran2.Store(true) })
		g.Cancel(1)
		time.Sleep(2 * time.Second)
		synctest.Wait()
		assert.False(t, ran1.Load(), "cancelled key 1 ran")
		assert.True(t, ran2.Load(), "key 2 did not run")
	})
}
