package companion

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"testing"

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
	must(t, os.MkdirAll(homeDir, 0700))
	must(t, os.MkdirAll(filepath.Join(homeDir, ".config", "gopass"), 0700))
	must(t, os.MkdirAll(filepath.Join(homeDir, ".gnupg"), 0700))
	unitDir := companionHome(homeDir).UnitDir()
	// Create the unit file so the systemd unit check passes.
	must(t, unitDir.UnitFile(daemonUnit).Write("[Unit]"))

	// D-Bus policy and PAM config live at absolute system paths; we can't redirect
	// those in Check() (it uses os.Stat directly). Instead we only verify that the
	// check infrastructure works end-to-end for the path-based checks we control.

	results := Check(cfg)
	if len(results) == 0 {
		t.Fatal("Check() returned no results")
	}

	// The first check (user exists) and home-dir checks should pass.
	checkMap := make(map[string]CheckResult, len(results))
	for _, r := range results {
		checkMap[r.Name] = r
	}

	assertPass(t, checkMap, "companion user exists")
	assertPass(t, checkMap, "home directory exists")
	assertPass(t, checkMap, "home directory mode 0700")
	assertPass(t, checkMap, "home directory owned by companion user")
	assertPass(t, checkMap, "gopass config directory exists")
	assertPass(t, checkMap, "GPG home directory exists")
	assertPass(t, checkMap, "systemd user unit file exists")
}

func TestCheck_MissingUser(t *testing.T) {
	testutil.Swap(t, &userLookupFunc, func(username string) (*user.User, error) {
		return nil, user.UnknownUserError(username)
	})

	cfg := Config{DesktopUser: "nouser", HomeBase: t.TempDir()}
	results := Check(cfg)

	checkMap := make(map[string]CheckResult, len(results))
	for _, r := range results {
		checkMap[r.Name] = r
	}

	r, ok := checkMap["companion user exists"]
	if !ok {
		t.Fatal("Check() missing 'companion user exists' result")
	}
	if r.Pass {
		t.Error("'companion user exists' should FAIL when user does not exist")
	}
	if r.Message == "" {
		t.Error("failed check should include a fix hint message")
	}
}

func TestCheck_MissingFiles(t *testing.T) {
	// User exists but no directories created.
	testutil.Swap(t, &userLookupFunc, func(username string) (*user.User, error) {
		return &user.User{Username: username, Uid: "1001", Gid: "1001"}, nil
	})

	cfg := Config{DesktopUser: "emptyuser", HomeBase: t.TempDir()}
	// Don't create any files/dirs — everything should fail.
	results := Check(cfg)

	checkMap := make(map[string]CheckResult, len(results))
	for _, r := range results {
		checkMap[r.Name] = r
	}

	// User exists check should pass.
	assertPass(t, checkMap, "companion user exists")

	// Home directory checks should fail.
	assertFail(t, checkMap, "home directory exists")
	assertFail(t, checkMap, "home directory mode 0700")
	assertFail(t, checkMap, "home directory owned by companion user")
	assertFail(t, checkMap, "gopass config directory exists")
	assertFail(t, checkMap, "GPG home directory exists")
}

func TestCheck_LingerMissing(t *testing.T) {
	testutil.Swap(t, &userLookupFunc, func(username string) (*user.User, error) {
		return &user.User{Username: username, Uid: "1001", Gid: "1001"}, nil
	})

	cfg := Config{DesktopUser: "lingertest", HomeBase: t.TempDir()}
	results := Check(cfg)

	checkMap := make(map[string]CheckResult, len(results))
	for _, r := range results {
		checkMap[r.Name] = r
	}

	// Linger check: /var/lib/systemd/linger/secrets-lingertest does not exist in test env.
	r, ok := checkMap["systemd linger enabled"]
	if !ok {
		t.Fatal("Check() missing 'systemd linger enabled' result")
	}
	if r.Pass {
		t.Error("'systemd linger enabled' should FAIL in test environment")
	}
	if r.Message == "" {
		t.Error("failed linger check should include a fix hint")
	}
}

func TestCheck_ReturnsExpectedCheckCount(t *testing.T) {
	testutil.Swap(t, &userLookupFunc, func(username string) (*user.User, error) {
		return nil, user.UnknownUserError(username)
	})

	cfg := Config{DesktopUser: "counttest", HomeBase: t.TempDir()}
	results := Check(cfg)

	// There should be exactly 11 checks (documented in check.go): user,
	// home-exists, home-mode, home-owned, gopass, gnupg, dbus, systemd-unit,
	// pam, pam-include, linger.
	const expectedChecks = 11
	if len(results) != expectedChecks {
		t.Errorf("Check() returned %d results, want %d; results: %v", len(results), expectedChecks, resultNames(results))
	}
}

// assertPass verifies a named check passed.
func assertPass(t *testing.T, checkMap map[string]CheckResult, name string) {
	t.Helper()
	r, ok := checkMap[name]
	if !ok {
		t.Errorf("check %q not found in results", name)
		return
	}
	if !r.Pass {
		t.Errorf("check %q should PASS but FAILED: %s", name, r.Message)
	}
}

// assertFail verifies a named check failed.
func assertFail(t *testing.T, checkMap map[string]CheckResult, name string) {
	t.Helper()
	r, ok := checkMap[name]
	if !ok {
		t.Errorf("check %q not found in results", name)
		return
	}
	if r.Pass {
		t.Errorf("check %q should FAIL but PASSED: %s", name, r.Message)
	}
}

// must calls t.Fatal on error.
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// resultNames extracts the Name field from a slice of CheckResult for error messages.
func resultNames(results []CheckResult) []string {
	names := make([]string, len(results))
	for i, r := range results {
		names[i] = r.Name
	}
	return names
}

func TestCheckPAMConfig(t *testing.T) {
	nb := Config{DesktopUser: "nb"}
	other := Config{DesktopUser: "other"}
	path := filepath.Join(t.TempDir(), "secrets-dispatcher")
	content, err := pamConfig("secrets-nb")
	must(t, err)
	must(t, os.WriteFile(path, content, 0644))

	if r := nb.checkPAMConfig(path); !r.Pass {
		t.Errorf("checkPAMConfig() failed on the provisioned file: %s", r.Message)
	}
	if r := other.checkPAMConfig(path); r.Pass {
		t.Error("checkPAMConfig() passed a file written for another companion user")
	}
	stale := "session optional pam_exec.so quiet /usr/bin/systemctl start --no-block secrets-dispatcher-companion@%u.service\n"
	must(t, os.WriteFile(path, []byte(stale), 0644))
	if r := nb.checkPAMConfig(path); r.Pass {
		t.Error("checkPAMConfig() passed a stale PAM config")
	}
	logoutToo := "session optional pam_exec.so quiet /usr/bin/systemctl --user --machine=secrets-nb@ start --no-block " + string(daemonUnit) + "\n"
	must(t, os.WriteFile(path, []byte(logoutToo), 0644))
	if r := nb.checkPAMConfig(path); r.Pass {
		t.Error("checkPAMConfig() passed a hook that also runs at logout")
	}
	if r := nb.checkPAMConfig(filepath.Join(t.TempDir(), "missing")); r.Pass {
		t.Error("checkPAMConfig() passed a missing file")
	}
}

func TestCheckPAMInclude(t *testing.T) {
	nb := Config{DesktopUser: "nb"}
	path := fakeSessionStack(t, debianCommonSession)
	if r := nb.checkPAMInclude(); r.Pass {
		t.Error("checkPAMInclude() passed a stack without the include line")
	}
	must(t, os.WriteFile(path, []byte(debianCommonSession+pamIncludeLine+"\n"), 0644))
	if r := nb.checkPAMInclude(); !r.Pass {
		t.Errorf("checkPAMInclude() failed on a stack with the include line: %s", r.Message)
	}
	testutil.Swap(t, &sessionStacks, []string{filepath.Join(t.TempDir(), "missing")})
	if r := nb.checkPAMInclude(); r.Pass {
		t.Error("checkPAMInclude() passed without a session stack")
	}
}
