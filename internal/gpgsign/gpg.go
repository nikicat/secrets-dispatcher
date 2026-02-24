package gpgsign

import (
	"errors"
	"os"
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
