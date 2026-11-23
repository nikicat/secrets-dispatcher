package proxy

import (
	"strings"

	"github.com/nikicat/secrets-dispatcher/internal/systemd"
)

// Returns the unit that the given systemd unit object path names, such as
// ssh.service for .../unit/ssh_2eservice. False when the path is not a unit path.
func DecodeUnitPath(path string) (systemd.Unit, bool) {
	encoded, ok := strings.CutPrefix(path, "/org/freedesktop/systemd1/unit/")
	if !ok || encoded == "" {
		return "", false
	}
	return systemd.Unit(decodeDBusPath(encoded)), true
}

// Decodes the _XX hex escapes of a D-Bus path element.
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

// Returns the byte that two hex digits spell.
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

// Returns the value of a hex digit.
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
