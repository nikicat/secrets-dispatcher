// Package companion provides provisioning and validation logic for the
// secrets-dispatcher companion user (v2.0 privilege separation).
package companion

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
)

// Injectable system call function variables. Unit tests swap these out to avoid
// requiring root, real users, or real system tools. This follows the same pattern
// as internal/service/install.go (systemctlFunc).

var (
	userAddFunc     = defaultUserAdd
	loginctlFunc    = defaultLoginctl
	userLookupFunc  = defaultUserLookup
	mkdirAllFunc    = os.MkdirAll
	chownFunc       = os.Lchown
	chmodFunc       = os.Chmod
	writeFileFunc   = os.WriteFile
	geteuidFunc     = os.Geteuid
)

// defaultUserAdd runs: useradd --home-dir <homeDir> --no-create-home --shell <shell> <username>
// NOTE: No --system flag — companion user needs a regular UID range so that
// systemd --user works correctly. See RESEARCH.md Pitfall 2.
func defaultUserAdd(username, homeDir, shell string) error {
	cmd := exec.Command("useradd",
		"--home-dir", homeDir,
		"--no-create-home",
		"--shell", shell,
		username,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("useradd: %w", err)
	}
	return nil
}

// defaultLoginctl runs loginctl with the given arguments.
func defaultLoginctl(args ...string) error {
	cmd := exec.Command("loginctl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("loginctl %v: %w", args, err)
	}
	return nil
}

// defaultUserLookup wraps os/user.Lookup for testability.
func defaultUserLookup(username string) (*user.User, error) {
	return user.Lookup(username)
}
