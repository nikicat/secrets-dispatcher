package gpgsign

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

// Returns the first gpg in PATH that is not this binary, so the thin client
// never runs itself when it is installed as gpg.program. Errors when PATH has
// no other gpg.
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

// One call of gpg.program. Git signs with `--status-fd=2 -bsau <keyID>` and
// verifies with `--keyid-format=long --status-fd=1 --verify <sig> -`, which
// asks for no signature (both verified empirically).
type gpgCall interface {
	// Serves the call with the object or input on stdin, and returns the
	// exit status for git.
	run(c thinClient, stdin io.Reader) ExitStatus
}

// A call that asks for no signature, such as a verify, a key listing or a
// version probe. It goes to the real gpg unchanged (ADR 0008).
type passThroughCall struct {
	args []string
}

// Runs the real gpg with the call's arguments and the given stdin, and returns
// gpg's exit status.
func (p passThroughCall) run(c thinClient, stdin io.Reader) ExitStatus {
	c.debugf("non-signing invocation, delegating to real gpg")
	gpgPath, err := FindRealGPG()
	if err != nil {
		return systemError(fmt.Errorf("cannot locate real gpg for pass-through: %w", err))
	}
	cmd := exec.Command(gpgPath, p.args...)
	cmd.Stdin = stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			return ExitStatus(exitErr.ExitCode())
		}
		return systemError(fmt.Errorf("gpg pass-through failed: %w", err))
	}
	return exitOK
}

// A call that asks the daemon for a signature by the given key (ADR 0008).
type signCall struct {
	keyID approval.GPGKeyID
}

var errNoSigningKey = errors.New("signing needs a key (-u)")

// Returns the call that the given gpg arguments make. A sign option, or a flag
// cluster with 's' or 'b', asks for a signature by the key after a cluster that
// ends in 'u'. Errors with errNoSigningKey when such a call names no key.
func parseGPGArgs(args []string) (gpgCall, error) {
	sign := false
	var keyID approval.GPGKeyID
	for i, a := range args {
		switch a {
		case "--sign", "--detach-sign", "--clearsign", "--clear-sign":
			sign = true
		}
		flags := shortFlags(a)
		if strings.ContainsAny(flags, "sb") {
			sign = true
		}
		if keyID == "" && strings.HasSuffix(flags, "u") && i+1 < len(args) {
			keyID = approval.GPGKeyID(args[i+1])
		}
	}
	switch {
	case !sign:
		return passThroughCall{args: args}, nil
	case keyID == "":
		return nil, errNoSigningKey
	default:
		return signCall{keyID: keyID}, nil
	}
}

// Returns the letters of a short-flag cluster such as -bsau. Returns "" for a
// long --option, for the lone "-" that means stdin, and for an operand.
func shortFlags(arg string) string {
	if len(arg) < 2 || arg[0] != '-' || arg[1] == '-' {
		return ""
	}
	return arg[1:]
}
