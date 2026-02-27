package daemon

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"

	"github.com/godbus/dbus/v5"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/gpgsign"
	"github.com/nikicat/secrets-dispatcher/internal/procchain"
	"github.com/nikicat/secrets-dispatcher/internal/tui"
)

// defaultGPGSigner implements gpgSigner using the real gpg binary from PATH.
type defaultGPGSigner struct{}

// Sign locates the real gpg binary (skipping self) and invokes it with
// --status-fd=2 -bsau <keyID>, feeding commitObject to stdin.
func (s *defaultGPGSigner) Sign(commitObject []byte, keyID string) (signature, status []byte, exitCode int, err error) {
	gpgPath, err := gpgsign.FindRealGPG()
	if err != nil {
		return nil, nil, 1, fmt.Errorf("find gpg: %w", err)
	}
	cmd := exec.Command(gpgPath, "--status-fd=2", "-bsau", keyID)
	cmd.Stdin = bytes.NewReader(commitObject)
	var sigBuf, statusBuf bytes.Buffer
	cmd.Stdout = &sigBuf
	cmd.Stderr = &statusBuf
	if runErr := cmd.Run(); runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return sigBuf.Bytes(), statusBuf.Bytes(), exitErr.ExitCode(), nil
		}
		return nil, statusBuf.Bytes(), 1, fmt.Errorf("gpg exec: %w", runErr)
	}
	return sigBuf.Bytes(), statusBuf.Bytes(), 0, nil
}

// makeDBusError creates a typed D-Bus error with a single string body.
func makeDBusError(name, msg string) *dbus.Error {
	return &dbus.Error{Name: name, Body: []interface{}{msg}}
}

// RequestSecret is the D-Bus method that blocks until the companion user
// approves or denies the secret access request on the VT.
//
// On success it returns the secret path prefixed with "approved:" (Phase 5
// placeholder — Phase 6 will fetch the real secret from gopass).
// On denial it returns net.mowaka.Error.Denied.
// On timeout it returns net.mowaka.Error.Timeout.
//
// godbus dispatches each method call in its own goroutine, so blocking here
// is safe and does not stall other D-Bus traffic.
func (d *Dispatcher) RequestSecret(sender dbus.Sender, path string) (string, *dbus.Error) {
	if d.program == nil {
		return "", makeDBusError("net.mowaka.Error.NotReady", "daemon TUI not initialized")
	}

	// Resolve the caller's process information.
	senderInfo := d.resolveOrEmpty(string(sender))

	// Walk the process chain for display in the TUI detail pane.
	chain := procchain.Walk(senderInfo.PID, 5)

	// Create a non-blocking request in the manager (starts timeout goroutine).
	id, err := d.mgr.CreateSecretRequest("dbus", path, senderInfo)
	if err != nil {
		slog.Error("RequestSecret: create request", "error", err)
		return "", makeDBusError("net.mowaka.Error.Internal", err.Error())
	}

	// Retrieve the request pointer so we can send it to the TUI.
	req := d.mgr.GetPending(id)
	if req != nil {
		d.program.Send(tui.NewRequestMsg{Request: req, ProcChain: chain})
	}

	// Block until the user approves/denies or the timeout fires.
	approved, err := d.mgr.WaitForResult(id)
	if err != nil {
		if errors.Is(err, approval.ErrTimeout) {
			return "", makeDBusError("net.mowaka.Error.Timeout",
				fmt.Sprintf("request timed out — approve on VT (Ctrl+Alt+F8)"))
		}
		if errors.Is(err, approval.ErrNotFound) {
			// Request expired before WaitForResult was called (race: very rare).
			return "", makeDBusError("net.mowaka.Error.Timeout",
				"request expired — approve on VT (Ctrl+Alt+F8)")
		}
		return "", makeDBusError("net.mowaka.Error.Internal", err.Error())
	}
	if !approved {
		return "", makeDBusError("net.mowaka.Error.Denied", "request denied by user")
	}

	// Phase 5 placeholder: return confirmed path.
	// Phase 6 will fetch the actual secret from gopass here.
	return "approved:" + path, nil
}

// RequestSign is the D-Bus method for GPG commit signing. It blocks until the
// companion user approves or denies on the VT, then invokes the real gpg
// binary and returns the signature and status bytes.
//
// Returns (signature, gpgStatus, error).
// On denial it returns net.mowaka.Error.Denied.
// On timeout it returns net.mowaka.Error.Timeout.
func (d *Dispatcher) RequestSign(
	sender dbus.Sender,
	repoName, commitMsg, author, committer, keyID string,
	changedFiles []string,
	commitObject string,
) ([]byte, []byte, *dbus.Error) {
	if d.program == nil {
		return nil, nil, makeDBusError("net.mowaka.Error.NotReady", "daemon TUI not initialized")
	}

	// Resolve the caller's process information.
	senderInfo := d.resolveOrEmpty(string(sender))

	// Walk the process chain for display in the TUI detail pane.
	chain := procchain.Walk(senderInfo.PID, 5)

	// Build GPGSignInfo from the method parameters.
	info := &approval.GPGSignInfo{
		RepoName:     repoName,
		CommitMsg:    commitMsg,
		Author:       author,
		Committer:    committer,
		KeyID:        keyID,
		ChangedFiles: changedFiles,
		CommitObject: commitObject,
	}

	// Create a non-blocking GPG sign request.
	id, err := d.mgr.CreateGPGSignRequest("dbus", info, senderInfo)
	if err != nil {
		slog.Error("RequestSign: create request", "error", err)
		return nil, nil, makeDBusError("net.mowaka.Error.Internal", err.Error())
	}

	// Retrieve the request pointer and notify TUI.
	req := d.mgr.GetPending(id)
	if req != nil {
		d.program.Send(tui.NewRequestMsg{Request: req, ProcChain: chain})
	}

	// Block until user decides or timeout fires.
	approved, err := d.mgr.WaitForResult(id)
	if err != nil {
		if errors.Is(err, approval.ErrTimeout) {
			return nil, nil, makeDBusError("net.mowaka.Error.Timeout",
				"request timed out — approve on VT (Ctrl+Alt+F8)")
		}
		if errors.Is(err, approval.ErrNotFound) {
			return nil, nil, makeDBusError("net.mowaka.Error.Timeout",
				"request expired — approve on VT (Ctrl+Alt+F8)")
		}
		return nil, nil, makeDBusError("net.mowaka.Error.Internal", err.Error())
	}
	if !approved {
		return nil, nil, makeDBusError("net.mowaka.Error.Denied", "request denied by user")
	}

	// Run real GPG signing if a signer is configured.
	if d.signer == nil {
		// No signer configured (headless/test mode) — return placeholder.
		return []byte("signature-placeholder"), []byte("[GNUPG:] SIG_CREATED"), nil
	}

	sig, status, exitCode, err := d.signer.Sign([]byte(commitObject), keyID)
	if err != nil {
		return nil, nil, makeDBusError("net.mowaka.Error.Internal",
			fmt.Sprintf("gpg exec failed: %v", err))
	}
	if exitCode != 0 {
		// gpg itself failed — return the status bytes so the caller can diagnose.
		// Return a GPG-specific error name so the caller distinguishes this from denial.
		return nil, status, makeDBusError("net.mowaka.Error.GPGFailed",
			fmt.Sprintf("gpg exited with code %d", exitCode))
	}

	return sig, status, nil
}

// resolveOrEmpty returns sender info for the given D-Bus sender name.
// Falls back to an empty SenderInfo if the resolver is nil (headless/test mode).
func (d *Dispatcher) resolveOrEmpty(sender string) approval.SenderInfo {
	if d.resolver == nil {
		return approval.SenderInfo{Sender: sender}
	}
	return d.resolver.Resolve(sender)
}
