package gpgsign

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nikicat/secrets-dispatcher/internal/unixsock"
)

// The secrets-dispatcher subcommand that git runs as its gpg.program (ADR
// 0008). Wrapper scripts that earlier setups wrote run it by this name.
const Command = "gpg-sign"

// Selects the git config file that SetupGitConfig writes gpg.program into.
type GitConfigScope int

const (
	// The user's global git config.
	ScopeGlobal GitConfigScope = iota
	// The current repository's git config.
	ScopeLocal
)

// Returns the scope's name as git spells it: "global" or "local".
func (s GitConfigScope) String() string {
	if s == ScopeLocal {
		return "local"
	}
	return "global"
}

// Returns the git config option that selects the scope.
func (s GitConfigScope) flag() string {
	return "--" + s.String()
}

// Writes a wrapper script that runs gpg-sign and sets git's gpg.program to it
// in scope, leaving commit.gpgsign alone (ADR 0008). When remoteSocket is set,
// the wrapper signs through the socket at that absolute path (ADR 0011).
func SetupGitConfig(scope GitConfigScope, remoteSocket unixsock.Path) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find executable: %w", err)
	}
	// Resolve symlinks so the wrapper calls the real binary path.
	self, err = filepath.EvalSymlinks(self)
	if err != nil {
		return fmt.Errorf("resolve executable: %w", err)
	}

	// Write shell wrapper to ~/.local/bin/secrets-dispatcher-gpg.
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("get home dir: %w", err)
	}
	wrapperDir := filepath.Join(home, ".local", "bin")
	wrapperPath := filepath.Join(wrapperDir, "secrets-dispatcher-gpg")

	if err := os.MkdirAll(wrapperDir, 0755); err != nil {
		return fmt.Errorf("create wrapper dir: %w", err)
	}

	if err := os.WriteFile(wrapperPath, []byte(wrapperScript(self, remoteSocket)), 0755); err != nil {
		return fmt.Errorf("write wrapper: %w", err)
	}

	// Configure git gpg.program to point at the wrapper.
	if err := exec.Command("git", "config", scope.flag(), "gpg.program", wrapperPath).Run(); err != nil {
		return fmt.Errorf("git config: %w", err)
	}

	fmt.Printf("Wrote wrapper: %s\n", wrapperPath)
	fmt.Printf("Configured git %s gpg.program = %s\n", scope, wrapperPath)
	fmt.Println("\nNote: Ensure ~/.local/bin is in your PATH.")
	fmt.Println("This does NOT enable commit.gpgsign — use 'git config --global commit.gpgsign true' to auto-sign all commits.")
	return nil
}

// Returns the gpg.program wrapper that execs bin, exporting
// remoteSocketEnv first when remoteSocket is non-empty.
func wrapperScript(bin string, remoteSocket unixsock.Path) string {
	env := ""
	if remoteSocket != "" {
		env = fmt.Sprintf("export %s=%s\n", remoteSocketEnv, shellQuote(string(remoteSocket)))
	}
	return fmt.Sprintf("#!/bin/sh\n%sexec %s %s \"$@\"\n", env, shellQuote(bin), Command)
}

// Returns s as one single-quoted sh word.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
