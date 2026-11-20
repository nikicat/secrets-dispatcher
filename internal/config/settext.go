package config

import (
	"encoding"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Sets u from a command-line flag value. Errors when value is empty, or when
// u.UnmarshalText rejects it.
func setText(u encoding.TextUnmarshaler, value string) error {
	if value == "" {
		return errors.New("empty")
	}
	return u.UnmarshalText([]byte(value))
}

// Returns text as one of valid. An empty text is unset, for WithDefaults to
// fill. Errors when text is none of valid; what names the setting.
func parseEnum[T ~string](text []byte, valid []T, what string) (T, error) {
	v := T(text)
	if v != "" && !slices.Contains(valid, v) {
		return "", fmt.Errorf("%s must be %s, got %q", what, Alternatives(valid), v)
	}
	return v, nil
}

// Returns values joined as "a, b, c or d".
func Alternatives[T ~string](values []T) string {
	names := make([]string, len(values))
	for i, v := range values {
		names[i] = string(v)
	}
	last := len(names) - 1
	if last < 1 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:last], ", ") + " or " + names[last]
}
