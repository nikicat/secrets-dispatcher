package service

import (
	"fmt"
	"slices"
	"strings"

	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/nikicat/secrets-dispatcher/internal/systemd"
)

// The private Secret Service that a local or full install runs behind the
// dispatcher (ADR 0001): one of BackendKeywords or a command line.
type Backend struct{ backendKind }

// The sealed variants of Backend: *backendKeyword and backendCommand.
type backendKind interface {
	// Returns the command that secrets-dispatcher-backend.service runs.
	// Errors when a keyword's program is not installed.
	exec() (systemd.Command, error)
	// Returns the variables that secrets-dispatcher-backend.service sets, as
	// NAME=value (docs/ARCHITECTURE.md, Service setup).
	environment() []string
	String() string
}

// A backend named by a keyword, whose program is looked up at install time.
type backendKeyword struct {
	name    string
	program func() (systemd.Command, error)
	env     []string
}

func (k *backendKeyword) exec() (systemd.Command, error) { return k.program() }
func (k *backendKeyword) environment() []string          { return k.env }
func (k *backendKeyword) String() string                 { return k.name }

// A backend given as a command line, run verbatim.
type backendCommand string

func (c backendCommand) exec() (systemd.Command, error) { return systemd.RawCommand(string(c)), nil }

// A command line's program is not known to take the private bus as a flag, so
// it gets the bus in its environment (docs/ARCHITECTURE.md, Service setup).
func (c backendCommand) environment() []string { return backendBusEnv }
func (c backendCommand) String() string        { return string(c) }

// The private bus that the backend serves on, with %t for systemd to expand.
var backendBus = config.SystemdRuntimeDir.BackendBusSocket()

// Hands a backend the private bus through its environment.
var backendBusEnv = []string{"DBUS_SESSION_BUS_ADDRESS=" + backendBus.Address()}

var (
	BackendGopass       = Backend{&backendKeyword{name: "gopass", program: gopassExec}}
	BackendGnomeKeyring = Backend{&backendKeyword{name: "gnome-keyring", program: gnomeKeyringExec, env: backendBusEnv}}
)

var backendKeywords = []Backend{BackendGopass, BackendGnomeKeyring}

// Returns the keywords that Backend.UnmarshalText accepts, as
// config.Alternatives lists them.
func BackendKeywords() string {
	names := make([]string, len(backendKeywords))
	for i, k := range backendKeywords {
		names[i] = k.String()
	}
	return config.Alternatives(names)
}

// Parses a backend keyword, or a command line when text contains a slash.
// Errors when text is neither.
func (b *Backend) UnmarshalText(text []byte) error {
	s := string(text)
	if strings.Contains(s, "/") {
		*b = Backend{backendCommand(s)}
		return nil
	}
	i := slices.IndexFunc(backendKeywords, func(k Backend) bool { return k.String() == s })
	if i < 0 {
		return fmt.Errorf("backend must be %s, or a command line that contains a slash, got %q", BackendKeywords(), s)
	}
	*b = backendKeywords[i]
	return nil
}

func gopassExec() (systemd.Command, error) {
	// gopass-secret ignores DBUS_SESSION_BUS_ADDRESS and crash-loops without
	// --bus-address.
	path, err := lookPathFunc("gopass-secret")
	if err != nil {
		return nil, fmt.Errorf("find gopass-secret: %w", err)
	}
	return systemd.NewCommand(path, "service", "--bus-address", backendBus.Address()), nil
}

// Returns the path of gnome-keyring-daemon. Errors when it is not installed.
func gnomeKeyringDaemon() (string, error) {
	path, err := lookPathFunc("gnome-keyring-daemon")
	if err != nil {
		return "", fmt.Errorf("find gnome-keyring-daemon: %w", err)
	}
	return path, nil
}

func gnomeKeyringExec() (systemd.Command, error) {
	path, err := gnomeKeyringDaemon()
	if err != nil {
		return nil, err
	}
	return systemd.NewCommand(path, "--foreground", "--components=secrets",
		// A private control directory keeps it apart from the session's own
		// gnome-keyring (ADR 0009).
		"--control-directory="+config.SystemdRuntimeDir.KeyringControlDir().String()), nil
}
