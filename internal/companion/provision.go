package companion

import (
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"text/template"
)

// Config holds the inputs for provisioning a companion user.
type Config struct {
	// DesktopUser is the regular Linux username that owns the desktop session.
	// If empty, Provision attempts to read it from the SUDO_USER environment variable.
	DesktopUser string

	// CompanionName, if non-empty, overrides the default "secrets-{DesktopUser}" username.
	CompanionName string

	// HomeBase is the parent directory for companion homes.
	// Defaults to "/var/lib/secret-companion".
	HomeBase string
}

func (c *Config) defaults() {
	if c.HomeBase == "" {
		c.HomeBase = "/var/lib/secret-companion"
	}
}

// companionUsername returns the companion Linux username derived from cfg.
func (c Config) companionUsername() string {
	if c.CompanionName != "" {
		return c.CompanionName
	}
	return "secrets-" + c.DesktopUser
}

// homeDir returns the companion user's home directory.
func (c Config) homeDir() string {
	return filepath.Join(c.HomeBase, c.DesktopUser)
}

// Provision creates the companion user and all deployment artifacts for the
// secrets-dispatcher v2.0 privilege separation model.
//
// Steps performed (all idempotent):
//  1. Validate preconditions (root, non-empty user)
//  2. Create companion Linux user
//  3. Create home directory with 0700 permissions owned by companion user
//  4. Create gopass/GPG directory skeleton
//  5. Write D-Bus system bus policy file
//  6. Write systemd user unit file
//  7. Write PAM session hook config
//  8. Enable systemd linger for companion user
//
// Provision requires root (os.Geteuid() == 0).
func Provision(cfg Config) error {
	cfg.defaults()

	// 1. Validate preconditions.
	if geteuidFunc() != 0 {
		return fmt.Errorf("provision requires root; run: sudo secrets-dispatcher provision")
	}

	// If DesktopUser is empty, try SUDO_USER (set by sudo when running as root).
	if cfg.DesktopUser == "" {
		cfg.DesktopUser = os.Getenv("SUDO_USER")
	}
	if cfg.DesktopUser == "" {
		return fmt.Errorf("no desktop user specified: use --user flag or run via sudo")
	}

	companionUser := cfg.companionUsername()
	homeDir := cfg.homeDir()

	slog.Info("provisioning companion user",
		"desktop_user", cfg.DesktopUser,
		"companion_user", companionUser,
		"home_dir", homeDir,
	)

	// 2. Create companion user (idempotent: skip if already exists).
	if err := ensureUser(companionUser, homeDir); err != nil {
		return fmt.Errorf("ensure companion user: %w", err)
	}

	// Look up the companion user to get UID/GID for chown.
	u, err := userLookupFunc(companionUser)
	if err != nil {
		return fmt.Errorf("lookup companion user %q after creation: %w", companionUser, err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return fmt.Errorf("parse companion UID %q: %w", u.Uid, err)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return fmt.Errorf("parse companion GID %q: %w", u.Gid, err)
	}

	// 3. Create home directory with correct ownership and permissions.
	if err := ensureHomeDir(homeDir, uid, gid); err != nil {
		return fmt.Errorf("ensure home dir: %w", err)
	}

	// 4. Create gopass/GPG directory skeleton (PROV-03 Phase 4 scope = dirs only).
	if err := ensureDirectorySkeleton(homeDir, uid, gid); err != nil {
		return fmt.Errorf("ensure directory skeleton: %w", err)
	}

	// 5. Write D-Bus system bus policy file.
	if err := writeDBusPolicy(cfg.DesktopUser, companionUser); err != nil {
		return fmt.Errorf("write D-Bus policy: %w", err)
	}

	// 6. Write systemd user unit file to companion's XDG config directory.
	if err := writeSystemdUnit(homeDir, companionUser, u.Uid, uid, gid); err != nil {
		return fmt.Errorf("write systemd unit: %w", err)
	}

	// 7. Write PAM session hook config.
	if err := writePAMConfig(); err != nil {
		return fmt.Errorf("write PAM config: %w", err)
	}

	// 8. Enable linger so companion's systemd --user persists after desktop logout.
	slog.Info("enabling systemd linger", "companion_user", companionUser)
	if err := loginctlFunc("enable-linger", companionUser); err != nil {
		return fmt.Errorf("enable linger: %w", err)
	}

	slog.Info("provisioning complete", "companion_user", companionUser)
	return nil
}

// ensureUser creates the companion Linux user if it does not already exist.
func ensureUser(username, homeDir string) error {
	if _, err := userLookupFunc(username); err == nil {
		slog.Info("companion user already exists, skipping creation", "username", username)
		return nil
	}

	// Create parent directory first (root-owned, 0755).
	parentDir := filepath.Dir(homeDir)
	if err := mkdirAllFunc(parentDir, 0755); err != nil {
		return fmt.Errorf("create parent dir %q: %w", parentDir, err)
	}

	slog.Info("creating companion user", "username", username, "home_dir", homeDir)
	return userAddFunc(username, homeDir, "/usr/sbin/nologin")
}

// ensureHomeDir creates the home directory with 0700 permissions and chowns it to the companion user.
func ensureHomeDir(homeDir string, uid, gid int) error {
	if err := mkdirAllFunc(homeDir, 0700); err != nil {
		return fmt.Errorf("create home dir %q: %w", homeDir, err)
	}
	if err := chownFunc(homeDir, uid, gid); err != nil {
		return fmt.Errorf("chown home dir %q: %w", homeDir, err)
	}
	if err := chmodFunc(homeDir, 0700); err != nil {
		return fmt.Errorf("chmod home dir %q: %w", homeDir, err)
	}
	slog.Info("home directory ready", "path", homeDir)
	return nil
}

// ensureDirectorySkeleton creates the gopass and GPG directory skeleton under companion home.
// Phase 4 scope: directories only. Actual gopass init + GPG key generation is Phase 5.
func ensureDirectorySkeleton(homeDir string, uid, gid int) error {
	dirs := []string{
		filepath.Join(homeDir, ".config", "gopass"),
		filepath.Join(homeDir, ".gnupg"),
	}
	for _, d := range dirs {
		if err := mkdirAllFunc(d, 0700); err != nil {
			return fmt.Errorf("create dir %q: %w", d, err)
		}
		if err := chownFunc(d, uid, gid); err != nil {
			return fmt.Errorf("chown %q: %w", d, err)
		}
		if err := chmodFunc(d, 0700); err != nil {
			return fmt.Errorf("chmod %q: %w", d, err)
		}
		slog.Info("directory ready", "path", d)
	}
	return nil
}

// writeDBusPolicy renders and writes the D-Bus system bus policy file.
func writeDBusPolicy(desktopUser, companionUser string) error {
	type templateVars struct {
		CompanionUser string
		DesktopUser   string
	}

	content, err := renderTemplate("dbus-policy", dbusPolicyTemplate, templateVars{
		CompanionUser: companionUser,
		DesktopUser:   desktopUser,
	})
	if err != nil {
		return err
	}

	path := "/usr/share/dbus-1/system.d/net.mowaka.SecretsDispatcher1.conf"
	slog.Info("writing D-Bus policy", "path", path)
	return writeFileFunc(path, content, 0644)
}

// writeSystemdUnit renders and writes the systemd user unit file to the companion's XDG dir.
func writeSystemdUnit(homeDir, companionUser, uidStr string, uid, gid int) error {
	type templateVars struct {
		CompanionHome string
		CompanionUID  string
	}

	content, err := renderTemplate("systemd-unit", systemdUnitTemplate, templateVars{
		CompanionHome: homeDir,
		CompanionUID:  uidStr,
	})
	if err != nil {
		return err
	}

	unitDir := filepath.Join(homeDir, ".config", "systemd", "user")
	if err := mkdirAllFunc(unitDir, 0700); err != nil {
		return fmt.Errorf("create systemd unit dir %q: %w", unitDir, err)
	}
	if err := chownFunc(unitDir, uid, gid); err != nil {
		return fmt.Errorf("chown systemd unit dir %q: %w", unitDir, err)
	}

	unitPath := filepath.Join(unitDir, "secrets-dispatcher-daemon.service")
	slog.Info("writing systemd unit", "path", unitPath)
	if err := writeFileFunc(unitPath, content, 0644); err != nil {
		return err
	}
	return chownFunc(unitPath, uid, gid)
}

// writePAMConfig writes the PAM session hook configuration.
func writePAMConfig() error {
	path := "/etc/pam.d/secrets-dispatcher"
	slog.Info("writing PAM config", "path", path)
	return writeFileFunc(path, []byte(pamConfigTemplate), 0644)
}

// renderTemplate executes a text/template and returns the rendered bytes.
func renderTemplate(name, tmplStr string, data any) ([]byte, error) {
	tmpl, err := template.New(name).Parse(tmplStr)
	if err != nil {
		return nil, fmt.Errorf("parse template %q: %w", name, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("execute template %q: %w", name, err)
	}
	return buf.Bytes(), nil
}
