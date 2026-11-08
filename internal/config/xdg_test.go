package config

import (
	"path/filepath"
	"testing"
)

func TestXDGBase(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{"absolute", "/custom/data", "/custom/data"},
		{"relative is ignored", "relative/data", "/home/u/.local/share"},
		{"unset", "", "/home/u/.local/share"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", "/home/u")
			t.Setenv("XDG_DATA_HOME", tt.value)
			got, err := xdgBase("XDG_DATA_HOME", filepath.Join(".local", "share"))
			if err != nil {
				t.Fatalf("xdgBase: %v", err)
			}
			if got != tt.want {
				t.Errorf("xdgBase() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestXDGBaseNoHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	if got, err := xdgBase("XDG_DATA_HOME", filepath.Join(".local", "share")); err == nil {
		t.Errorf("xdgBase() = %q, want an error", got)
	}
}
