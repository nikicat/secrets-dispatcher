package main

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/config"
)

// Returns the path of a config file that holds body.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

func TestSettingsFlagsOverConfigFile(t *testing.T) {
	path := writeConfig(t, `
listen: 127.0.0.1:1111
serve:
  log_level: warn
  timeout: 1m
  notifications: false
`)
	cfg := newServeFlags().parse([]string{"--config", path, "--listen", "127.0.0.1:2222", "--notifications"})

	assert.Equal(t, config.ListenAddr("127.0.0.1:2222"), cfg.Listen, "a flag wins over the file")
	require.NotNil(t, cfg.Serve.Notifications)
	assert.True(t, *cfg.Serve.Notifications, "a flag wins over the file")
	assert.Equal(t, config.LogWarn, cfg.Serve.LogLevel, "the file wins over an unset flag")
	assert.Equal(t, config.Duration(time.Minute), cfg.Serve.Timeout, "the file wins over an unset flag")
	assert.Empty(t, cfg.Serve.LogFormat, "unset in both, left for WithDefaults")
}

func TestSettingsFlagsWithoutConfigFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := newServeFlags().parse([]string{"--log-format", "json"})

	assert.Equal(t, config.LogJSON, cfg.Serve.LogFormat)
	assert.Empty(t, cfg.Listen)
	assert.Nil(t, cfg.Serve.Notifications)
}

func TestNegatedBool(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{nil, true},
		{[]string{"--no-start"}, false},
		{[]string{"--start=false"}, false},
		{[]string{"--no-start=false"}, true},
	} {
		fs := flag.NewFlagSet("install", flag.ContinueOnError)
		var start bool
		startFlags(fs, &start)
		require.NoError(t, fs.Parse(tc.args))
		assert.Equal(t, tc.want, start, "%v", tc.args)
	}
}

func TestServeArgsRoundTrip(t *testing.T) {
	source, err := config.Path(filepath.Join(t.TempDir(), "config.yaml")).OrDefault()
	require.NoError(t, err)
	file := source.File
	args := config.ServeArgs(file)

	fs := newServeFlags()
	assert.Equal(t, fs.Name(), args[0], "the subcommand that main dispatches to serve")
	require.NoError(t, fs.Parse(args[1:]))
	assert.Equal(t, file.Path(), fs.path)
}
