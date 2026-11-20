package service

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/nikicat/secrets-dispatcher/internal/procutil"
)

// A known Secret Service implementation.
type ProviderKind string

const (
	ProviderGnomeKeyring ProviderKind = "gnome-keyring"
	ProviderGopass       ProviderKind = "gopass"
	ProviderKWallet      ProviderKind = "kwallet"
	ProviderDispatcher   ProviderKind = "secrets-dispatcher"
	ProviderUnknown      ProviderKind = "unknown" // owned, but not by a known implementation
)

// Who owns org.freedesktop.secrets on the session bus. It drives the
// install-time decision of which masking/topology to apply and which private
// backend to default to (ADR 0009).
type Provider struct {
	// The owning process; nil when nothing owns the name.
	Owner *Owner
}

// The process that owns org.freedesktop.secrets.
type Owner struct {
	PID procutil.PID
	// The resolved /proc/<pid>/exe; nil when it cannot be resolved.
	Exe *procutil.Path
}

// Returns the executable of a process. Replaced in tests.
var exeOf = procutil.PID.Exe

// exeToKind maps a Secret Service daemon binary name to its provider kind.
// Exact names, deliberately: matching on globs would misclassify wrappers.
var exeToKind = map[string]ProviderKind{
	"gnome-keyring-daemon": ProviderGnomeKeyring,
	"gopass-secret":        ProviderGopass, // runs `gopass-secret service`
	"kwalletd5":            ProviderKWallet,
	"kwalletd6":            ProviderKWallet,
	"secrets-dispatcher":   ProviderDispatcher,
}

// Resolves who currently owns org.freedesktop.secrets on the session bus
// (owner PID -> /proc/<pid>/exe).
func DetectProvider(ctx context.Context) Provider {
	pid, err := session.secretsOwner(ctx)
	if err != nil {
		return Provider{}
	}
	o := &Owner{PID: pid}
	if exe, ok := exeOf(pid); ok {
		o.Exe = &exe
	}
	return Provider{Owner: o}
}

// Reports whether a provider of kind owns the name.
func (p Provider) is(kind ProviderKind) bool { return p.Owner != nil && p.Owner.Kind() == kind }

// Returns the owner of the name when it is not the dispatcher, nil otherwise.
func (p Provider) displaced() *Owner {
	if p.Owner == nil || p.Owner.Kind() == ProviderDispatcher {
		return nil
	}
	return p.Owner
}

func (p Provider) String() string {
	if p.Owner == nil {
		return "none (org.freedesktop.secrets is unowned)"
	}
	return p.Owner.String()
}

// Returns the implementation that o's executable names. Returns
// ProviderUnknown when the executable names no known implementation or was not
// resolved.
func (o Owner) Kind() ProviderKind {
	if o.Exe != nil {
		if kind, ok := exeToKind[filepath.Base(string(*o.Exe))]; ok {
			return kind
		}
	}
	return ProviderUnknown
}

func (o Owner) String() string {
	if o.Kind() == ProviderUnknown && o.Exe != nil {
		return fmt.Sprintf("unknown (%s, pid %d)", *o.Exe, o.PID)
	}
	return fmt.Sprintf("%s (pid %d)", o.Kind(), o.PID)
}

// Returns the change that stops o, as the dry run lists it.
func (o Owner) sigtermChange() Change {
	return Change{"signal", fmt.Sprintf("SIGTERM -> pid %d (%s)", o.PID, o.Kind()), "current owner of org.freedesktop.secrets"}
}
