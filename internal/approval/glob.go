package approval

import (
	"errors"
	"fmt"
	"path"
)

// A shell pattern, as path.Match reads it. Trust rules use globs, not regular
// expressions (ADR 0005). The zero Glob is unset, and every other Glob holds a
// pattern that path.Match accepts, so Matches has no error to drop.
type Glob struct {
	pattern string
}

var errEmptyGlob = errors.New("empty glob; omit the key to match anything")

// Errors when s is empty or is not a pattern that path.Match accepts.
func ParseGlob(s string) (Glob, error) {
	if s == "" {
		return Glob{}, errEmptyGlob
	}
	if _, err := path.Match(s, ""); err != nil {
		return Glob{}, fmt.Errorf("invalid glob %q: %w", s, err)
	}
	return Glob{pattern: s}, nil
}

// Returns the glob for a pattern written in code. Panics when ParseGlob
// rejects it.
func MustGlob(s string) Glob {
	g, err := ParseGlob(s)
	if err != nil {
		panic(err)
	}
	return g
}

// Checks whether g is unset. The omitempty and omitzero tags use it to leave an
// unset glob out of YAML and JSON.
func (g Glob) IsZero() bool {
	return g.pattern == ""
}

func (g Glob) String() string {
	return g.pattern
}

func (g Glob) MarshalText() ([]byte, error) {
	return []byte(g.pattern), nil
}

// Errors as ParseGlob does.
func (g *Glob) UnmarshalText(text []byte) error {
	parsed, err := ParseGlob(string(text))
	if err != nil {
		return err
	}
	*g = parsed
	return nil
}

// Checks whether the pattern matches all of s. The zero Glob matches only the
// empty string.
func (g Glob) Matches(s string) bool {
	ok, _ := path.Match(g.pattern, s)
	return ok
}
