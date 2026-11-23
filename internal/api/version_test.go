package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildVersionNonEmpty(t *testing.T) {
	// BuildVersion should be non-empty when assets are embedded
	if BuildVersion == "" {
		t.Skip("No embedded assets (dev mode)")
	}
	assert.Len(t, BuildVersion, 12, "hash length")
}

func TestBuildVersionConsistent(t *testing.T) {
	// Multiple calls should return same value
	assert.Equal(t, computeVersion(), computeVersion())
}
