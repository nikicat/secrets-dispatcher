// Package strs converts between string types.
package strs

// Returns values as plain strings.
func Of[T ~string](values []T) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = string(v)
	}
	return out
}
