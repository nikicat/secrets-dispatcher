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

func TestLogLevelUnmarshalText(t *testing.T) {
	var l LogLevel
	require.NoError(t, l.UnmarshalText([]byte("warn")))
	assert.Equal(t, LogWarn, l)
	assert.EqualError(t, l.UnmarshalText([]byte("verbose")), `log level must be debug, info, warn or error, got "verbose"`)
	require.NoError(t, l.UnmarshalText(nil))
	assert.Empty(t, l, "an empty text is unset")
}

func TestLogFormatUnmarshalText(t *testing.T) {
	var f LogFormat
	require.NoError(t, f.UnmarshalText([]byte("json")))
	assert.Equal(t, LogJSON, f)
	assert.Error(t, f.UnmarshalText([]byte("logfmt")))
	require.NoError(t, f.UnmarshalText(nil))
	assert.Empty(t, f, "an empty text is unset")
}

func TestAlternatives(t *testing.T) {
	assert.Equal(t, "debug, info, warn or error", LogLevels.String())
	assert.Equal(t, "text or json", LogFormats.String())
	assert.Equal(t, "text", Alternatives([]LogFormat{LogText}))
}

func TestEveryLogLevelHasSlogLevel(t *testing.T) {
	for _, l := range LogLevels.Values() {
		assert.Contains(t, slogLevels, l)
	}
}
