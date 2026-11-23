package companion

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/nikicat/secrets-dispatcher/internal/procutil"
	"github.com/nikicat/secrets-dispatcher/internal/systemd"
)

// The default of Config.HomeBase.
const DefaultHomeBase = "/var/lib/secret-companion"

// The inputs for provisioning a companion user.
type Config struct {
	// The Linux user that owns the desktop session.
	DesktopUser string

	// Overrides the default companion username "secrets-{DesktopUser}".
	CompanionName string

	// The parent directory of companion homes, or empty for DefaultHomeBase.
	HomeBase string
}

func (c *Config) defaults() {
	if c.HomeBase == "" {
		c.HomeBase = DefaultHomeBase
	}
}

func (c Config) companionUsername() string {
	if c.CompanionName != "" {
		return c.CompanionName
	}
	return "secrets-" + c.DesktopUser
}

func (c Config) homeDir() companionHome {
	return companionHome(filepath.Join(c.HomeBase, c.DesktopUser))
}

// Creates the companion user and its deployment, part of the privilege
// separation experiment (ADR 0002). It can run again, and each run rewrites the
// deployment's files. Errors when not run as root or no desktop user is given.
func Provision(cfg Config) error {
	cfg.defaults()

	if geteuidFunc() != procutil.RootUID {
		return fmt.Errorf("provision requires root; run: sudo secrets-dispatcher provision")
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

	if err := ensureUser(companionUser, homeDir); err != nil {
		return fmt.Errorf("ensure companion user: %w", err)
	}

	acct, err := lookupAccount(companionUser)
	if err != nil {
		return fmt.Errorf("lookup companion user %q after creation: %w", companionUser, err)
	}
	c := companion{account: acct, home: homeDir}

	if err := c.home.Dir().ensure(c.account, 0700); err != nil {
		return fmt.Errorf("ensure home dir: %w", err)
	}

	if err := c.ensureDirectorySkeleton(); err != nil {
		return fmt.Errorf("ensure directory skeleton: %w", err)
	}

	if err := c.writeDBusPolicy(cfg.DesktopUser); err != nil {
		return fmt.Errorf("write D-Bus policy: %w", err)
	}

	if err := c.writeSystemdUnit(); err != nil {
		return fmt.Errorf("write systemd unit: %w", err)
	}

	if err := writePAMConfig(c.name); err != nil {
		return fmt.Errorf("write PAM config: %w", err)
	}

	if err := includePAMConfig(); err != nil {
		return fmt.Errorf("include PAM config: %w", err)
	}

	// Linger keeps the companion's systemd user manager running after the desktop
	// user logs out.
	slog.Info("enabling systemd linger", "companion_user", c.name)
	if err := loginctlFunc("enable-linger", c.name); err != nil {
		return fmt.Errorf("enable linger: %w", err)
	}

	slog.Info("provisioning complete", "companion_user", c.name)
	return nil
}

// Creates the given companion Linux user unless it exists.
func ensureUser(username string, homeDir companionHome) error {
	if _, err := userLookupFunc(username); err == nil {
		slog.Info("companion user already exists, skipping creation", "username", username)
		return nil
	}

	// Create parent directory first (root-owned, 0755).
	parentDir := filepath.Dir(homeDir.String())
	if err := mkdirAllFunc(parentDir, 0755); err != nil {
		return fmt.Errorf("create parent dir %q: %w", parentDir, err)
	}

	slog.Info("creating companion user", "username", username, "home_dir", homeDir)
	return userAddFunc(username, homeDir.String(), "/usr/sbin/nologin")
}

// Creates the companion's gopass and GPG directories, empty.
func (c companion) ensureDirectorySkeleton() error {
	for _, d := range []companionDir{c.home.GopassDir(), c.home.GnupgDir()} {
		if err := d.ensure(c.account, 0700); err != nil {
			return err
		}
	}
	return nil
}

// Writes the system bus policy that lets the given desktop user call the
// companion daemon.
func (c companion) writeDBusPolicy(desktopUser string) error {
	type templateVars struct {
		CompanionUser string
		DesktopUser   string
	}

	content, err := renderTemplate("dbus-policy", dbusPolicyTemplate, templateVars{
		CompanionUser: c.name,
		DesktopUser:   desktopUser,
	})
	if err != nil {
		return err
	}

	path := "/usr/share/dbus-1/system.d/net.mowaka.SecretsDispatcher1.conf"
	slog.Info("writing D-Bus policy", "path", path)
	return writeFileFunc(path, content, 0644)
}

// The companion daemon's systemd user unit.
const daemonUnit systemd.Unit = "secrets-dispatcher-daemon.service"

// Writes the daemon's systemd user unit into the companion's XDG config
// directory.
func (c companion) writeSystemdUnit() error {
	type templateVars struct {
		CompanionHome companionHome
		CompanionUID  procutil.UID
	}

	content, err := renderTemplate("systemd-unit", systemdUnitTemplate, templateVars{
		CompanionHome: c.home,
		CompanionUID:  c.uid,
	})
	if err != nil {
		return err
	}

	unitFile := companionFile{c.home.UnitDir().UnitFile(daemonUnit).File}
	if err := unitFile.dir().ensure(c.account, 0700); err != nil {
		return err
	}
	slog.Info("writing systemd unit", "path", unitFile)
	return unitFile.write(c.account, content, 0644)
}

// The PAM service name of the session hook.
const pamService = "secrets-dispatcher"

// The PAM session hook file.
const pamConfigPath = "/etc/pam.d/" + pamService

// The session stack line that runs the hook.
const pamIncludeLine = "session include " + pamService

// Renders the PAM session hook that starts daemonUnit under companionUser's
// systemd user manager.
func pamConfig(companionUser string) ([]byte, error) {
	type templateVars struct {
		CompanionUser string
		DaemonUnit    systemd.Unit
	}
	return renderTemplate("pam-config", pamConfigTemplate, templateVars{
		CompanionUser: companionUser,
		DaemonUnit:    daemonUnit,
	})
}

func writePAMConfig(companionUser string) error {
	content, err := pamConfig(companionUser)
	if err != nil {
		return err
	}
	slog.Info("writing PAM config", "path", pamConfigPath)
	return writeFileFunc(pamConfigPath, content, 0644)
}

// Session stacks that login services include, in the order provision tries
// them: Debian's common-session, whose lines after the pam-auth-update block
// survive pam-auth-update, and Arch's system-login. Fedora's authselect
// rewrites its stacks, so none of them is listed.
var sessionStacks = []string{"/etc/pam.d/common-session", "/etc/pam.d/system-login"}

// A session stack file as read from disk.
type sessionStack struct {
	path    string
	content []byte
	mode    fs.FileMode
}

// Reads the first of sessionStacks that exists. Errors when none does, naming
// the line to add by hand.
func findSessionStack() (sessionStack, error) {
	for _, path := range sessionStacks {
		info, err := os.Stat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return sessionStack{}, err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return sessionStack{}, err
		}
		return sessionStack{path: path, content: content, mode: info.Mode().Perm()}, nil
	}
	return sessionStack{}, fmt.Errorf("none of %s exists; add %q to the session stack your login services include",
		strings.Join(sessionStacks, ", "), pamIncludeLine)
}

// Checks whether the stack already runs the hook.
func (s sessionStack) includesHook() bool {
	for line := range bytes.Lines(s.content) {
		if string(bytes.TrimSpace(line)) == pamIncludeLine {
			return true
		}
	}
	return false
}

// Appends the hook's include line to the session stack, after a marker
// comment, unless the stack already has it.
func includePAMConfig() error {
	stack, err := findSessionStack()
	if err != nil {
		return err
	}
	if stack.includesHook() {
		slog.Info("session stack already includes PAM config", "path", stack.path)
		return nil
	}
	content := stack.content
	if len(content) > 0 && content[len(content)-1] != '\n' {
		content = append(content, '\n')
	}
	content = append(content, "# Added by secrets-dispatcher provision.\n"+pamIncludeLine+"\n"...)
	slog.Info("including PAM config in session stack", "path", stack.path)
	return writeFileFunc(stack.path, content, stack.mode)
}

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
