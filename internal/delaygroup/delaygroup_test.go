package delaygroup

import (
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

func TestScheduleRunsAfterDelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := New[string]()
		var ran atomic.Bool
		g.Schedule("a", time.Second, func() { ran.Store(true) })
		time.Sleep(time.Second - time.Nanosecond)
		synctest.Wait()
		if ran.Load() {
			t.Fatal("callback ran before the delay")
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if !ran.Load() {
			t.Fatal("callback did not run after the delay")
		}
		if g.Cancel("a") {
			t.Error("Cancel after the callback ran reported true")
		}
	})
}

func TestCancelSuppresses(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		g := New[string]()
		var ran atomic.Bool
		g.Schedule("a", time.Second, func() { ran.Store(true) })
		if !g.Cancel("a") {
			t.Error("Cancel of a waiting callback reported false")
		}
		if g.Cancel("a") {
			t.Error("second Cancel reported true")
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if ran.Load() {
			t.Error("cancelled callback ran")
		}
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
		if oldRan.Load() || !newRan.Load() {
			t.Errorf("old ran %v, new ran %v; want only the new callback", oldRan.Load(), newRan.Load())
		}
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
		if ran1.Load() || !ran2.Load() {
			t.Errorf("key 1 ran %v, key 2 ran %v; want only key 2", ran1.Load(), ran2.Load())
		}
	})
}
