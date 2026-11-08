// Package randhex makes random identifiers and secrets as hex text.
package randhex

import (
	"crypto/rand"
	"encoding/hex"
)

// Returns n random bytes from crypto/rand, hex-encoded into 2n characters.
// Errors when the system's random source fails.
func String(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
