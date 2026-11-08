package gpgsign

import (
	"context"
	"fmt"
	"os"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

// The process exit status of the thin client. It is gpg's own status when gpg
// ran (ERR-02).
type ExitStatus int

// Exit statuses the thin client sets itself.
const (
	exitOK          ExitStatus = 0
	exitRefused     ExitStatus = 1
	exitSystemError ExitStatus = 2
)

// How the daemon resolved one sign request, on either signing path. GPGResult
// is set when Result approves the request.
type SignOutcome struct {
	Result approval.Resolution `json:"result"`
	*approval.GPGResult
}

// Errors when the outcome approves the request without gpg's output.
func (o SignOutcome) check() error {
	if o.Result.Approves() && o.GPGResult == nil {
		return fmt.Errorf("%s without gpg output", o.Result)
	}
	return nil
}

// Hands the outcome back to git: gpg's output when the request was approved,
// and a message on stderr otherwise. Returns the process exit status.
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
	errorf("unexpected result %q from the daemon", o.Result)
	return exitSystemError
}

// Tells git on stderr why a sign request got no outcome. Returns exitRefused
// when ctx was cancelled, because the user interrupted the request, and
// exitSystemError otherwise.
func reportFailure(ctx context.Context, err error) ExitStatus {
	if ctx.Err() != nil {
		errorf("signing interrupted")
		return exitRefused
	}
	errorf("%v", err)
	return exitSystemError
}

// Writes gpg's status lines to stderr and, when gpg succeeded, the signature
// to stdout (SIGN-08). Returns gpg's own exit status (ERR-02).
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
