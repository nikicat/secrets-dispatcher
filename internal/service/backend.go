package service

import (
	"fmt"
	"slices"
	"strings"

	"github.com/nikicat/secrets-dispatcher/internal/config"
)

// The private Secret Service that a local or full install runs behind the
// dispatcher (ADR 0001). It is one of BackendKeywords, a command line that
// contains a slash, or empty to follow the detected provider (ADR 0009).
type Backend string

const (
	BackendGopass       Backend = "gopass"
	BackendGnomeKeyring Backend = "gnome-keyring"
)

type backendKeyword struct {
	name Backend
	exec func() (string, error)
}

var backendKeywords = []backendKeyword{
	{BackendGopass, gopassExec},
	{BackendGnomeKeyring, gnomeKeyringExec},
}

// Returns the keywords that Backend.Set accepts, comma-separated.
func BackendKeywords() string {
	names := make([]string, len(backendKeywords))
	for i, k := range backendKeywords {
		names[i] = string(k.name)
	}
	return strings.Join(names, ", ")
}

func (b Backend) String() string { return string(b) }

// Errors when value is not empty, not a keyword, and contains no slash.
func (b *Backend) Set(value string) error {
	v := Backend(value)
	if v != "" && !v.isCommand() {
		if _, ok := v.keyword(); !ok {
			return v.unknown()
		}
	}
	*b = v
	return nil
}

// Checks whether b is a command line rather than a keyword. Any value that
// contains a slash counts as a command line.
func (b Backend) isCommand() bool { return strings.Contains(string(b), "/") }

func (b Backend) keyword() (backendKeyword, bool) {
	i := slices.IndexFunc(backendKeywords, func(k backendKeyword) bool { return k.name == b })
	if i < 0 {
		return backendKeyword{}, false
	}
	return backendKeywords[i], true
}

func (b Backend) unknown() error {
	return fmt.Errorf("unknown backend %q (use a path, or one of: %s)", b, BackendKeywords())
}

// Returns the command line that secrets-dispatcher-backend.service runs for b.
// Errors when the backend's program is not installed, or when b is neither a
// keyword nor a command line.
func (b Backend) exec() (string, error) {
	if k, ok := b.keyword(); ok {
		return k.exec()
	}
	if b.isCommand() {
		return string(b), nil
	}
	return "", b.unknown()
}

func gopassExec() (string, error) {
	// gopass-secret ignores DBUS_SESSION_BUS_ADDRESS and crash-loops without
	// --bus-address.
	path, err := lookPathFunc("gopass-secret")
	if err != nil {
		return "", fmt.Errorf("find gopass-secret: %w", err)
	}
	return path + " service --bus-address unix:path=" + config.SystemdRuntimeDir.BackendBusSocket(), nil
}

// Returns the path of gnome-keyring-daemon. Errors when it is not installed.
func gnomeKeyringDaemon() (string, error) {
	path, err := lookPathFunc("gnome-keyring-daemon")
	if err != nil {
		return "", fmt.Errorf("find gnome-keyring-daemon: %w", err)
	}
	return path, nil
}

func gnomeKeyringExec() (string, error) {
	path, err := gnomeKeyringDaemon()
	if err != nil {
		return "", err
	}
	return path + " --foreground --components=secrets" +
		// A private control directory keeps it apart from the session's own
		// gnome-keyring (ADR 0009).
		" --control-directory=" + config.SystemdRuntimeDir.KeyringControlDir(), nil
}
