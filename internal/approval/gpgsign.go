package approval

import (
	"context"
	"errors"
	"strings"
	"time"
)

// RequestTypeGPGSign is the request type for GPG commit signing approval requests.
const RequestTypeGPGSign RequestType = "gpg_sign"

// The kind of git object that a gpg_sign request signs. git hands commits,
// annotated tags and push certificates to the same gpg.program, so the signed
// bytes are not always a commit (ADR 0008).
type GPGSignKind string

const (
	GPGSignCommit  GPGSignKind = "commit"
	GPGSignTag     GPGSignKind = "tag"
	GPGSignPush    GPGSignKind = "push"
	GPGSignUnknown GPGSignKind = "unknown"
)

// RepoName and ChangedFiles describe the working tree, not the signed object
// (ADR 0008).
type GPGSignInfo struct {
	RepoName RepoName    `json:"repo_name"`
	Kind     GPGSignKind `json:"kind,omitempty"`
	// A commit's or a tag's message, or the ref updates of a push certificate.
	CommitMsg string `json:"commit_msg"`
	// A commit's author, a tag's tagger, or a push certificate's pusher.
	Author string `json:"author"`
	// Commit only.
	Committer    string   `json:"committer"`
	KeyID        string   `json:"key_id"`
	Fingerprint  string   `json:"fingerprint,omitempty"`
	ChangedFiles []string `json:"changed_files"`
	// The first parent. Commit only.
	ParentHash string `json:"parent_hash,omitempty"`
	// Tag only.
	TagName string `json:"tag_name,omitempty"`
	// The hash of the object that the tag points at. Tag only.
	Target string `json:"target,omitempty"`
	// The URL that a push certificate pushes to. Push only.
	Pushee string `json:"pushee,omitempty"`
	// The bytes that gpg signs, as UTF-8 text.
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

// Errors with ErrNoGPGSignInfo when i is nil, and with ErrNoCommitObject when
// it has no CommitObject.
func (i *GPGSignInfo) RequireSignedBytes() error {
	if i == nil {
		return ErrNoGPGSignInfo
	}
	if i.CommitObject == "" {
		return ErrNoCommitObject
	}
	return nil
}

// Returns the gpg_sign RequestInfo of sender's request to sign info. Its one
// item is labelled with the repository name and the commit's subject.
func (info *GPGSignInfo) RequestInfo(sender SenderInfo) RequestInfo {
	label := string(info.RepoName)
	if info.CommitMsg != "" {
		subject := info.CommitMsg
		if i := len(subject); i > 60 {
			subject = subject[:60]
		}
		if nl := strings.IndexByte(subject, '\n'); nl >= 0 {
			subject = subject[:nl]
		}
		label += ": " + subject
	}
	return RequestInfo{
		Type:        RequestTypeGPGSign,
		Items:       []ItemInfo{{Label: label}},
		SenderInfo:  sender,
		GPGSignInfo: info,
	}
}

// Records a gpg_sign request that was approved without a prompt, with res as
// its result, and returns its ID. Errors as GPGSignInfo.RequireSignedBytes does.
func (m *Manager) RecordAutoApprovedGPGSign(info RequestInfo, res GPGResult) (string, error) {
	if err := info.GPGSignInfo.RequireSignedBytes(); err != nil {
		return "", err
	}

	req := info.newRequest(0)
	req.GPGResult = res
	req.done = make(chan struct{})
	req.resolution = ResolutionAutoApproved
	close(req.done)
	m.notify(Event{Type: EventRequestAutoApproved, Request: req})
	return req.ID, nil
}

// Creates a pending gpg_sign request that expires after the manager's timeout
// (ADR 0004), and returns its ID without waiting, unlike AwaitGPGSign. Errors
// as GPGSignInfo.RequireSignedBytes does.
func (m *Manager) CreateGPGSignRequest(info RequestInfo) (string, error) {
	req, err := m.createGPGSignRequest(info)
	if err != nil {
		return "", err
	}
	return req.ID, nil
}

// Creates a gpg_sign request, waits until it is resolved, and returns it with
// its resolution (ADR 0011). Ending ctx cancels the request. Errors as
// GPGSignInfo.RequireSignedBytes does.
func (m *Manager) AwaitGPGSign(ctx context.Context, info RequestInfo) (*Request, Resolution, error) {
	req, err := m.createGPGSignRequest(info)
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

func (m *Manager) createGPGSignRequest(info RequestInfo) (*Request, error) {
	if err := info.GPGSignInfo.RequireSignedBytes(); err != nil {
		return nil, err
	}

	req := info.newRequest(m.timeout)
	req.done = make(chan struct{})
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
