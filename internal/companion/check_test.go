package companion

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/testutil"
)

func TestCheck_AllPass(t *testing.T) {
	// Set up a temp dir that mirrors the expected provisioned structure.
	tmpDir := t.TempDir()

	// Mock user lookup to succeed, as the user that owns the temp dir.
	companionUsername := "secrets-testuser"
	uid := strconv.Itoa(os.Getuid())
	testutil.Swap(t, &userLookupFunc, func(username string) (*user.User, error) {
		if username == companionUsername {
			return &user.User{Username: companionUsername, Uid: uid, Gid: uid}, nil
		}
		return nil, user.UnknownUserError(username)
	})

	cfg := Config{
		DesktopUser: "testuser",
		HomeBase:    tmpDir,
	}

	homeDir := filepath.Join(tmpDir, "testuser")

	// Create all expected files/dirs.
	require.NoError(t, os.MkdirAll(homeDir, 0700))
	require.NoError(t, os.MkdirAll(filepath.Join(homeDir, ".config", "gopass"), 0700))
	require.NoError(t, os.MkdirAll(filepath.Join(homeDir, ".gnupg"), 0700))
	unitDir := companionHome(homeDir).UnitDir()
	// Create the unit file so the systemd unit check passes.
	require.NoError(t, unitDir.UnitFile(daemonUnit).Write("[Unit]"))

	// D-Bus policy and PAM config live at absolute system paths; we can't redirect
	// those in Check() (it uses os.Stat directly). Instead we only verify that the
	// check infrastructure works end-to-end for the path-based checks we control.

	// The first check (user exists) and home-dir checks should pass.
	checks := byName(Check(cfg))
	checks.pass(t, "companion user exists")
	checks.pass(t, "home directory exists")
	checks.pass(t, "home directory mode 0700")
	checks.pass(t, "home directory owned by companion user")
	checks.pass(t, "gopass config directory exists")
	checks.pass(t, "GPG home directory exists")
	checks.pass(t, "systemd user unit file exists")
}

func TestCheck_MissingUser(t *testing.T) {
	testutil.Swap(t, &userLookupFunc, func(username string) (*user.User, error) {
		return nil, user.UnknownUserError(username)
	})

	cfg := Config{DesktopUser: "nouser", HomeBase: t.TempDir()}
	byName(Check(cfg)).fail(t, "companion user exists")
}

func TestCheck_MissingFiles(t *testing.T) {
	// User exists but no directories created.
	testutil.Swap(t, &userLookupFunc, func(username string) (*user.User, error) {
		return &user.User{Username: username, Uid: "1001", Gid: "1001"}, nil
	})

	cfg := Config{DesktopUser: "emptyuser", HomeBase: t.TempDir()}
	// Don't create any files/dirs — everything should fail.
	checks := byName(Check(cfg))

	// User exists check should pass.
	checks.pass(t, "companion user exists")

	// Home directory checks should fail.
	checks.fail(t, "home directory exists")
	checks.fail(t, "home directory mode 0700")
	checks.fail(t, "home directory owned by companion user")
	checks.fail(t, "gopass config directory exists")
	checks.fail(t, "GPG home directory exists")
}

func TestCheck_LingerMissing(t *testing.T) {
	testutil.Swap(t, &userLookupFunc, func(username string) (*user.User, error) {
		return &user.User{Username: username, Uid: "1001", Gid: "1001"}, nil
	})

	cfg := Config{DesktopUser: "lingertest", HomeBase: t.TempDir()}
	// Linger check: /var/lib/systemd/linger/secrets-lingertest does not exist in test env.
	byName(Check(cfg)).fail(t, "systemd linger enabled")
}

func TestCheck_ReturnsExpectedCheckCount(t *testing.T) {
	testutil.Swap(t, &userLookupFunc, func(username string) (*user.User, error) {
		return nil, user.UnknownUserError(username)
	})

	cfg := Config{DesktopUser: "counttest", HomeBase: t.TempDir()}
	assert.Len(t, Check(cfg), 11)
}

type checksByName map[string]CheckResult

func byName(results []CheckResult) checksByName {
	checks := make(checksByName, len(results))
	for _, r := range results {
		checks[r.Name] = r
	}
	return checks
}

func (c checksByName) pass(t *testing.T, name string) {
	t.Helper()
	if r, ok := c[name]; assert.True(t, ok, "no check %q", name) {
		assert.True(t, r.Pass, "check %q failed: %s", name, r.Message)
	}
}

// Fails the test unless the named check ran and failed with a fix hint.
func (c checksByName) fail(t *testing.T, name string) {
	t.Helper()
	if r, ok := c[name]; assert.True(t, ok, "no check %q", name) {
		assert.False(t, r.Pass, "check %q passed: %s", name, r.Message)
		assert.NotEmpty(t, r.Message, "check %q failed without a fix hint", name)
	}
}

func TestCheckPAMConfig(t *testing.T) {
	cfg := Config{DesktopUser: testDesktopUser}
	other := Config{DesktopUser: "other"}
	path := filepath.Join(t.TempDir(), "secrets-dispatcher")
	content, err := pamConfig(testCompanionUser)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, content, 0644))

	r := cfg.checkPAMConfig(path)
	assert.True(t, r.Pass, "checkPAMConfig() failed on the provisioned file: %s", r.Message)
	assert.False(t, other.checkPAMConfig(path).Pass, "checkPAMConfig() passed a file written for another companion user")
	stale := "session optional pam_exec.so quiet /usr/bin/systemctl start --no-block secrets-dispatcher-companion@%u.service\n"
	require.NoError(t, os.WriteFile(path, []byte(stale), 0644))
	assert.False(t, cfg.checkPAMConfig(path).Pass, "checkPAMConfig() passed a stale PAM config")
	logoutToo := "session optional pam_exec.so quiet /usr/bin/systemctl --user --machine=" + testCompanionUser + "@ start --no-block " + string(daemonUnit) + "\n"
	require.NoError(t, os.WriteFile(path, []byte(logoutToo), 0644))
	assert.False(t, cfg.checkPAMConfig(path).Pass, "checkPAMConfig() passed a hook that also runs at logout")
	assert.False(t, cfg.checkPAMConfig(filepath.Join(t.TempDir(), "missing")).Pass, "checkPAMConfig() passed a missing file")
}

func TestCheckPAMInclude(t *testing.T) {
	cfg := Config{DesktopUser: testDesktopUser}
	path := fakeSessionStack(t, debianCommonSession)
	assert.False(t, cfg.checkPAMInclude().Pass, "checkPAMInclude() passed a stack without the include line")
	require.NoError(t, os.WriteFile(path, []byte(debianCommonSession+pamIncludeLine+"\n"), 0644))
	r := cfg.checkPAMInclude()
	assert.True(t, r.Pass, "checkPAMInclude() failed on a stack with the include line: %s", r.Message)
	testutil.Swap(t, &sessionStacks, []string{filepath.Join(t.TempDir(), "missing")})
	assert.False(t, cfg.checkPAMInclude().Pass, "checkPAMInclude() passed without a session stack")
}
