// Package expiryset provides a set whose keys expire.
package expiryset

import (
	"maps"
	"time"
)

// A set of keys, each held until its expiry. The zero value is an empty set.
// Not safe for concurrent use.
type Set[K comparable] struct {
	expiries map[K]time.Time
}

// Adds the given key to the set until the given expiry. Reports false, and adds
// nothing, when the expiry has passed or when the key is already in the set
// and has not expired. A key expires once time.Now reaches its expiry.
func (s *Set[K]) Add(key K, expires time.Time) bool {
	if passed(expires, time.Now()) || s.Has(key) {
		return false
	}
	s.Put(key, expires)
	return true
}

// Holds the given key in the set until the given expiry, replacing the expiry
// it had.
func (s *Set[K]) Put(key K, expires time.Time) {
	s.dropExpired(time.Now())
	if s.expiries == nil {
		s.expiries = make(map[K]time.Time)
	}
	s.expiries[key] = expires
}

// Checks whether the given key is in the set and has not expired.
func (s *Set[K]) Has(key K) bool {
	exp, ok := s.expiries[key]
	return ok && !passed(exp, time.Now())
}

// Removes every key for which the given function reports true.
func (s *Set[K]) DeleteFunc(del func(K) bool) {
	maps.DeleteFunc(s.expiries, func(k K, _ time.Time) bool { return del(k) })
}

func (s *Set[K]) dropExpired(now time.Time) {
	maps.DeleteFunc(s.expiries, func(_ K, exp time.Time) bool { return passed(exp, now) })
}

func passed(expiry, now time.Time) bool {
	return !now.Before(expiry)
}
