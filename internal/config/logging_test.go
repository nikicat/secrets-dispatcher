package config

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogLevelLevel(t *testing.T) {
	for _, tc := range []struct {
		in   LogLevel
		want slog.Level
	}{
		{"", slog.LevelInfo},
		{LogDebug, slog.LevelDebug},
		{LogInfo, slog.LevelInfo},
		{LogWarn, slog.LevelWarn},
		{LogError, slog.LevelError},
	} {
		assert.Equal(t, tc.want, tc.in.Level(), "LogLevel(%q).Level()", tc.in)
	}
}

func TestLogLevelSet(t *testing.T) {
	var l LogLevel
	require.NoError(t, l.Set("warn"))
	assert.Equal(t, LogWarn, l)
	assert.Error(t, l.Set(""))
	assert.Error(t, l.Set("verbose"))
}

func TestLogFormatSet(t *testing.T) {
	var f LogFormat
	require.NoError(t, f.Set("json"))
	assert.Equal(t, LogJSON, f)
	assert.Error(t, f.Set(""))
	assert.Error(t, f.Set("logfmt"))
}

func TestAlternatives(t *testing.T) {
	assert.Equal(t, "debug, info, warn or error", Alternatives(LogLevels))
	assert.Equal(t, "text or json", Alternatives(LogFormats))
	assert.Equal(t, "text", Alternatives([]LogFormat{LogText}))
}

func TestEveryLogLevelHasSlogLevel(t *testing.T) {
	for _, l := range LogLevels {
		assert.Contains(t, slogLevels, l)
	}
}
