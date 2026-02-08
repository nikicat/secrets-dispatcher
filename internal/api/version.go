package api

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
)

// BuildVersion is computed from embedded assets at init.
// Empty string indicates dev mode (no embedded assets).
// Can be overridden via TEST_BUILD_VERSION environment variable for testing.
var BuildVersion string

func init() {
	if override := os.Getenv("TEST_BUILD_VERSION"); override != "" {
		BuildVersion = override
	} else {
		BuildVersion = computeVersion()
	}
}

func computeVersion() string {
	content, err := fs.ReadFile(WebAssets, "web/dist/index.html")
	if err != nil {
		return "" // Empty version = dev mode, skip checks
	}
	hash := sha256.Sum256(content)
	return hex.EncodeToString(hash[:])[:12]
}
