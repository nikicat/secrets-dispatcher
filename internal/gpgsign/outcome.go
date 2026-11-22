package gpgsign

import (
	"context"
	"fmt"
	"os"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

// The process exit status of the thin client. It is gpg's own status when gpg
// ran, since git reads it as gpg's (ADR 0008).
type ExitStatus int

// Exit statuses the thin client sets itself.
const (
	exitOK          ExitStatus = 0
	exitRefused     ExitStatus = 1
	exitSystemError ExitStatus = 2
)

// How the daemon resolved one request. GPGResult is set as
// approval.HistoryEntry.GPGOutput says.
type SignOutcome struct {
	Result approval.Resolution `json:"result,omitempty"`
	*approval.GPGResult
}

// Returns the outcome of a settled sign request, as History records it.
func OutcomeOf(entry approval.HistoryEntry) SignOutcome {
	return SignOutcome{Result: entry.Resolution, GPGResult: entry.GPGOutput()}
}

// Errors when the outcome approves the request without gpg's output.
func (o SignOutcome) check() error {
	if o.Result.Approves() && o.GPGResult == nil {
		return fmt.Errorf("%s without gpg output", o.Result)
	}
	return nil
}

// Writes gpg's output for git when the request was approved, and a message on
// stderr otherwise. Returns the process exit status.
func (o SignOutcome) report() ExitStatus {
	if o.Result.Approves() {
		return o.emit()
	}
	switch o.Result {
	case approval.ResolutionDenied:
		errorf("signing request denied by user")
		return exitRefused
	case approval.ResolutionCancelled:
		errorf("signing request cancelled")
		return exitRefused
	case approval.ResolutionExpired:
		errorf("signing request timed out")
		return exitRefused
	}
	return systemError(fmt.Errorf("unexpected result %q from the daemon", o.Result))
}

// Tells git on stderr why a sign request got no outcome. Returns exitRefused
// when ctx was cancelled, because the user interrupted the request, and
// exitSystemError otherwise.
func reportFailure(ctx context.Context, err error) ExitStatus {
	if ctx.Err() != nil {
		errorf("signing interrupted")
		return exitRefused
	}
	return systemError(err)
}

// Writes gpg's status lines to stderr and, when gpg succeeded, the signature
// to stdout. Returns gpg's own exit status.
func (o SignOutcome) emit() ExitStatus {
	if len(o.Status) > 0 {
		os.Stderr.Write(o.Status) //nolint:errcheck
	}
	if !o.Succeeded() {
		return ExitStatus(o.ExitCode)
	}
	os.Stdout.Write(o.Signature) //nolint:errcheck
	return exitOK
}
