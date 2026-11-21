package approval

import (
	"context"
	"errors"
	"strings"
)

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

// The exit code of gpg for one sign request. It is the gpg process's own code,
// or one of the codes below when gpg did not run.
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
func (e HistoryEntry) GPGOutput() *GPGResult {
	if e.Request.Type != RequestTypeGPGSign || !e.Resolution.Approves() {
		return nil
	}
	return &e.Request.GPGResult
}

// Errors for a sign request with no bytes to sign.
var (
	ErrNoGPGSignInfo  = errors.New("gpg sign info is required")
	ErrNoCommitObject = errors.New("gpg sign info has no commit object")
)

// Errors with ErrNoGPGSignInfo when the info is nil, and with ErrNoCommitObject
// when it has no CommitObject.
func (i *GPGSignInfo) RequireSignedBytes() error {
	if i == nil {
		return ErrNoGPGSignInfo
	}
	if i.CommitObject == "" {
		return ErrNoCommitObject
	}
	return nil
}

// Returns the gpg_sign request that the given sender makes to sign this object.
// The request's one item is labelled with the repository name and the first
// line of the object's message.
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

// Records the gpg_sign request as approved without a prompt, and stores the
// given gpg result on it. Returns the request's ID. Errors as
// GPGSignInfo.RequireSignedBytes does.
func (m *Manager) RecordAutoApprovedGPGSign(info RequestInfo, res GPGResult) (string, error) {
	if err := info.GPGSignInfo.RequireSignedBytes(); err != nil {
		return "", err
	}

	req := info.newRecord()
	req.GPGResult = res
	m.notify(endEvent(ResolutionAutoApproved, req))
	return req.ID, nil
}

// Creates a pending gpg_sign request that expires after the manager's timeout
// (ADR 0004), and returns its ID without waiting, unlike AwaitGPGSign. Errors
// as GPGSignInfo.RequireSignedBytes does.
func (m *Manager) CreateGPGSignRequest(info RequestInfo) (string, error) {
	if err := info.GPGSignInfo.RequireSignedBytes(); err != nil {
		return "", err
	}
	return m.addPending(info).req.ID, nil
}

// Creates a gpg_sign request, waits until it is resolved, and returns the
// entry that History records for it (ADR 0011). Ending the context cancels
// the request. Errors as GPGSignInfo.RequireSignedBytes does.
func (m *Manager) AwaitGPGSign(ctx context.Context, info RequestInfo) (HistoryEntry, error) {
	if err := info.GPGSignInfo.RequireSignedBytes(); err != nil {
		return HistoryEntry{}, err
	}
	return m.await(ctx, info), nil
}
