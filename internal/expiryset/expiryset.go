// Package expiryset provides a set whose keys expire.
package expiryset

import "time"

// A set of keys, each held until its expiry. The zero value is an empty set.
// Not safe for concurrent use.
type Set[K comparable] struct {
	expiries map[K]time.Time
}

// Adds key to the set until the given expiry. Reports false, and adds nothing,
// when the expiry has passed or when key is already in the set and has not
// expired. A key expires once time.Now reaches its expiry.
func (s *Set[K]) Add(key K, expires time.Time) bool {
	now := time.Now()
	if !now.Before(expires) {
		return false
	}
	for k, exp := range s.expiries {
		if !now.Before(exp) {
			delete(s.expiries, k)
		}
	}
	if _, ok := s.expiries[key]; ok {
		return false
	}
	if s.expiries == nil {
		s.expiries = make(map[K]time.Time)
	}
	s.expiries[key] = expires
	return true
}
