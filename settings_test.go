package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/cli"
	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/nikicat/secrets-dispatcher/internal/gpgsign"
	"github.com/nikicat/secrets-dispatcher/internal/service"
	"github.com/nikicat/secrets-dispatcher/internal/unixsock"
)

// Returns the path of a config file that holds body.
func writeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// Returns the command line that args parse into, and the selected command.
func parse(t *testing.T, args ...string) (*commandLine, string) {
	t.Helper()
	var c commandLine
	parser, err := newParser(&c)
	require.NoError(t, err)
	ctx, err := parser.Parse(args)
	require.NoError(t, err)
	return &c, ctx.Command()
}

// Returns the error of parsing args.
func parseError(t *testing.T, args ...string) error {
	t.Helper()
	var c commandLine
	parser, err := newParser(&c)
	require.NoError(t, err)
	_, err = parser.Parse(args)
	return err
}

func TestServeFlagsOverConfigFile(t *testing.T) {
	path := writeConfig(t, `
listen: 127.0.0.1:1111
serve:
  log_level: warn
  timeout: 1m
  notifications: false
`)
	c, _ := parse(t, "serve", "--config", path, "--listen", "127.0.0.1:2222", "--notifications")
	cfg, err := c.Serve.merged()
	require.NoError(t, err)

	assert.Equal(t, config.ListenAddr("127.0.0.1:2222"), cfg.Listen, "a flag wins over the file")
	require.NotNil(t, cfg.Serve.Notifications)
	assert.True(t, *cfg.Serve.Notifications, "a flag wins over the file")
	assert.Equal(t, config.LogWarn, cfg.Serve.LogLevel, "the file wins over an unset flag")
	assert.Equal(t, config.Duration(time.Minute), cfg.Serve.Timeout, "the file wins over an unset flag")
	assert.Empty(t, cfg.Serve.LogFormat, "unset in both, left for WithDefaults")
}

func TestServeFlagsWithoutConfigFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	c, _ := parse(t, "serve", "--log-format", "json", "--timeout", "90s", "--no-notifications")
	cfg, err := c.Serve.merged()
	require.NoError(t, err)

	assert.Equal(t, config.LogJSON, cfg.Serve.LogFormat)
	assert.Equal(t, config.Duration(90*time.Second), cfg.Serve.Timeout)
	require.NotNil(t, cfg.Serve.Notifications)
	assert.False(t, *cfg.Serve.Notifications)
	assert.Empty(t, cfg.Listen)
}

func TestNotificationsFlag(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want *bool
	}{
		{nil, nil},
		{[]string{"--notifications"}, new(true)},
		{[]string{"--no-notifications"}, new(false)},
		{[]string{"--notifications=false"}, new(false)},
	} {
		c, _ := parse(t, append([]string{"serve"}, tc.args...)...)
		assert.Equal(t, tc.want, c.Serve.Notifications, "%v", tc.args)
	}
}

func TestPathFlagsResolveAgainstWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	c, _ := parse(t, "list", "--config", "x.yaml", "--state-dir", "state")

	assert.Equal(t, config.Path(filepath.Join(dir, "x.yaml")), c.List.Config)
	assert.Equal(t, config.StateDir(filepath.Join(dir, "state")), c.List.StateDir)

	c, _ = parse(t, "try", "--config", "base.yaml")
	assert.Equal(t, config.Path(filepath.Join(dir, "base.yaml")), c.Try.Config)
}

func TestEmptyPathFlagsAreUnset(t *testing.T) {
	t.Chdir(t.TempDir())
	c, _ := parse(t, "list", "--config=", "--state-dir=")
	assert.Empty(t, c.List.Config)
	assert.Empty(t, c.List.StateDir)

	c, _ = parse(t, "try", "--config=")
	assert.Empty(t, c.Try.Config)
}

func TestFlagValuesAreValidated(t *testing.T) {
	for _, args := range [][]string{
		{"serve", "--log-level", "verbose"},
		{"serve", "--log-format", "logfmt"},
		{"serve", "--listen", "8484"},
		{"login", "--listen", "8484"},
		{"serve", "--timeout", "soon"},
		{"service", "install", "--mode", "bogus"},
		{"service", "install", "--backend", "keepassxc"},
		{"service", "install", "--backend", ""},
		{"bogus"},
	} {
		assert.Error(t, parseError(t, args...), "%v", args)
	}
}

func TestServiceInstallFlags(t *testing.T) {
	for _, tc := range []struct {
		args      []string
		wantStart bool
	}{
		{nil, true},
		{[]string{"--no-start"}, false},
		{[]string{"--start=false"}, false},
		{[]string{"--start"}, true},
	} {
		c, _ := parse(t, append([]string{"service", "install"}, tc.args...)...)
		assert.Equal(t, tc.wantStart, c.Service.Install.Start, "%v", tc.args)
		assert.Equal(t, service.ModeLocal, c.Service.Install.Mode, "the default mode")
		assert.Nil(t, c.Service.Install.Backend, "no backend leaves it to the provider")
	}

	c, _ := parse(t, "service", "install", "--mode", "full", "--backend", "/opt/backend --flag", "--verbose")
	assert.Equal(t, service.ModeFull, c.Service.Install.Mode)
	assert.Equal(t, "/opt/backend --flag", c.Service.Install.Backend.String())
	assert.Equal(t, service.NarrateChanges, c.Service.Install.narration())
}

func TestProvisionUserDefaultsToSudoUser(t *testing.T) {
	t.Setenv("SUDO_USER", "alice")
	c, _ := parse(t, "provision")
	assert.Equal(t, "alice", c.Provision.User)

	c, _ = parse(t, "provision", "--user", "bob")
	assert.Equal(t, "bob", c.Provision.User, "the flag wins over the environment")
}

func TestRequestCommandsTakeAnID(t *testing.T) {
	c, cmd := parse(t, "approve", "abc", "--json")
	assert.Equal(t, "approve <request-id>", cmd)
	assert.Equal(t, cli.IDPrefix("abc"), c.Approve.ID)
	assert.True(t, c.Approve.JSON)

	assert.Error(t, parseError(t, "deny"), "the ID is required")
}

func TestGPGProgramArgs(t *testing.T) {
	for _, tc := range []struct {
		args   []string
		want   []string
		wantOK bool
	}{
		{[]string{"gpg-sign", "--status-fd=2", "-bsau", "KEY"}, []string{"--status-fd=2", "-bsau", "KEY"}, true},
		{[]string{"gpg-sign"}, []string{}, true},
		{[]string{"gpg-sign", "setup", "--local"}, nil, false},
		{[]string{"serve"}, nil, false},
		{nil, nil, false},
	} {
		got, ok := gpgProgramArgs(tc.args)
		assert.Equal(t, tc.wantOK, ok, "%v", tc.args)
		assert.Equal(t, tc.want, got, "%v", tc.args)
	}

	_, cmd := parse(t, gpgsign.Command, gpgSignSetupCommand)
	assert.Equal(t, gpgsign.Command+" "+gpgSignSetupCommand, cmd, "kong's name for the command that gpgProgramArgs leaves to it")
}

func TestServeArgsRoundTrip(t *testing.T) {
	source, err := config.Path(filepath.Join(t.TempDir(), "config.yaml")).OrDefault()
	require.NoError(t, err)
	file := source.File

	c, cmd := parse(t, config.ServeArgs(file)...)
	assert.Equal(t, config.ServeCommand, cmd, "the command that the unit's ExecStart runs")
	assert.Equal(t, file.Path(), c.Serve.Config)
}

func TestServiceCommandNames(t *testing.T) {
	for _, command := range []string{string(service.InstallCommand), string(service.UninstallCommand), string(service.StatusCommand)} {
		_, cmd := parse(t, strings.Fields(command)...)
		assert.Equal(t, command, cmd, "kong's name for the command that the service hints spell")
	}
}

func TestConfigValidate(t *testing.T) {
	validate := func(body string) error {
		c, _ := parse(t, "config", "validate", "--config", writeConfig(t, body))
		return c.Config.Validate.Run()
	}
	assert.NoError(t, validate("serve:\n  upstream:\n    type: session_bus\n"))
	assert.Error(t, validate("serve: ["), "the file fails to load")
	assert.Error(t, validate("serve:\n  upstream:\n    type: socket\n"), "the config fails to resolve")
}

func TestGPGSignSetupRemoteSocket(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)
	remoteSocket := func(args ...string) unixsock.Path {
		c, _ := parse(t, append([]string{"gpg-sign", "setup"}, args...)...)
		return c.GPGSign.Setup.RemoteSocket
	}
	assert.Equal(t, unixsock.Path(filepath.Join(cwd, "r.sock")), remoteSocket("--remote-socket", "r.sock"))
	assert.Equal(t, unixsock.Path("/run/r.sock"), remoteSocket("--remote-socket", "/run/r.sock"))
	assert.Empty(t, remoteSocket("--remote-socket", ""), "an empty value stays unset")
	assert.Empty(t, remoteSocket())
}
