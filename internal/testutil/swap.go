package testutil

import "testing"

// Sets *p to v until the test ends.
func Swap[T any](t testing.TB, p *T, v T) {
	t.Helper()
	orig := *p
	*p = v
	t.Cleanup(func() { *p = orig })
}
