package config

import (
	"fmt"
	"slices"
	"strings"

	"github.com/nikicat/secrets-dispatcher/internal/strs"
)

// The values that a setting may take.
type Enum[T ~string] struct {
	what   string
	values []T
}

// Returns an Enum of the given values. Its errors name the setting with what,
// such as "log level".
func NewEnum[T ~string](what string, values ...T) Enum[T] {
	return Enum[T]{what: what, values: values}
}

// Returns text as one of e's values. An empty text is unset and returns the
// zero value (ARCHITECTURE.md, "Command line and settings"). Errors when text
// is none of e's values.
func (e Enum[T]) Parse(text []byte) (T, error) {
	v := T(text)
	if v != "" && !slices.Contains(e.values, v) {
		return "", e.Unknown(v)
	}
	return v, nil
}

// Returns the error for v, which is none of e's values.
func (e Enum[T]) Unknown(v T) error {
	return fmt.Errorf("%s must be %s, got %q", e.what, e, v)
}

// Returns e's values as Alternatives joins them.
func (e Enum[T]) String() string { return Alternatives(e.values) }

// Returns e's values in order.
func (e Enum[T]) Values() []T { return e.values }

// Returns values joined as "a, b, c or d".
func Alternatives[T ~string](values []T) string {
	names := strs.Of(values)
	last := len(names) - 1
	if last < 1 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:last], ", ") + " or " + names[last]
}
