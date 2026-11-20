package companion

import (
	"errors"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"testing/fstest"

	"github.com/nikicat/secrets-dispatcher/internal/procutil"
	"github.com/nikicat/secrets-dispatcher/internal/testutil"
)

// Swaps every system seam for the test: root, a companion user secrets-nb
// (1001:1001) that exists, an in-memory directory tree that holds only "/",
// a fake Debian session stack, and no-ops for everything else. Returns the tree.
func fakeSystem(t *testing.T) fakeDirs {
	t.Helper()
	dirs := fakeDirs{}
	fakeSessionStack(t, debianCommonSession)
	testutil.Swap(t, &geteuidFunc, func() procutil.UID { return procutil.RootUID })
	testutil.Swap(t, &userLookupFunc, mockLookupAlwaysFound("secrets-nb", "1001", "1001"))
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
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	testutil.Swap(t, &sessionStacks, []string{path})
	return path
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

	cfg := Config{DesktopUser: "nb"}
	if err := Provision(cfg); err != nil {
		t.Fatalf("Provision() unexpected error: %v", err)
	}

	if addedUser != "secrets-nb" {
		t.Errorf("userAdd called with username=%q, want %q", addedUser, "secrets-nb")
	}
	if addedHome != "/var/lib/secret-companion/nb" {
		t.Errorf("userAdd called with homeDir=%q, want %q", addedHome, "/var/lib/secret-companion/nb")
	}
	if addedShell != "/usr/sbin/nologin" {
		t.Errorf("userAdd called with shell=%q, want %q", addedShell, "/usr/sbin/nologin")
	}
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

	cfg := Config{DesktopUser: "nb"}
	if err := Provision(cfg); err != nil {
		t.Fatalf("Provision() unexpected error: %v", err)
	}

	if userAddCalled {
		t.Error("userAddFunc should NOT be called when user already exists (idempotent)")
	}
}

// mockLookupAlwaysFound returns a userLookupFunc that always succeeds.
func mockLookupAlwaysFound(username, uid, gid string) func(string) (*user.User, error) {
	return func(name string) (*user.User, error) {
		return &user.User{Username: username, Uid: uid, Gid: gid}, nil
	}
}

func TestProvision_CreatesDirectories(t *testing.T) {
	dirs := fakeSystem(t)
	testutil.Swap(t, &userLookupFunc, mockLookupAlwaysFound("secrets-nb", "1001", "1002"))

	// A filesystem where only the companion home's parent exists.
	dirs[fsName("/var/lib/secret-companion")] = &fstest.MapFile{Mode: fs.ModeDir | 0755}

	var chownedPaths []string
	testutil.Swap(t, &chownFunc, func(path string, uid procutil.UID, gid procutil.GID) error {
		if uid != 1001 || gid != 1002 {
			t.Errorf("chown %q to %d:%d, want 1001:1002", path, uid, gid)
		}
		chownedPaths = append(chownedPaths, path)
		return nil
	})

	cfg := Config{DesktopUser: "nb"}
	if err := Provision(cfg); err != nil {
		t.Fatalf("Provision() unexpected error: %v", err)
	}

	homeDir := "/var/lib/secret-companion/nb"
	created := []string{
		homeDir,
		filepath.Join(homeDir, ".config"),
		filepath.Join(homeDir, ".config", "gopass"),
		filepath.Join(homeDir, ".gnupg"),
		filepath.Join(homeDir, ".config", "systemd"),
		companionHome(homeDir).UnitDir().String(),
	}
	for _, dir := range created {
		if !dirs.isDir(dir) {
			t.Errorf("directory %q not created", dir)
		}
		if !slices.Contains(chownedPaths, dir) {
			t.Errorf("directory %q not handed to the companion; chowned: %v", dir, chownedPaths)
		}
	}
	if unitFile := companionHome(homeDir).UnitDir().UnitFile(daemonUnit).String(); !slices.Contains(chownedPaths, unitFile) {
		t.Errorf("unit file %q not handed to the companion; chowned: %v", unitFile, chownedPaths)
	}
	for _, dir := range []string{"/", "/var", "/var/lib", "/var/lib/secret-companion"} {
		if slices.Contains(chownedPaths, dir) {
			t.Errorf("pre-existing directory %q handed to the companion", dir)
		}
	}
}

func TestProvision_FailsOnFileInPlaceOfDirectory(t *testing.T) {
	dirs := fakeSystem(t)
	dirs[fsName("/var/lib/secret-companion/nb/.gnupg")] = &fstest.MapFile{Mode: 0600}

	err := Provision(Config{DesktopUser: "nb"})
	if !errors.Is(err, syscall.ENOTDIR) {
		t.Errorf("Provision() error = %v, want ENOTDIR", err)
	}
}

func TestProvision_WritesDBusPolicy(t *testing.T) {
	fakeSystem(t)

	var writtenPaths []string
	var writtenContents [][]byte
	testutil.Swap(t, &writeFileFunc, func(path string, data []byte, perm os.FileMode) error {
		writtenPaths = append(writtenPaths, path)
		writtenContents = append(writtenContents, data)
		return nil
	})

	cfg := Config{DesktopUser: "nb"}
	if err := Provision(cfg); err != nil {
		t.Fatalf("Provision() unexpected error: %v", err)
	}

	// Find the D-Bus policy write.
	dbusPath := "/usr/share/dbus-1/system.d/net.mowaka.SecretsDispatcher1.conf"
	var dbusContent string
	for i, p := range writtenPaths {
		if p == dbusPath {
			dbusContent = string(writtenContents[i])
		}
	}
	if dbusContent == "" {
		t.Fatalf("writeFileFunc not called for D-Bus policy at %q; got paths: %v", dbusPath, writtenPaths)
	}

	if !strings.Contains(dbusContent, "secrets-nb") {
		t.Error("D-Bus policy should contain companion username 'secrets-nb'")
	}
	if !strings.Contains(dbusContent, "nb") {
		t.Error("D-Bus policy should contain desktop username 'nb'")
	}
	if !strings.Contains(dbusContent, "net.mowaka.SecretsDispatcher1") {
		t.Error("D-Bus policy should reference the bus name")
	}
	if !strings.Contains(dbusContent, `allow own`) {
		t.Error("D-Bus policy should contain 'allow own' for companion user")
	}
}

func TestProvision_WritesSystemdUnit(t *testing.T) {
	fakeSystem(t)

	var systemdContent string
	unitPath := companionHome("/var/lib/secret-companion/nb").UnitDir().UnitFile(daemonUnit).String()
	testutil.Swap(t, &writeFileFunc, func(path string, data []byte, perm os.FileMode) error {
		if path == unitPath {
			systemdContent = string(data)
		}
		return nil
	})

	cfg := Config{DesktopUser: "nb"}
	if err := Provision(cfg); err != nil {
		t.Fatalf("Provision() unexpected error: %v", err)
	}

	if systemdContent == "" {
		t.Fatalf("writeFileFunc not called for systemd unit at %q", unitPath)
	}
	if !strings.Contains(systemdContent, "secrets-dispatcher daemon") {
		t.Errorf("systemd unit ExecStart should contain 'secrets-dispatcher daemon'; got:\n%s", systemdContent)
	}
	if !strings.Contains(systemdContent, "Type=notify") {
		t.Errorf("systemd unit should have Type=notify; got:\n%s", systemdContent)
	}
	if !strings.Contains(systemdContent, "Environment=XDG_RUNTIME_DIR=/run/user/1001\n") {
		t.Errorf("systemd unit should set the companion's runtime dir; got:\n%s", systemdContent)
	}
}

func TestProvision_WritesPAMConfig(t *testing.T) {
	fakeSystem(t)

	var pamContent string
	testutil.Swap(t, &writeFileFunc, func(path string, data []byte, perm os.FileMode) error {
		if path == pamConfigPath {
			pamContent = string(data)
		}
		return nil
	})

	cfg := Config{DesktopUser: "nb"}
	if err := Provision(cfg); err != nil {
		t.Fatalf("Provision() unexpected error: %v", err)
	}

	if pamContent == "" {
		t.Fatal("writeFileFunc not called for PAM config at /etc/pam.d/secrets-dispatcher")
	}
	if !strings.Contains(pamContent, "pam_exec.so") {
		t.Errorf("PAM config should reference pam_exec.so; got:\n%s", pamContent)
	}
	wantLine := "/usr/bin/systemctl --user --machine=secrets-nb@ start --no-block " + string(daemonUnit) + "\n"
	if !strings.HasSuffix(pamContent, wantLine) {
		t.Errorf("PAM config should start %s under the companion user's manager; got:\n%s", daemonUnit, pamContent)
	}
}

func TestProvision_EnablesLinger(t *testing.T) {
	fakeSystem(t)

	var loginctlArgs []string
	testutil.Swap(t, &loginctlFunc, func(args ...string) error {
		loginctlArgs = args
		return nil
	})

	cfg := Config{DesktopUser: "nb"}
	if err := Provision(cfg); err != nil {
		t.Fatalf("Provision() unexpected error: %v", err)
	}

	if len(loginctlArgs) < 2 || loginctlArgs[0] != "enable-linger" || loginctlArgs[1] != "secrets-nb" {
		t.Errorf("loginctlFunc should be called with ('enable-linger', 'secrets-nb'); got %v", loginctlArgs)
	}
}

func TestProvision_RequiresRoot(t *testing.T) {
	fakeSystem(t)
	testutil.Swap(t, &geteuidFunc, func() procutil.UID { return 1000 }) // non-root

	cfg := Config{DesktopUser: "nb"}
	err := Provision(cfg)
	if err == nil {
		t.Fatal("Provision() should return error when not root")
	}
	if !strings.Contains(err.Error(), "root") {
		t.Errorf("error should mention root requirement; got: %v", err)
	}
}

func TestProvision_DetectsSUDO_USER(t *testing.T) {
	fakeSystem(t)

	var detectedUser string
	testutil.Swap(t, &userLookupFunc, func(username string) (*user.User, error) {
		// Capture what companion username was derived.
		if after, ok := strings.CutPrefix(username, "secrets-"); ok {
			detectedUser = after
		}
		return &user.User{Username: username, Uid: "1001", Gid: "1001"}, nil
	})

	t.Setenv("SUDO_USER", "alice")

	cfg := Config{} // empty DesktopUser — should fall back to SUDO_USER
	if err := Provision(cfg); err != nil {
		t.Fatalf("Provision() unexpected error: %v", err)
	}

	if detectedUser != "alice" {
		t.Errorf("should have detected desktop user 'alice' from SUDO_USER; got %q", detectedUser)
	}
}

func TestProvision_FailsWithoutUser(t *testing.T) {
	fakeSystem(t)

	// Ensure SUDO_USER is not set.
	t.Setenv("SUDO_USER", "")

	cfg := Config{} // empty DesktopUser, no SUDO_USER
	err := Provision(cfg)
	if err == nil {
		t.Fatal("Provision() should return error with no desktop user")
	}
	if !strings.Contains(err.Error(), "user") {
		t.Errorf("error should mention user requirement; got: %v", err)
	}
}

func TestProvision_Idempotent(t *testing.T) {
	fakeSystem(t)

	cfg := Config{DesktopUser: "nb"}

	// First run.
	if err := Provision(cfg); err != nil {
		t.Fatalf("Provision() first run error: %v", err)
	}

	// Second run should also succeed.
	if err := Provision(cfg); err != nil {
		t.Fatalf("Provision() second run error (not idempotent): %v", err)
	}
}

func TestProvision_UserAddPropagatesError(t *testing.T) {
	fakeSystem(t)

	testutil.Swap(t, &userLookupFunc, func(username string) (*user.User, error) {
		return nil, user.UnknownUserError(username)
	})
	testutil.Swap(t, &userAddFunc, func(username, homeDir, shell string) error {
		return errors.New("useradd: exit status 1")
	})

	cfg := Config{DesktopUser: "nb"}
	err := Provision(cfg)
	if err == nil {
		t.Fatal("Provision() should propagate userAdd error")
	}
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

	cfg := Config{DesktopUser: "nb", CompanionName: "sd-companion"}
	if err := Provision(cfg); err != nil {
		t.Fatalf("Provision() unexpected error: %v", err)
	}

	if addedUser != "sd-companion" {
		t.Errorf("userAdd should use CompanionName override %q; got %q", "sd-companion", addedUser)
	}
}

// Pins the files Provision writes and the PAM hook's exact content.
func TestProvision_WrittenFiles(t *testing.T) {
	fakeSystem(t)
	written := map[string]string{}
	testutil.Swap(t, &writeFileFunc, func(path string, data []byte, perm os.FileMode) error {
		written[path] = string(data)
		return nil
	})

	if err := Provision(Config{DesktopUser: "nb"}); err != nil {
		t.Fatalf("Provision() unexpected error: %v", err)
	}

	var paths []string
	for p := range written {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	want := []string{
		sessionStacks[0],
		pamConfigPath,
		"/usr/share/dbus-1/system.d/net.mowaka.SecretsDispatcher1.conf",
		"/var/lib/secret-companion/nb/.config/systemd/user/" + string(daemonUnit),
	}
	slices.Sort(want)
	if !slices.Equal(paths, want) {
		t.Errorf("Provision() wrote %v, want %v", paths, want)
	}
	wantPAM := "# Managed by secrets-dispatcher provision. Do not edit manually.\n" +
		"# Start the companion daemon on desktop user login.\n" +
		"session optional pam_exec.so quiet type=open_session /usr/bin/systemctl --user --machine=secrets-nb@ start --no-block " + string(daemonUnit) + "\n"
	if written[pamConfigPath] != wantPAM {
		t.Errorf("PAM config = %q, want %q", written[pamConfigPath], wantPAM)
	}
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
			must(t, os.Chmod(path, 0640))
			writeOnlySessionStack(t)

			if err := Provision(Config{DesktopUser: "nb"}); err != nil {
				t.Fatalf("Provision() unexpected error: %v", err)
			}

			got, err := os.ReadFile(path)
			must(t, err)
			want := debianCommonSession + "# Added by secrets-dispatcher provision.\nsession include secrets-dispatcher\n"
			if string(got) != want {
				t.Errorf("session stack = %q, want %q", got, want)
			}
			info, err := os.Stat(path)
			must(t, err)
			if info.Mode().Perm() != 0640 {
				t.Errorf("session stack mode = %v, want 0640", info.Mode().Perm())
			}
		})
	}
}

func TestProvision_IncludesPAMConfigOnce(t *testing.T) {
	fakeSystem(t)
	writeOnlySessionStack(t)
	path := sessionStacks[0]

	must(t, Provision(Config{DesktopUser: "nb"}))
	first, err := os.ReadFile(path)
	must(t, err)
	must(t, Provision(Config{DesktopUser: "nb"}))
	second, err := os.ReadFile(path)
	must(t, err)
	if string(second) != string(first) {
		t.Errorf("second Provision() changed the session stack:\n%s\nwas:\n%s", second, first)
	}
}

func TestProvision_FailsWithoutSessionStack(t *testing.T) {
	fakeSystem(t)
	testutil.Swap(t, &sessionStacks, []string{filepath.Join(t.TempDir(), "common-session"), filepath.Join(t.TempDir(), "system-login")})

	err := Provision(Config{DesktopUser: "nb"})
	if err == nil || !strings.Contains(err.Error(), "session include secrets-dispatcher") {
		t.Errorf("Provision() error = %v, want one naming the line to add", err)
	}
}
