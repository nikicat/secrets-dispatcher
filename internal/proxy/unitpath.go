package proxy

import (
	"strings"
)

// DecodeUnitPath decodes a systemd unit path to extract the unit name.
// D-Bus encodes special characters in object paths:
//   - _2d → -
//   - _2e → .
//   - _5f → _
//
// Example: /org/freedesktop/systemd1/unit/ssh_2eservice → ssh.service
func DecodeUnitPath(path string) string {
	// Extract the unit name from the path
	const prefix = "/org/freedesktop/systemd1/unit/"
	if !strings.HasPrefix(path, prefix) {
		return ""
	}

	encoded := path[len(prefix):]
	return decodeDBusPath(encoded)
}

// decodeDBusPath decodes D-Bus path encoding.
func decodeDBusPath(s string) string {
	var result strings.Builder
	result.Grow(len(s))

	for i := 0; i < len(s); i++ {
		if s[i] == '_' && i+2 < len(s) {
			// Try to decode the hex sequence
			hex := s[i+1 : i+3]
			if decoded, ok := decodeHex(hex); ok {
				result.WriteByte(decoded)
				i += 2
				continue
			}
		}
		result.WriteByte(s[i])
	}

	return result.String()
}

// decodeHex decodes a two-character hex string to a byte.
func decodeHex(hex string) (byte, bool) {
	if len(hex) != 2 {
		return 0, false
	}

	high, ok1 := hexValue(hex[0])
	low, ok2 := hexValue(hex[1])
	if !ok1 || !ok2 {
		return 0, false
	}

	return high<<4 | low, true
}

// hexValue returns the numeric value of a hex digit.
func hexValue(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}
