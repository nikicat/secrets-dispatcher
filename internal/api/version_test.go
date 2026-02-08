package api

import (
	"testing"
)

func TestBuildVersionNonEmpty(t *testing.T) {
	// BuildVersion should be non-empty when assets are embedded
	if BuildVersion == "" {
		t.Skip("No embedded assets (dev mode)")
	}
	if len(BuildVersion) != 12 {
		t.Errorf("expected 12-char hash, got %d chars", len(BuildVersion))
	}
}

func TestBuildVersionConsistent(t *testing.T) {
	// Multiple calls should return same value
	v1 := computeVersion()
	v2 := computeVersion()
	if v1 != v2 {
		t.Errorf("version not consistent: %s != %s", v1, v2)
	}
}
