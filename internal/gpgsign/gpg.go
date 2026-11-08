package gpgsign

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// FindRealGPG locates the real gpg binary in PATH, skipping any candidate
// that is the same file as the running executable (using inode comparison via
// os.SameFile). This prevents the thin client from calling itself when it is
// installed as gpg.program.
//
// Returns the absolute path to the real gpg binary, or an error if none is
// found.
func FindRealGPG() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	selfInfo, err := os.Stat(self)
	if err != nil {
		return "", err
	}

	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		candidate := filepath.Join(dir, "gpg")
		info, err := os.Stat(candidate)
		if err != nil {
			continue
		}
		if os.SameFile(selfInfo, info) {
			continue // skip self
		}
		return candidate, nil
	}
	return "", errors.New("gpg not found in PATH")
}

// What git asks gpg.program to do, read once from the arguments it passes.
//
// Git's signing invocation is `--status-fd=2 -bsau <keyID>`. Its verify
// invocation is `--keyid-format=long --status-fd=1 --verify <sig> -`, which
// asks for no signature (both verified empirically).
type gpgArgs struct {
	// Picks the daemon or the real gpg, as Run explains.
	sign bool
	// Names the signing key in the approval prompt. Empty when no -u is given.
	keyID string
}

// Parses the arguments git passes to gpg.program. A long sign option, or a
// short-flag cluster carrying 's' (--sign) or 'b' (--detach-sign), asks for a
// signature. The key ID is the argument after the first cluster that ends in
// 'u', such as -u or -bsau.
func parseGPGArgs(args []string) gpgArgs {
	var g gpgArgs
	for i, a := range args {
		switch a {
		case "--sign", "--detach-sign", "--clearsign", "--clear-sign":
			g.sign = true
		}
		flags := shortFlags(a)
		if strings.ContainsAny(flags, "sb") {
			g.sign = true
		}
		if g.keyID == "" && strings.HasSuffix(flags, "u") && i+1 < len(args) {
			g.keyID = args[i+1]
		}
	}
	return g
}

// Returns the letters of a short-flag cluster such as -bsau. Returns "" for a
// long --option, for the lone "-" that means stdin, and for an operand.
func shortFlags(arg string) string {
	if len(arg) < 2 || arg[0] != '-' || arg[1] == '-' {
		return ""
	}
	return arg[1:]
}

// Runs the real gpg with args, stdin and the process's stdout and stderr, and
// returns gpg's exit status.
func passThroughToGPG(args []string, stdin io.Reader) ExitStatus {
	gpgPath, err := FindRealGPG()
	if err != nil {
		errorf("cannot locate real gpg for pass-through: %v", err)
		return exitSystemError
	}
	cmd := exec.Command(gpgPath, args...)
	cmd.Stdin = stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return ExitStatus(exitErr.ExitCode())
		}
		errorf("gpg pass-through failed: %v", err)
		return exitSystemError
	}
	return exitOK
}
