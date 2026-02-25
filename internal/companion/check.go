package companion

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
)

// CheckResult holds the outcome of a single deployment validation check.
type CheckResult struct {
	Name    string // Short human-readable check name
	Pass    bool   // true if the check passed
	Message string // On failure: actionable fix hint. On pass: brief confirmation.
}

// Check validates the full companion user deployment and returns a slice of
// CheckResult, one per component. All checks are read-only; Check does not
// require root.
func Check(cfg Config) []CheckResult {
	cfg.defaults()

	// Resolve desktop user from SUDO_USER if unset (non-fatal for check).
	if cfg.DesktopUser == "" {
		cfg.DesktopUser = os.Getenv("SUDO_USER")
	}

	companionUser := cfg.companionUsername()
	homeDir := cfg.homeDir()

	var results []CheckResult

	// 1. Companion user exists.
	u, userErr := userLookupFunc(companionUser)
	results = append(results, CheckResult{
		Name:    "companion user exists",
		Pass:    userErr == nil,
		Message: passOrFix(userErr == nil,
			fmt.Sprintf("user %q found (uid=%s)", companionUser, safeUID(u)),
			fmt.Sprintf("run: sudo secrets-dispatcher provision --user %s", cfg.DesktopUser),
		),
	})

	// 2. Home directory exists and has correct permissions.
	homeStat, homeErr := os.Stat(homeDir)
	homeExists := homeErr == nil
	results = append(results, CheckResult{
		Name: "home directory exists",
		Pass: homeExists,
		Message: passOrFix(homeExists,
			fmt.Sprintf("directory %q exists", homeDir),
			fmt.Sprintf("run: sudo secrets-dispatcher provision --user %s", cfg.DesktopUser),
		),
	})

	homeMode0700 := homeExists && homeStat.Mode().Perm() == 0700
	results = append(results, CheckResult{
		Name: "home directory mode 0700",
		Pass: homeMode0700,
		Message: passOrFix(homeMode0700,
			"home directory has correct permissions (0700)",
			fmt.Sprintf("run: sudo chmod 0700 %s", homeDir),
		),
	})

	// 3. Home directory owned by companion user.
	homeOwnedByCompanion := false
	if homeExists && userErr == nil {
		uid, _ := strconv.Atoi(u.Uid)
		homeOwnedByCompanion = fileOwnedByUID(homeDir, uid)
	}
	results = append(results, CheckResult{
		Name: "home directory owned by companion user",
		Pass: homeOwnedByCompanion,
		Message: passOrFix(homeOwnedByCompanion,
			fmt.Sprintf("home directory owned by %q", companionUser),
			fmt.Sprintf("run: sudo chown %s:%s %s", companionUser, companionUser, homeDir),
		),
	})

	// 4. gopass config directory exists.
	gopassDir := filepath.Join(homeDir, ".config", "gopass")
	gopassExists := dirExists(gopassDir)
	results = append(results, CheckResult{
		Name: "gopass config directory exists",
		Pass: gopassExists,
		Message: passOrFix(gopassExists,
			fmt.Sprintf("directory %q exists", gopassDir),
			fmt.Sprintf("run: sudo secrets-dispatcher provision --user %s", cfg.DesktopUser),
		),
	})

	// 5. GPG home directory exists.
	gnupgDir := filepath.Join(homeDir, ".gnupg")
	gnupgExists := dirExists(gnupgDir)
	results = append(results, CheckResult{
		Name: "GPG home directory exists",
		Pass: gnupgExists,
		Message: passOrFix(gnupgExists,
			fmt.Sprintf("directory %q exists", gnupgDir),
			fmt.Sprintf("run: sudo secrets-dispatcher provision --user %s", cfg.DesktopUser),
		),
	})

	// 6. D-Bus policy file exists.
	dbusPolicy := "/usr/share/dbus-1/system.d/net.mowaka.SecretsDispatcher1.conf"
	dbusPolicyExists := fileExists(dbusPolicy)
	results = append(results, CheckResult{
		Name: "D-Bus policy file exists",
		Pass: dbusPolicyExists,
		Message: passOrFix(dbusPolicyExists,
			fmt.Sprintf("policy file %q exists", dbusPolicy),
			fmt.Sprintf("run: sudo secrets-dispatcher provision --user %s", cfg.DesktopUser),
		),
	})

	// 7. systemd user unit file exists.
	unitFile := filepath.Join(homeDir, ".config", "systemd", "user", "secrets-dispatcher-daemon.service")
	unitExists := fileExists(unitFile)
	results = append(results, CheckResult{
		Name: "systemd user unit file exists",
		Pass: unitExists,
		Message: passOrFix(unitExists,
			fmt.Sprintf("unit file %q exists", unitFile),
			fmt.Sprintf("run: sudo secrets-dispatcher provision --user %s", cfg.DesktopUser),
		),
	})

	// 8. PAM config exists.
	pamConfig := "/etc/pam.d/secrets-dispatcher"
	pamExists := fileExists(pamConfig)
	results = append(results, CheckResult{
		Name: "PAM config exists",
		Pass: pamExists,
		Message: passOrFix(pamExists,
			fmt.Sprintf("PAM config %q exists", pamConfig),
			fmt.Sprintf("run: sudo secrets-dispatcher provision --user %s", cfg.DesktopUser),
		),
	})

	// 9. Linger enabled (indicated by presence of /var/lib/systemd/linger/{username}).
	lingerFile := filepath.Join("/var/lib/systemd/linger", companionUser)
	lingerEnabled := fileExists(lingerFile)
	results = append(results, CheckResult{
		Name: "systemd linger enabled",
		Pass: lingerEnabled,
		Message: passOrFix(lingerEnabled,
			fmt.Sprintf("linger enabled for %q (%s exists)", companionUser, lingerFile),
			fmt.Sprintf("run: sudo loginctl enable-linger %s", companionUser),
		),
	})

	return results
}

// passOrFix returns passMsg when ok is true and fixMsg otherwise.
func passOrFix(ok bool, passMsg, fixMsg string) string {
	if ok {
		return passMsg
	}
	return fixMsg
}

// safeUID returns the UID string from a *user.User, or "?" if u is nil.
func safeUID(u *user.User) string {
	if u == nil {
		return "?"
	}
	return u.Uid
}

// fileExists returns true if path exists and is a regular file or symlink.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// dirExists returns true if path exists and is a directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// fileOwnedByUID returns true if path's owner UID matches uid.
func fileOwnedByUID(path string, uid int) bool {
	return statUID(path) == uid
}

// statUID returns the owning UID of path, or -1 on error.
func statUID(path string) int {
	info, err := os.Stat(path)
	if err != nil {
		return -1
	}
	sys, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return -1
	}
	return int(sys.Uid)
}
