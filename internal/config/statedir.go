package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The directory where the daemon keeps what survives its restarts, such as the
// API auth token. Holds an absolute path, or is empty for the default.
type StateDir string

// The auth token's file name in the state directory.
const cookieName = ".cookie"

// Parses a state directory. An empty text leaves it unset for OrDefault.
// Errors when text is a relative path.
func (d *StateDir) UnmarshalText(text []byte) error {
	v := StateDir(text)
	if v != "" && !filepath.IsAbs(string(v)) {
		return fmt.Errorf("state directory must be an absolute path: %q", v)
	}
	*d = v
	return nil
}

// Returns the current user's default state directory, under the XDG state home.
func LookupStateDir() (StateDir, error) {
	base, err := stateHome()
	if err != nil {
		return "", fmt.Errorf("find the state directory: %w", err)
	}
	return StateDir(filepath.Join(base, appName)), nil
}

// Returns d, or the current user's default state directory when d is empty.
func (d StateDir) OrDefault() (StateDir, error) {
	if d != "" {
		return d, nil
	}
	return LookupStateDir()
}

func (d StateDir) String() string { return string(d) }

// Sets the directory from a command-line flag, as flag.Value does. Resolves a
// relative path against the current directory.
func (d *StateDir) Set(value string) error {
	if value == "" {
		*d = ""
		return nil
	}
	abs, err := filepath.Abs(value)
	if err != nil {
		return err
	}
	*d = StateDir(abs)
	return nil
}

// Returns the file that holds the API's auth token.
func (d StateDir) Cookie() string { return filepath.Join(string(d), cookieName) }

// Reads the API's auth token from the cookie file. The error wraps
// os.ErrNotExist when the daemon has never written one.
func (d StateDir) ReadToken() (AuthToken, error) {
	path := d.Cookie()
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", fmt.Errorf("cookie file %s is empty", path)
	}
	return AuthToken(token), nil
}

// Writes the API's auth token to the cookie file, readable by the owner only.
// Creates the state directory, also owner-only, when it does not exist.
func (d StateDir) WriteToken(token AuthToken) error {
	if err := os.MkdirAll(string(d), 0o700); err != nil {
		return err
	}
	return os.WriteFile(d.Cookie(), []byte(token), 0o600)
}
