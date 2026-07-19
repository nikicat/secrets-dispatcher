package gpgsign

import (
	"errors"
	"fmt"
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

// isSignRequest reports whether the args git handed to gpg.program ask it to
// *create* a signature, as opposed to verifying one (git verify-commit,
// verify-tag, log --show-signature, merge/pull verification) or any other gpg
// operation (--list-keys, --version, ...).
//
// Git's signing invocation is `--status-fd=2 -bsau <keyID>`; the tell is the
// short-flag cluster carrying 's' (--sign) or 'b' (--detach-sign). Its verify
// invocation is `--keyid-format=long --status-fd=1 --verify <sig> -`, which
// carries neither (both verified empirically). Long options (--foo) and the
// lone "-" that means stdin never count. This proxy can only create signatures
// (via the daemon), so anything that is not a sign request is delegated to the
// real gpg by the caller.
func isSignRequest(args []string) bool {
	for _, a := range args {
		switch a {
		case "--sign", "--detach-sign", "--clearsign", "--clear-sign":
			return true
		}
		// Short-flag cluster like -bsau, but not a long --option nor the lone
		// "-" (stdin). 's' = sign, 'b' = detach-sign.
		if len(a) >= 2 && a[0] == '-' && a[1] != '-' && strings.ContainsAny(a[1:], "sb") {
			return true
		}
	}
	return false
}

// passThroughToGPG execs the real gpg binary with the exact args git passed,
// wiring stdin/stdout/stderr straight through, and returns gpg's own exit code.
// It is used for every non-signing invocation (signature verification, key
// listing, --version probing): git captures the child's stdout for the GNUPG
// status lines and its exit code for pass/fail, so forwarding all three
// verbatim makes gpg.program behave exactly as plain gpg would. Without it, git
// would verify a validly-signed commit by calling this proxy, get no GOODSIG
// back, and report the commit as unsigned.
func passThroughToGPG(args []string, stdin io.Reader) int {
	gpgPath, err := FindRealGPG()
	if err != nil {
		fmt.Fprintf(os.Stderr, "secrets-dispatcher: cannot locate real gpg for pass-through: %v\n", err)
		return 2
	}
	cmd := exec.Command(gpgPath, args...)
	cmd.Stdin = stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(os.Stderr, "secrets-dispatcher: gpg pass-through failed: %v\n", err)
		return 2
	}
	return 0
}

// extractKeyID parses the GPG key ID from the args that git passes to
// gpg.program. Git's invocation is:
//
//	--status-fd=2 -bsau <keyID>
//
// The key ID follows the -u flag, which may be embedded in a combined short
// flag like -bsau (the last character is 'u', so the next argument is the
// key ID). It may also appear as a standalone -u flag.
//
// Returns the key ID string, or an empty string if not found.
func extractKeyID(args []string) string {
	for i, arg := range args {
		// Standalone -u flag: the next argument is the key ID.
		if arg == "-u" {
			if i+1 < len(args) {
				return args[i+1]
			}
			return ""
		}
		// Combined short flag ending in 'u' (e.g. -bsau): next arg is key ID.
		if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.HasSuffix(arg, "u") {
			if i+1 < len(args) {
				return args[i+1]
			}
			return ""
		}
	}
	return ""
}
