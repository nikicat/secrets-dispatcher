package companion

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

// The outcome of one deployment check.
type CheckResult struct {
	Name    string // Short human-readable check name
	Pass    bool
	Message string // On failure: actionable fix hint. On pass: brief confirmation.
}

// Returns the result as one report line, such as "[PASS] name: message".
func (r CheckResult) String() string {
	status := "[PASS]"
	if !r.Pass {
		status = "[FAIL]"
	}
	return fmt.Sprintf("%s %s: %s", status, r.Name, r.Message)
}

// Checks whether every result passed.
func AllPass(results []CheckResult) bool {
	return !slices.ContainsFunc(results, func(r CheckResult) bool { return !r.Pass })
}

// Returns one result for each check of the companion user's deployment. The
// checks only read, so they need no root.
func Check(cfg Config) []CheckResult {
	cfg.defaults()

	companionUser := cfg.companionUsername()

	var results []CheckResult

	// 1. Companion user exists.
	acct, userErr := lookupAccount(companionUser)
	c := companion{account: acct, home: cfg.homeDir()}
	home := c.home.Dir()
	results = append(results, checkResult("companion user exists", userErr == nil,
		fmt.Sprintf("user %q found (uid=%d)", companionUser, c.uid),
		cfg.provisionHint(),
	))

	// 2. Home directory exists and has correct permissions.
	results = append(results, checkResult("home directory exists", home.isDir(),
		fmt.Sprintf("directory %q exists", home),
		cfg.provisionHint(),
	))

	perm, permErr := home.perm()
	results = append(results, checkResult("home directory mode 0700", permErr == nil && perm == 0700,
		"home directory has correct permissions (0700)",
		fmt.Sprintf("run: sudo chmod 0700 %s", home),
	))

	// 3. Home directory owned by companion user.
	owner, ownerErr := home.ownerUID()
	results = append(results, checkResult("home directory owned by companion user", userErr == nil && ownerErr == nil && owner == c.uid,
		fmt.Sprintf("home directory owned by %q", companionUser),
		fmt.Sprintf("run: sudo chown %s:%s %s", companionUser, companionUser, home),
	))

	// 4. gopass config directory exists.
	gopassDir := c.home.GopassDir()
	gopassExists := gopassDir.isDir()
	results = append(results, checkResult("gopass config directory exists", gopassExists,
		fmt.Sprintf("directory %q exists", gopassDir),
		cfg.provisionHint(),
	))

	// 5. GPG home directory exists.
	gnupgDir := c.home.GnupgDir()
	gnupgExists := gnupgDir.isDir()
	results = append(results, checkResult("GPG home directory exists", gnupgExists,
		fmt.Sprintf("directory %q exists", gnupgDir),
		cfg.provisionHint(),
	))

	// 6. D-Bus policy file exists.
	dbusPolicy := "/usr/share/dbus-1/system.d/net.mowaka.SecretsDispatcher1.conf"
	dbusPolicyExists := fileExists(dbusPolicy)
	results = append(results, checkResult("D-Bus policy file exists", dbusPolicyExists,
		fmt.Sprintf("policy file %q exists", dbusPolicy),
		cfg.provisionHint(),
	))

	// 7. systemd user unit file exists.
	unitFile := c.home.UnitDir().UnitFile(daemonUnit)
	unitExists := unitFile.Exists()
	results = append(results, checkResult("systemd user unit file exists", unitExists,
		fmt.Sprintf("unit file %q exists", unitFile),
		cfg.provisionHint(),
	))

	// 8. PAM config starts the daemon under the companion user's manager.
	results = append(results, cfg.checkPAMConfig(pamConfigPath))

	// 9. The login session stack runs the PAM config.
	results = append(results, cfg.checkPAMInclude())

	// 10. Linger enabled (indicated by presence of /var/lib/systemd/linger/{username}).
	lingerFile := filepath.Join("/var/lib/systemd/linger", companionUser)
	lingerEnabled := fileExists(lingerFile)
	results = append(results, checkResult("systemd linger enabled", lingerEnabled,
		fmt.Sprintf("linger enabled for %q (%s exists)", companionUser, lingerFile),
		fmt.Sprintf("run: sudo loginctl enable-linger %s", companionUser),
	))

	return results
}

// Checks that the PAM config at path is the one provision writes for c's
// companion user.
func (c Config) checkPAMConfig(path string) CheckResult {
	companionUser := c.companionUsername()
	want, err := pamConfig(companionUser)
	if err != nil {
		return CheckResult{Name: "PAM config current", Message: err.Error()}
	}
	got, readErr := os.ReadFile(path)
	current := readErr == nil && bytes.Equal(got, want)
	return checkResult("PAM config current", current,
		fmt.Sprintf("PAM config %q starts %s for %q", path, daemonUnit, companionUser),
		c.provisionHint(),
	)
}

// Checks that the login session stack includes the PAM config.
func (c Config) checkPAMInclude() CheckResult {
	stack, err := findSessionStack()
	if err != nil {
		return CheckResult{Name: "PAM config included", Message: err.Error()}
	}
	included := stack.includesHook()
	return checkResult("PAM config included", included,
		fmt.Sprintf("session stack %q has %q", stack.path, pamIncludeLine),
		c.provisionHint(),
	)
}

// Returns the result of the check name: passed with passMsg when ok, failed
// with the fix hint fixMsg otherwise.
func checkResult(name string, ok bool, passMsg, fixMsg string) CheckResult {
	if ok {
		return CheckResult{Name: name, Pass: true, Message: passMsg}
	}
	return CheckResult{Name: name, Message: fixMsg}
}

// Returns the fix hint for anything provision installs.
func (c Config) provisionHint() string {
	return fmt.Sprintf("run: sudo secrets-dispatcher provision --user %s", c.DesktopUser)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
