package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			got, err := DataHome()
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestXDGBaseNoHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	got, err := DataHome()
	assert.Error(t, err, "DataHome() = %q", got)
}
