package gpgsign

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// SetupGitConfig writes a shell wrapper script and configures git's gpg.program.
// scope is "global" (default) or "local" (per-repo).
//
// A wrapper script is required because git does NOT shell-split gpg.program —
// it uses execvp, so "secrets-dispatcher gpg-sign" (with a space) would fail.
// The wrapper calls: exec secrets-dispatcher gpg-sign "$@"
//
// Per CONTEXT.md locked decisions:
//   - Setup only sets gpg.program; does NOT enable commit.gpgsign=true
//   - Defaults to --global; caller can pass "local" for per-repo config
func SetupGitConfig(scope string) error {
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

	content := fmt.Sprintf("#!/bin/sh\nexec %s gpg-sign \"$@\"\n", self)
	if err := os.WriteFile(wrapperPath, []byte(content), 0755); err != nil {
		return fmt.Errorf("write wrapper: %w", err)
	}

	// Configure git gpg.program to point at the wrapper.
	gitArgs := []string{"config"}
	if scope == "local" {
		gitArgs = append(gitArgs, "--local")
	} else {
		gitArgs = append(gitArgs, "--global")
	}
	gitArgs = append(gitArgs, "gpg.program", wrapperPath)
	if err := exec.Command("git", gitArgs...).Run(); err != nil {
		return fmt.Errorf("git config: %w", err)
	}

	fmt.Printf("Wrote wrapper: %s\n", wrapperPath)
	fmt.Printf("Configured git %s gpg.program = %s\n", scope, wrapperPath)
	fmt.Println("\nNote: Ensure ~/.local/bin is in your PATH.")
	fmt.Println("This does NOT enable commit.gpgsign — use 'git config --global commit.gpgsign true' to auto-sign all commits.")
	return nil
}
