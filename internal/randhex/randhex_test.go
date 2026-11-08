package randhex

import (
	"encoding/hex"
	"testing"
)

func TestString(t *testing.T) {
	a, err := String(16)
	if err != nil {
		t.Fatalf("String: %v", err)
	}
	if len(a) != 32 {
		t.Errorf("len(String(16)) = %d, want 32", len(a))
	}
	if _, err := hex.DecodeString(a); err != nil {
		t.Errorf("String(16) = %q, not hex: %v", a, err)
	}
	b, err := String(16)
	if err != nil {
		t.Fatalf("String: %v", err)
	}
	if a == b {
		t.Errorf("two calls returned the same value %q", a)
	}
}
