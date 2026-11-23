package companion

import (
	"errors"
	"io/fs"
	"maps"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/procutil"
	"github.com/nikicat/secrets-dispatcher/internal/testutil"
)

// Swaps every system seam for the test: root, the test's companion user
// (1001:1001) as an existing user, an in-memory directory tree that holds only "/",
// a fake Debian session stack, and no-ops for everything else. Returns the tree.
func fakeSystem(t *testing.T) fakeDirs {
	t.Helper()
	dirs := fakeDirs{}
	fakeSessionStack(t, debianCommonSession)
	testutil.Swap(t, &geteuidFunc, func() procutil.UID { return procutil.RootUID })
	testutil.Swap(t, &userLookupFunc, mockLookupAlwaysFound(testCompanionUser, "1001", "1001"))
	testutil.Swap(t, &userAddFunc, func(username, homeDir, shell string) error { return nil })
	testutil.Swap(t, &loginctlFunc, func(args ...string) error { return nil })
	testutil.Swap(t, &mkdirAllFunc, func(path string, perm os.FileMode) error { return nil })
	testutil.Swap(t, &mkdirFunc, dirs.mkdir)
	testutil.Swap(t, &statFunc, dirs.stat)
	testutil.Swap(t, &chownFunc, func(path string, uid procutil.UID, gid procutil.GID) error { return nil })
	testutil.Swap(t, &chmodFunc, func(path string, mode os.FileMode) error { return nil })
	testutil.Swap(t, &writeFileFunc, func(path string, data []byte, perm os.FileMode) error { return nil })
	return dirs
}

// An in-memory tree behind mkdirFunc and statFunc, keyed by absolute path.
type fakeDirs fstest.MapFS

// Maps an absolute path to its fs.FS name.
func fsName(path string) string {
	if path == "/" {
		return "."
	}
	return strings.TrimPrefix(path, "/")
}

func (d fakeDirs) stat(path string) (fs.FileInfo, error) {
	return fs.Stat(fstest.MapFS(d), fsName(path))
}

// Fails as os.Mkdir does when path exists or its parent is missing.
func (d fakeDirs) mkdir(path string, perm os.FileMode) error {
	if _, err := d.stat(path); err == nil {
		return fs.ErrExist
	}
	if parent, err := d.stat(filepath.Dir(path)); err != nil || !parent.IsDir() {
		return fs.ErrNotExist
	}
	d[fsName(path)] = &fstest.MapFile{Mode: fs.ModeDir | perm}
	return nil
}

// Reports whether path is a directory in the tree.
func (d fakeDirs) isDir(path string) bool {
	info, err := d.stat(path)
	return err == nil && info.IsDir()
}

// A trimmed Debian /etc/pam.d/common-session.
const debianCommonSession = `session	[default=1]			pam_permit.so
session	requisite			pam_deny.so
session	required			pam_permit.so
session	required	pam_unix.so
# end of pam-auth-update config
`

// Makes a stack file holding content the only session stack for the test,
// and returns its path.
func fakeSessionStack(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "common-session")
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
	testutil.Swap(t, &sessionStacks, []string{path})
	return path
}

// The test's desktop user, and its companion user, home and unit file spelled
// out rather than derived, so the tests pin the production names.
const (
	testDesktopUser       = "alice"
	testCompanionUser     = "secrets-" + testDesktopUser
	testCompanionHome     = "/var/lib/secret-companion/" + testDesktopUser
	testCompanionUnitFile = testCompanionHome + "/.config/systemd/user/" + string(daemonUnit)
)

// Records the content of each write by path.
func captureWrites(t *testing.T) map[string]string {
	t.Helper()
	written := map[string]string{}
	testutil.Swap(t, &writeFileFunc, func(path string, data []byte, perm os.FileMode) error {
		written[path] = string(data)
		return nil
	})
	return written
}

func TestProvision_CreatesUser(t *testing.T) {
	fakeSystem(t)

	var addedUser, addedHome, addedShell string
	testutil.Swap(t, &userAddFunc, func(username, homeDir, shell string) error {
		addedUser = username
		addedHome = homeDir
		addedShell = shell
		return nil
	})
	// First lookup: not found (triggers creation). Second lookup: returns created user.
	callCount := 0
	testutil.Swap(t, &userLookupFunc, func(username string) (*user.User, error) {
		callCount++
		if callCount == 1 {
			return nil, user.UnknownUserError(username)
		}
		return &user.User{Username: username, Uid: "1001", Gid: "1001"}, nil
	})

	cfg := Config{DesktopUser: testDesktopUser}
	require.NoError(t, Provision(cfg))

	assert.Equal(t, testCompanionUser, addedUser, "userAdd username")
	assert.Equal(t, testCompanionHome, addedHome, "userAdd homeDir")
	assert.Equal(t, "/usr/sbin/nologin", addedShell, "userAdd shell")
	// Verify --system flag is NOT in the command (it's the real defaultUserAdd that would use it; test stub just records args)
	// The real check is that we're NOT passing --system in defaultUserAdd; since the function signature
	// does not include a "system bool" param, this is architecturally guaranteed.
}

func TestProvision_SkipsExistingUser(t *testing.T) {
	fakeSystem(t)

	userAddCalled := false
	testutil.Swap(t, &userAddFunc, func(username, homeDir, shell string) error {
		userAddCalled = true
		return nil
	})

	cfg := Config{DesktopUser: testDesktopUser}
	require.NoError(t, Provision(cfg))

	assert.False(t, userAddCalled, "userAddFunc should NOT be called when user already exists (idempotent)")
}

// mockLookupAlwaysFound returns a userLookupFunc that always succeeds.
func mockLookupAlwaysFound(username, uid, gid string) func(string) (*user.User, error) {
	return func(name string) (*user.User, error) {
		return &user.User{Username: username, Uid: uid, Gid: gid}, nil
	}
}

func TestProvision_CreatesDirectories(t *testing.T) {
	dirs := fakeSystem(t)
	testutil.Swap(t, &userLookupFunc, mockLookupAlwaysFound(testCompanionUser, "1001", "1002"))

	// A filesystem where only the companion home's parent exists.
	dirs[fsName("/var/lib/secret-companion")] = &fstest.MapFile{Mode: fs.ModeDir | 0755}

	var chownedPaths []string
	testutil.Swap(t, &chownFunc, func(path string, uid procutil.UID, gid procutil.GID) error {
		assert.Equal(t, procutil.UID(1001), uid, "chown %q uid", path)
		assert.Equal(t, procutil.GID(1002), gid, "chown %q gid", path)
		chownedPaths = append(chownedPaths, path)
		return nil
	})

	cfg := Config{DesktopUser: testDesktopUser}
	require.NoError(t, Provision(cfg))

	created := []string{
		testCompanionHome,
		filepath.Join(testCompanionHome, ".config"),
		filepath.Join(testCompanionHome, ".config", "gopass"),
		filepath.Join(testCompanionHome, ".gnupg"),
		filepath.Join(testCompanionHome, ".config", "systemd"),
		companionHome(testCompanionHome).UnitDir().String(),
	}
	for _, dir := range created {
		assert.True(t, dirs.isDir(dir), "directory %q not created", dir)
		assert.Contains(t, chownedPaths, dir, "directory not handed to the companion")
	}
	assert.Contains(t, chownedPaths, testCompanionUnitFile, "unit file not handed to the companion")
	for _, dir := range []string{"/", "/var", "/var/lib", "/var/lib/secret-companion"} {
		assert.NotContains(t, chownedPaths, dir, "pre-existing directory handed to the companion")
	}
}

func TestProvision_FailsOnFileInPlaceOfDirectory(t *testing.T) {
	dirs := fakeSystem(t)
	dirs[fsName(testCompanionHome+"/.gnupg")] = &fstest.MapFile{Mode: 0600}

	err := Provision(Config{DesktopUser: testDesktopUser})
	assert.ErrorIs(t, err, syscall.ENOTDIR)
}

func TestProvision_WritesDBusPolicy(t *testing.T) {
	fakeSystem(t)

	written := captureWrites(t)

	cfg := Config{DesktopUser: testDesktopUser}
	require.NoError(t, Provision(cfg))

	dbusPath := "/usr/share/dbus-1/system.d/net.mowaka.SecretsDispatcher1.conf"
	require.Contains(t, written, dbusPath)
	dbusContent := written[dbusPath]

	assert.Contains(t, dbusContent, testCompanionUser, "D-Bus policy should contain companion username")
	assert.Contains(t, dbusContent, testDesktopUser, "D-Bus policy should contain desktop username")
	assert.Contains(t, dbusContent, "net.mowaka.SecretsDispatcher1", "D-Bus policy should reference the bus name")
	assert.Contains(t, dbusContent, `allow own`, "D-Bus policy should contain 'allow own' for companion user")
}

func TestProvision_WritesSystemdUnit(t *testing.T) {
	fakeSystem(t)

	written := captureWrites(t)

	cfg := Config{DesktopUser: testDesktopUser}
	require.NoError(t, Provision(cfg))

	require.Contains(t, written, testCompanionUnitFile)
	systemdContent := written[testCompanionUnitFile]
	assert.Contains(t, systemdContent, "secrets-dispatcher daemon", "systemd unit ExecStart")
	assert.Contains(t, systemdContent, "Type=notify")
	assert.Contains(t, systemdContent, "Environment=XDG_RUNTIME_DIR=/run/user/1001\n", "systemd unit should set the companion's runtime dir")
}

func TestProvision_EnablesLinger(t *testing.T) {
	fakeSystem(t)

	var loginctlArgs []string
	testutil.Swap(t, &loginctlFunc, func(args ...string) error {
		loginctlArgs = args
		return nil
	})

	cfg := Config{DesktopUser: testDesktopUser}
	require.NoError(t, Provision(cfg))

	assert.Equal(t, []string{"enable-linger", testCompanionUser}, loginctlArgs)
}

func TestProvision_RequiresRoot(t *testing.T) {
	fakeSystem(t)
	testutil.Swap(t, &geteuidFunc, func() procutil.UID { return 1000 }) // non-root

	cfg := Config{DesktopUser: testDesktopUser}
	err := Provision(cfg)
	assert.ErrorContains(t, err, "root", "error should mention root requirement")
}

func TestProvision_FailsWithoutUser(t *testing.T) {
	fakeSystem(t)

	cfg := Config{}
	err := Provision(cfg)
	assert.ErrorContains(t, err, "user", "error should mention user requirement")
}

func TestProvision_Idempotent(t *testing.T) {
	fakeSystem(t)

	cfg := Config{DesktopUser: testDesktopUser}

	// First run.
	require.NoError(t, Provision(cfg), "first run error")

	// Second run should also succeed.
	require.NoError(t, Provision(cfg), "second run error (not idempotent)")
}

func TestProvision_UserAddPropagatesError(t *testing.T) {
	fakeSystem(t)

	testutil.Swap(t, &userLookupFunc, func(username string) (*user.User, error) {
		return nil, user.UnknownUserError(username)
	})
	errUserAdd := errors.New("useradd: exit status 1")
	testutil.Swap(t, &userAddFunc, func(username, homeDir, shell string) error {
		return errUserAdd
	})

	cfg := Config{DesktopUser: testDesktopUser}
	err := Provision(cfg)
	assert.ErrorIs(t, err, errUserAdd)
}

func TestProvision_CompanionNameOverride(t *testing.T) {
	fakeSystem(t)

	var addedUser string
	callCount := 0
	testutil.Swap(t, &userLookupFunc, func(username string) (*user.User, error) {
		callCount++
		if callCount == 1 {
			return nil, user.UnknownUserError(username)
		}
		return &user.User{Username: username, Uid: "1001", Gid: "1001"}, nil
	})
	testutil.Swap(t, &userAddFunc, func(username, homeDir, shell string) error {
		addedUser = username
		return nil
	})

	cfg := Config{DesktopUser: testDesktopUser, CompanionName: "sd-companion"}
	require.NoError(t, Provision(cfg))

	assert.Equal(t, "sd-companion", addedUser, "userAdd should use CompanionName override")
}

// Pins the files Provision writes and the PAM hook's exact content.
func TestProvision_WrittenFiles(t *testing.T) {
	fakeSystem(t)
	written := captureWrites(t)

	require.NoError(t, Provision(Config{DesktopUser: testDesktopUser}))

	want := []string{
		sessionStacks[0],
		pamConfigPath,
		"/usr/share/dbus-1/system.d/net.mowaka.SecretsDispatcher1.conf",
		testCompanionUnitFile,
	}
	assert.ElementsMatch(t, want, slices.Collect(maps.Keys(written)))
	wantPAM := "# Managed by secrets-dispatcher provision. Do not edit manually.\n" +
		"# Start the companion daemon on desktop user login.\n" +
		"session optional pam_exec.so quiet type=open_session /usr/bin/systemctl --user --machine=" + testCompanionUser + "@ start --no-block " + string(daemonUnit) + "\n"
	assert.Equal(t, wantPAM, written[pamConfigPath], "PAM config")
}

// Writes only the session stack, for real, and drops every other write.
func writeOnlySessionStack(t *testing.T) {
	testutil.Swap(t, &writeFileFunc, func(path string, data []byte, perm os.FileMode) error {
		if slices.Contains(sessionStacks, path) {
			return os.WriteFile(path, data, perm)
		}
		return nil
	})
}

func TestProvision_IncludesPAMConfig(t *testing.T) {
	for _, tc := range []struct{ name, stack string }{
		{"trailing newline", debianCommonSession},
		{"no trailing newline", strings.TrimSuffix(debianCommonSession, "\n")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeSystem(t)
			path := fakeSessionStack(t, tc.stack)
			require.NoError(t, os.Chmod(path, 0640))
			writeOnlySessionStack(t)

			require.NoError(t, Provision(Config{DesktopUser: testDesktopUser}))

			got, err := os.ReadFile(path)
			require.NoError(t, err)
			want := debianCommonSession + "# Added by secrets-dispatcher provision.\nsession include secrets-dispatcher\n"
			assert.Equal(t, want, string(got), "session stack")
			info, err := os.Stat(path)
			require.NoError(t, err)
			assert.Equal(t, os.FileMode(0640), info.Mode().Perm(), "session stack mode")
		})
	}
}

func TestProvision_IncludesPAMConfigOnce(t *testing.T) {
	fakeSystem(t)
	writeOnlySessionStack(t)
	path := sessionStacks[0]

	require.NoError(t, Provision(Config{DesktopUser: testDesktopUser}))
	first, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, Provision(Config{DesktopUser: testDesktopUser}))
	second, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(first), string(second), "second Provision() changed the session stack")
}

func TestProvision_FailsWithoutSessionStack(t *testing.T) {
	fakeSystem(t)
	testutil.Swap(t, &sessionStacks, []string{filepath.Join(t.TempDir(), "common-session"), filepath.Join(t.TempDir(), "system-login")})

	err := Provision(Config{DesktopUser: testDesktopUser})
	assert.ErrorContains(t, err, "session include secrets-dispatcher", "Provision() error should name the line to add")
}
