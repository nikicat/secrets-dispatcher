// Package daemon implements the secrets-dispatcher companion daemon.
// The daemon registers on the system D-Bus as net.mowaka.SecretsDispatcher1
// and serves the RequestSecret and RequestSign methods that block until the
// companion user approves or denies via the VT-hosted bubbletea TUI.
package daemon

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/godbus/dbus/v5"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

// D-Bus interface constants for the companion daemon.
const (
	BusName    = "net.mowaka.SecretsDispatcher1"
	ObjectPath = dbus.ObjectPath("/net/mowaka/SecretsDispatcher1")
	Interface  = "net.mowaka.SecretsDispatcher1"
)

// senderResolver abstracts D-Bus sender info resolution for testability.
type senderResolver interface {
	Resolve(sender string) approval.SenderInfo
}

// gpgSigner abstracts GPG signing for testability.
type gpgSigner interface {
	// Sign invokes the real gpg binary with --status-fd=2 -bsau <keyID>,
	// feeding commitObject to stdin.
	Sign(commitObject []byte, keyID string) (signature, status []byte, exitCode int, err error)
}

// messageSender is a subset of *tea.Program used by Dispatcher.
// Defined as an interface for unit-test substitution — the real tea.Program
// satisfies it; tests provide a mock channel-based implementation.
type messageSender interface {
	Send(msg tea.Msg)
}

// Dispatcher is the D-Bus object exported under ObjectPath/Interface.
type Dispatcher struct {
	version  string
	mgr      *approval.Manager
	program  messageSender  // nil until TUI starts; methods return NotReady if nil
	resolver senderResolver
	signer   gpgSigner
}

// NewDispatcher creates a Dispatcher with the given dependencies.
// resolver and signer may be nil in headless/test mode (methods will return
// NotReady or skip signing respectively).
func NewDispatcher(version string, mgr *approval.Manager, resolver senderResolver) *Dispatcher {
	return &Dispatcher{
		version:  version,
		mgr:      mgr,
		resolver: resolver,
	}
}

// SetProgram injects the running bubbletea program. Must be called before the
// daemon accepts D-Bus calls (Run() does this after tea.NewProgram returns).
// *tea.Program satisfies the messageSender interface.
func (d *Dispatcher) SetProgram(p *tea.Program) {
	d.program = p
}

// SetSigner injects the GPG signer used by RequestSign on approval.
func (d *Dispatcher) SetSigner(s gpgSigner) {
	d.signer = s
}

// Ping is a health-check method. Returns "pong" to confirm the daemon is alive.
func (d *Dispatcher) Ping() (string, *dbus.Error) {
	return "pong", nil
}

// GetVersion returns the daemon version string.
func (d *Dispatcher) GetVersion() (string, *dbus.Error) {
	return d.version, nil
}
