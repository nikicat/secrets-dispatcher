package expiryset

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestAddRejectsUnexpiredKey(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var s Set[string]
		assert.True(t, s.Add("a", time.Now().Add(time.Minute)))
		assert.False(t, s.Add("a", time.Now().Add(time.Hour)))
		assert.True(t, s.Add("b", time.Now().Add(time.Minute)))
	})
}

func TestAddAcceptsKeyAtItsExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var s Set[string]
		assert.True(t, s.Add("a", time.Now().Add(time.Minute)))
		time.Sleep(time.Minute - time.Nanosecond)
		assert.False(t, s.Add("a", time.Now().Add(time.Minute)))
		time.Sleep(time.Nanosecond)
		assert.True(t, s.Add("a", time.Now().Add(time.Minute)))
	})
}

func TestAddDropsExpiredKeys(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var s Set[int]
		for i := range 3 {
			s.Add(i, time.Now().Add(time.Second))
		}
		s.Add(99, time.Now().Add(time.Hour))
		time.Sleep(time.Second)
		s.Add(100, time.Now().Add(time.Hour))
		assert.Len(t, s.expiries, 2)
	})
}

func TestAddRejectsPastExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var s Set[string]
		assert.False(t, s.Add("a", time.Now()))
		assert.False(t, s.Add("a", time.Now().Add(-time.Second)))
		assert.Empty(t, s.expiries)
		assert.True(t, s.Add("a", time.Now().Add(time.Minute)))
	})
}

func TestPutRefreshesAndHasExpires(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var s Set[string]
		assert.False(t, s.Has("a"))
		s.Put("a", time.Now().Add(time.Minute))
		s.Put("a", time.Now().Add(2*time.Minute))
		time.Sleep(time.Minute)
		assert.True(t, s.Has("a"))
		time.Sleep(time.Minute)
		assert.False(t, s.Has("a"))
	})
}

func TestPutDropsExpiredKeys(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var s Set[int]
		s.Put(1, time.Now().Add(time.Second))
		time.Sleep(time.Second)
		s.Put(2, time.Now().Add(time.Second))
		assert.Len(t, s.expiries, 1)
	})
}

func TestDeleteFunc(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var s Set[int]
		for i := range 4 {
			s.Put(i, time.Now().Add(time.Minute))
		}
		s.DeleteFunc(func(k int) bool { return k%2 == 0 })
		assert.False(t, s.Has(0))
		assert.True(t, s.Has(1))
		assert.False(t, s.Has(2))
		assert.True(t, s.Has(3))
	})
}
