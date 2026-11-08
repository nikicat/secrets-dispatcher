package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// The directory name of the app under each XDG base directory.
const appName = "secrets-dispatcher"

// Returns the current user's XDG config home.
func ConfigHome() (string, error) { return xdgBase("XDG_CONFIG_HOME", ".config") }

// Returns the current user's XDG data home.
func DataHome() (string, error) {
	return xdgBase("XDG_DATA_HOME", filepath.Join(".local", "share"))
}

// Returns the current user's XDG state home.
func stateHome() (string, error) {
	return xdgBase("XDG_STATE_HOME", filepath.Join(".local", "state"))
}

// Returns the XDG base directory that the variable env names, or fallback
// under the home directory when env is unset or not absolute. The XDG base
// directory spec says to ignore a relative value. Errors when the fallback is
// needed and the user has no home directory.
func xdgBase(env, fallback string) (string, error) {
	base := os.Getenv(env)
	if filepath.IsAbs(base) {
		return base, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}
	return filepath.Join(home, fallback), nil
}
