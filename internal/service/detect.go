package service

import (
	"fmt"
	"os"
	"path/filepath"
)

// ProviderKind identifies a known Secret Service implementation.
type ProviderKind string

const (
	ProviderGnomeKeyring ProviderKind = "gnome-keyring"
	ProviderGopass       ProviderKind = "gopass"
	ProviderKWallet      ProviderKind = "kwallet"
	ProviderDispatcher   ProviderKind = "secrets-dispatcher"
	ProviderNone         ProviderKind = "none"    // nothing owns org.freedesktop.secrets
	ProviderUnknown      ProviderKind = "unknown" // owned, but not by a known implementation
)

// Provider describes the current owner of org.freedesktop.secrets on the
// session bus, plus the desktop environment it runs in. It drives the
// install-time decision of which masking/topology to apply (US-5) and which
// private backend to default to (US-4).
type Provider struct {
	Kind    ProviderKind
	PID     int
	Exe     string // resolved /proc/<pid>/exe; empty if unresolvable
	Desktop string // $XDG_CURRENT_DESKTOP (e.g. "ubuntu:GNOME"), informational
}

// readlinkFunc is the function used to resolve /proc/<pid>/exe. Replaced in tests.
var readlinkFunc = os.Readlink

// exeToKind maps a Secret Service daemon binary name to its provider kind.
// Exact names, deliberately: matching on globs would misclassify wrappers.
var exeToKind = map[string]ProviderKind{
	"gnome-keyring-daemon":  ProviderGnomeKeyring,
	"gopass-secret-service": ProviderGopass,
	"kwalletd5":             ProviderKWallet,
	"kwalletd6":             ProviderKWallet,
	"secrets-dispatcher":    ProviderDispatcher,
}

// DetectProvider resolves who currently owns org.freedesktop.secrets on the
// session bus (owner PID -> /proc/<pid>/exe) and the current desktop.
func DetectProvider() Provider {
	p := Provider{Kind: ProviderNone, Desktop: os.Getenv("XDG_CURRENT_DESKTOP")}

	// Same mechanism as stopDBusActivatedService: ask the bus daemon for the
	// owner's PID. Fails when the name is unowned.
	out, err := execOutputFunc("busctl", "--user", "call",
		"org.freedesktop.DBus", "/org/freedesktop/DBus",
		"org.freedesktop.DBus", "GetConnectionUnixProcessID",
		"s", "org.freedesktop.secrets")
	if err != nil {
		return p
	}
	var pid int
	if _, err := fmt.Sscanf(string(out), "u %d", &pid); err != nil || pid <= 0 {
		return p
	}
	p.PID = pid
	p.Kind = ProviderUnknown

	exe, err := readlinkFunc(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return p
	}
	p.Exe = exe

	if kind, ok := exeToKind[filepath.Base(exe)]; ok {
		p.Kind = kind
	}
	return p
}

// String renders the provider for user-facing output.
func (p Provider) String() string {
	switch p.Kind {
	case ProviderNone:
		return "none (org.freedesktop.secrets is unowned)"
	case ProviderUnknown:
		if p.Exe != "" {
			return fmt.Sprintf("unknown (%s, pid %d)", p.Exe, p.PID)
		}
		return fmt.Sprintf("unknown (pid %d)", p.PID)
	default:
		return fmt.Sprintf("%s (pid %d)", p.Kind, p.PID)
	}
}
