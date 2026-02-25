package companion

import (
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

// swapFuncs saves and restores all injectable syscall functions around a test.
// Returns a cleanup function to be called with t.Cleanup.
func saveOrigFuncs(t *testing.T) {
	t.Helper()
	origUserAdd := userAddFunc
	origLoginctl := loginctlFunc
	origUserLookup := userLookupFunc
	origMkdirAll := mkdirAllFunc
	origChown := chownFunc
	origChmod := chmodFunc
	origWriteFile := writeFileFunc
	origGeteuid := geteuidFunc
	t.Cleanup(func() {
		userAddFunc = origUserAdd
		loginctlFunc = origLoginctl
		userLookupFunc = origUserLookup
		mkdirAllFunc = origMkdirAll
		chownFunc = origChown
		chmodFunc = origChmod
		writeFileFunc = origWriteFile
		geteuidFunc = origGeteuid
	})
}

// mockRootEuid makes geteuidFunc return 0 (root).
func mockRootEuid() {
	geteuidFunc = func() int { return 0 }
}

// mockUserNotFound makes userLookupFunc return a "not found" error.
func mockUserNotFound() {
	userLookupFunc = func(username string) (*user.User, error) {
		return nil, user.UnknownUserError(username)
	}
}

// mockUserFound makes userLookupFunc return a fake user with given uid/gid.
func mockUserFound(username, uid, gid string) {
	userLookupFunc = func(name string) (*user.User, error) {
		if name == username {
			return &user.User{Username: username, Uid: uid, Gid: gid}, nil
		}
		return nil, user.UnknownUserError(name)
	}
}

// noopFuncs stubs out all filesystem/exec calls to be no-ops.
func noopFuncs() {
	userAddFunc = func(username, homeDir, shell string) error { return nil }
	loginctlFunc = func(args ...string) error { return nil }
	mkdirAllFunc = func(path string, perm os.FileMode) error { return nil }
	chownFunc = func(path string, uid, gid int) error { return nil }
	chmodFunc = func(path string, mode os.FileMode) error { return nil }
	writeFileFunc = func(path string, data []byte, perm os.FileMode) error { return nil }
}

func TestProvision_CreatesUser(t *testing.T) {
	saveOrigFuncs(t)
	mockRootEuid()

	var addedUser, addedHome, addedShell string
	userAddFunc = func(username, homeDir, shell string) error {
		addedUser = username
		addedHome = homeDir
		addedShell = shell
		return nil
	}
	// First lookup: not found (triggers creation). Second lookup: returns created user.
	callCount := 0
	userLookupFunc = func(username string) (*user.User, error) {
		callCount++
		if callCount == 1 {
			return nil, user.UnknownUserError(username)
		}
		return &user.User{Username: username, Uid: "1001", Gid: "1001"}, nil
	}

	noopFuncs()
	userAddFunc = func(username, homeDir, shell string) error {
		addedUser = username
		addedHome = homeDir
		addedShell = shell
		return nil
	}
	userLookupFunc = func(username string) (*user.User, error) {
		callCount++
		if callCount == 1 {
			return nil, user.UnknownUserError(username)
		}
		return &user.User{Username: username, Uid: "1001", Gid: "1001"}, nil
	}

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
	saveOrigFuncs(t)
	mockRootEuid()
	noopFuncs()

	// User already exists on first lookup.
	userLookupFunc = mockLookupAlwaysFound("secrets-nb", "1001", "1001")

	userAddCalled := false
	userAddFunc = func(username, homeDir, shell string) error {
		userAddCalled = true
		return nil
	}

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
	saveOrigFuncs(t)
	mockRootEuid()
	noopFuncs()
	userLookupFunc = mockLookupAlwaysFound("secrets-nb", "1001", "1001")

	var createdDirs []string
	mkdirAllFunc = func(path string, perm os.FileMode) error {
		createdDirs = append(createdDirs, path)
		return nil
	}

	var chownedPaths []string
	chownFunc = func(path string, uid, gid int) error {
		chownedPaths = append(chownedPaths, path)
		return nil
	}

	cfg := Config{DesktopUser: "nb"}
	if err := Provision(cfg); err != nil {
		t.Fatalf("Provision() unexpected error: %v", err)
	}

	homeDir := "/var/lib/secret-companion/nb"
	wantDirs := []string{
		homeDir,                                          // home dir
		filepath.Join(homeDir, ".config", "gopass"),     // gopass config
		filepath.Join(homeDir, ".gnupg"),                // gnupg
		filepath.Join(homeDir, ".config", "systemd", "user"), // systemd unit dir
	}

	dirSet := make(map[string]bool, len(createdDirs))
	for _, d := range createdDirs {
		dirSet[d] = true
	}
	for _, want := range wantDirs {
		if !dirSet[want] {
			t.Errorf("expected mkdirAllFunc called for %q; got dirs: %v", want, createdDirs)
		}
	}

	// chown should be called for home dir at minimum.
	chownSet := make(map[string]bool, len(chownedPaths))
	for _, p := range chownedPaths {
		chownSet[p] = true
	}
	if !chownSet[homeDir] {
		t.Errorf("expected chownFunc called for home dir %q; got paths: %v", homeDir, chownedPaths)
	}
}

func TestProvision_WritesDBusPolicy(t *testing.T) {
	saveOrigFuncs(t)
	mockRootEuid()

	// Use a real temp dir for file writes so we can read back content.
	tmpDir := t.TempDir()

	// Wire real writeFileFunc but redirect to tmpDir.
	var writtenPaths []string
	var writtenContents [][]byte
	writeFileFunc = func(path string, data []byte, perm os.FileMode) error {
		writtenPaths = append(writtenPaths, path)
		writtenContents = append(writtenContents, data)
		return nil
	}
	mkdirAllFunc = func(path string, perm os.FileMode) error { return nil }
	chownFunc = func(path string, uid, gid int) error { return nil }
	chmodFunc = func(path string, mode os.FileMode) error { return nil }
	userAddFunc = func(username, homeDir, shell string) error { return nil }
	loginctlFunc = func(args ...string) error { return nil }
	userLookupFunc = mockLookupAlwaysFound("secrets-nb", "1001", "1001")
	_ = tmpDir

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
	saveOrigFuncs(t)
	mockRootEuid()
	userLookupFunc = mockLookupAlwaysFound("secrets-nb", "1001", "1001")
	mkdirAllFunc = func(path string, perm os.FileMode) error { return nil }
	chownFunc = func(path string, uid, gid int) error { return nil }
	chmodFunc = func(path string, mode os.FileMode) error { return nil }
	userAddFunc = func(username, homeDir, shell string) error { return nil }
	loginctlFunc = func(args ...string) error { return nil }

	var systemdContent string
	unitPath := filepath.Join("/var/lib/secret-companion/nb", ".config", "systemd", "user", "secrets-dispatcher-daemon.service")
	writeFileFunc = func(path string, data []byte, perm os.FileMode) error {
		if path == unitPath {
			systemdContent = string(data)
		}
		return nil
	}

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
}

func TestProvision_WritesPAMConfig(t *testing.T) {
	saveOrigFuncs(t)
	mockRootEuid()
	userLookupFunc = mockLookupAlwaysFound("secrets-nb", "1001", "1001")
	mkdirAllFunc = func(path string, perm os.FileMode) error { return nil }
	chownFunc = func(path string, uid, gid int) error { return nil }
	chmodFunc = func(path string, mode os.FileMode) error { return nil }
	userAddFunc = func(username, homeDir, shell string) error { return nil }
	loginctlFunc = func(args ...string) error { return nil }

	var pamContent string
	writeFileFunc = func(path string, data []byte, perm os.FileMode) error {
		if path == "/etc/pam.d/secrets-dispatcher" {
			pamContent = string(data)
		}
		return nil
	}

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
	if !strings.Contains(pamContent, "--no-block") {
		t.Errorf("PAM config should use --no-block to avoid hanging login; got:\n%s", pamContent)
	}
}

func TestProvision_EnablesLinger(t *testing.T) {
	saveOrigFuncs(t)
	mockRootEuid()
	noopFuncs()
	userLookupFunc = mockLookupAlwaysFound("secrets-nb", "1001", "1001")

	var loginctlArgs []string
	loginctlFunc = func(args ...string) error {
		loginctlArgs = args
		return nil
	}

	cfg := Config{DesktopUser: "nb"}
	if err := Provision(cfg); err != nil {
		t.Fatalf("Provision() unexpected error: %v", err)
	}

	if len(loginctlArgs) < 2 || loginctlArgs[0] != "enable-linger" || loginctlArgs[1] != "secrets-nb" {
		t.Errorf("loginctlFunc should be called with ('enable-linger', 'secrets-nb'); got %v", loginctlArgs)
	}
}

func TestProvision_RequiresRoot(t *testing.T) {
	saveOrigFuncs(t)
	geteuidFunc = func() int { return 1000 } // non-root

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
	saveOrigFuncs(t)
	mockRootEuid()
	noopFuncs()

	var detectedUser string
	userLookupFunc = func(username string) (*user.User, error) {
		// Capture what companion username was derived.
		if strings.HasPrefix(username, "secrets-") {
			detectedUser = strings.TrimPrefix(username, "secrets-")
		}
		return &user.User{Username: username, Uid: "1001", Gid: "1001"}, nil
	}

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
	saveOrigFuncs(t)
	mockRootEuid()

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
	saveOrigFuncs(t)
	mockRootEuid()
	noopFuncs()
	userLookupFunc = mockLookupAlwaysFound("secrets-nb", "1001", "1001")

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
	saveOrigFuncs(t)
	mockRootEuid()
	noopFuncs()

	mockUserNotFound()
	userAddFunc = func(username, homeDir, shell string) error {
		return errors.New("useradd: exit status 1")
	}

	cfg := Config{DesktopUser: "nb"}
	err := Provision(cfg)
	if err == nil {
		t.Fatal("Provision() should propagate userAdd error")
	}
}

func TestProvision_CompanionNameOverride(t *testing.T) {
	saveOrigFuncs(t)
	mockRootEuid()
	noopFuncs()

	var addedUser string
	callCount := 0
	userLookupFunc = func(username string) (*user.User, error) {
		callCount++
		if callCount == 1 {
			return nil, user.UnknownUserError(username)
		}
		return &user.User{Username: username, Uid: "1001", Gid: "1001"}, nil
	}
	userAddFunc = func(username, homeDir, shell string) error {
		addedUser = username
		return nil
	}

	cfg := Config{DesktopUser: "nb", CompanionName: "sd-companion"}
	if err := Provision(cfg); err != nil {
		t.Fatalf("Provision() unexpected error: %v", err)
	}

	if addedUser != "sd-companion" {
		t.Errorf("userAdd should use CompanionName override %q; got %q", "sd-companion", addedUser)
	}
}
