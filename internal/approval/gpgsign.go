package approval

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// RequestTypeGPGSign is the request type for GPG commit signing approval requests.
const RequestTypeGPGSign RequestType = "gpg_sign"

// GPGSignInfo carries the signing context for a gpg_sign approval request.
// All fields are supplied by the thin client; CommitObject is the raw object
// bytes (UTF-8 text) that the daemon feeds to real gpg's stdin on approval.
//
// git signs three kinds of object through the same invocation (see
// gpgsign.PayloadKind): commits, annotated tags, and signed-push certificates.
// The common fields are reused across kinds — Author holds the commit author /
// tag tagger / push pusher, and CommitMsg holds the commit message / tag
// message / pushed ref-update lines — while Kind and the kind-specific fields
// below let the approval UI label them correctly. Committer and ParentHash are
// commit-only; TagName/Target are tag-only; Pushee is push-only.
type GPGSignInfo struct {
	RepoName     string   `json:"repo_name"`
	Kind         string   `json:"kind,omitempty"`
	CommitMsg    string   `json:"commit_msg"`
	Author       string   `json:"author"`
	Committer    string   `json:"committer"`
	KeyID        string   `json:"key_id"`
	Fingerprint  string   `json:"fingerprint,omitempty"`
	ChangedFiles []string `json:"changed_files"`
	ParentHash   string   `json:"parent_hash,omitempty"`
	TagName      string   `json:"tag_name,omitempty"`
	Target       string   `json:"target,omitempty"`
	Pushee       string   `json:"pushee,omitempty"`
	// CommitObject is the raw signed object bytes (UTF-8 text) fed to gpg's stdin.
	CommitObject string `json:"commit_object,omitempty"`
}

// What real gpg produced for one approved gpg_sign request.
type GPGResult struct {
	Signature []byte `json:"signature,omitempty"`
	Status    []byte `json:"gpg_status,omitempty"`
	// Sent even when zero, so that a result with no signature and no status
	// still decodes as a result.
	ExitCode GPGExitCode `json:"exit_code"`
}

// Checks whether gpg ran and produced a signature.
func (r GPGResult) Succeeded() bool { return r.ExitCode == 0 }

// The exit code of a gpg sign request: the real gpg process's own, or one of
// the codes below when gpg did not run. Zero means gpg succeeded.
type GPGExitCode int

// Exit codes the daemon reports when it could not run gpg.
const (
	// gpg was found but did not start.
	GPGNotStarted GPGExitCode = 1
	// No gpg binary was found.
	GPGNotFound GPGExitCode = 2
)

// Returns what gpg produced for an approved or auto-approved gpg_sign request,
// and nil for any other request or resolution.
func (r *Request) GPGOutput() *GPGResult {
	if r.Type != RequestTypeGPGSign || !r.resolution.Approves() {
		return nil
	}
	return &r.GPGResult
}

// Errors for a sign request with no bytes to sign.
var (
	ErrNoGPGSignInfo  = errors.New("gpg sign info is required")
	ErrNoCommitObject = errors.New("gpg sign info has no commit object")
)

// Errors when there is nothing to sign and nothing to bind the approval display to.
func (i *GPGSignInfo) RequireSignedBytes() error {
	if i == nil {
		return ErrNoGPGSignInfo
	}
	if i.CommitObject == "" {
		return ErrNoCommitObject
	}
	return nil
}

// RecordAutoApprovedGPGSign creates a resolved gpg_sign request for history and
// WebSocket delivery WITHOUT firing EventRequestCreated (so no desktop notification
// appears). Fires EventRequestAutoApproved so the history entry shows "auto_approved".
// The request is never added to pending and no timeout goroutine is started.
// res is the gpg output to deliver to the thin client via WebSocket.
func (m *Manager) RecordAutoApprovedGPGSign(client string, info *GPGSignInfo, senderInfo SenderInfo, res GPGResult) (string, error) {
	if err := info.RequireSignedBytes(); err != nil {
		return "", err
	}

	now := time.Now()
	req := &Request{
		ID:          uuid.New().String(),
		Client:      client,
		CreatedAt:   now,
		ExpiresAt:   now,
		Type:        RequestTypeGPGSign,
		GPGSignInfo: info,
		SenderInfo:  senderInfo,
		GPGResult:   res,
		done:        make(chan struct{}),
		resolution:  ResolutionAutoApproved,
	}
	close(req.done)
	m.notify(Event{Type: EventRequestAutoApproved, Request: req})
	return req.ID, nil
}

// CreateGPGSignRequest creates a pending gpg_sign approval request and returns its ID.
// It does NOT block — the result is delivered to the caller via the WebSocket observer
// pipeline (EventRequestApproved / EventRequestDenied / EventRequestExpired).
// Uses context.Background() for the timeout goroutine so a dropped HTTP connection does
// not cancel a request the user is actively reviewing in the web UI.
func (m *Manager) CreateGPGSignRequest(client string, info *GPGSignInfo, senderInfo SenderInfo) (string, error) {
	req, err := m.createGPGSignRequest(client, info, senderInfo)
	if err != nil {
		return "", err
	}
	return req.ID, nil
}

// Creates a gpg_sign request and returns it with its resolution once it is
// resolved. Ending ctx cancels the request.
func (m *Manager) AwaitGPGSign(ctx context.Context, client string, info *GPGSignInfo, senderInfo SenderInfo) (*Request, Resolution, error) {
	req, err := m.createGPGSignRequest(client, info, senderInfo)
	if err != nil {
		return nil, "", err
	}
	select {
	case <-req.done:
	case <-ctx.Done():
		m.Cancel(req.ID) //nolint:errcheck // resolved meanwhile is fine
		<-req.done
	}
	return req, req.resolution, nil
}

func (m *Manager) createGPGSignRequest(client string, info *GPGSignInfo, senderInfo SenderInfo) (*Request, error) {
	if err := info.RequireSignedBytes(); err != nil {
		return nil, err
	}

	now := time.Now()
	req := &Request{
		ID:          uuid.New().String(),
		Client:      client,
		CreatedAt:   now,
		ExpiresAt:   now.Add(m.timeout),
		Type:        RequestTypeGPGSign,
		GPGSignInfo: info,
		SenderInfo:  senderInfo,
		done:        make(chan struct{}),
	}
	m.mu.Lock()
	m.pending[req.ID] = req
	m.mu.Unlock()
	m.notify(Event{Type: EventRequestCreated, Request: req})

	// Timeout goroutine — mirrors the timer.C branch in RequireApproval.
	// ERR-03: gpg_sign requests expire via the existing timeout mechanism.
	go func() {
		select {
		case <-req.done:
			// resolved by Approve or Deny — no action needed
		case <-time.After(m.timeout):
			m.mu.Lock()
			if _, ok := m.pending[req.ID]; !ok {
				m.mu.Unlock()
				return // resolved as the timer fired
			}
			delete(m.pending, req.ID)
			req.resolution = ResolutionExpired
			close(req.done)
			m.mu.Unlock()
			m.notify(Event{Type: EventRequestExpired, Request: req})
		}
	}()
	return req, nil
}
