package approval

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// RequestTypeGPGSign is the request type for GPG commit signing approval requests.
const RequestTypeGPGSign RequestType = "gpg_sign"

// GPGSignInfo carries the commit context fields for a signing approval request.
// All fields are supplied by the thin client; CommitObject is the raw commit object
// bytes (UTF-8 text) that the daemon feeds to real gpg's stdin on approval.
type GPGSignInfo struct {
	RepoName     string   `json:"repo_name"`
	CommitMsg    string   `json:"commit_msg"`
	Author       string   `json:"author"`
	Committer    string   `json:"committer"`
	KeyID        string   `json:"key_id"`
	Fingerprint  string   `json:"fingerprint,omitempty"`
	ChangedFiles []string `json:"changed_files"`
	ParentHash   string   `json:"parent_hash,omitempty"`
	// CommitObject is the raw commit object bytes (UTF-8 text) fed to gpg's stdin.
	CommitObject string `json:"commit_object,omitempty"`
}

// RecordAutoApprovedGPGSign creates a resolved gpg_sign request for history and
// WebSocket delivery WITHOUT firing EventRequestCreated (so no desktop notification
// appears). The request is never added to pending and no timeout goroutine is started.
// sig and status are the gpg output to deliver to the thin client via WebSocket.
func (m *Manager) RecordAutoApprovedGPGSign(client string, info *GPGSignInfo, senderInfo SenderInfo, sig, status []byte) (string, error) {
	if info == nil {
		return "", errors.New("gpg sign info is required")
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
		Signature:   sig,
		GPGStatus:   status,
		done:        make(chan struct{}),
	}
	close(req.done)
	m.notify(Event{Type: EventRequestApproved, Request: req})
	return req.ID, nil
}

// CreateGPGSignRequest creates a pending gpg_sign approval request and returns its ID.
// It does NOT block — the result is delivered to the caller via the WebSocket observer
// pipeline (EventRequestApproved / EventRequestDenied / EventRequestExpired).
// Uses context.Background() for the timeout goroutine so a dropped HTTP connection does
// not cancel a request the user is actively reviewing in the web UI.
func (m *Manager) CreateGPGSignRequest(client string, info *GPGSignInfo, senderInfo SenderInfo) (string, error) {
	if info == nil {
		return "", errors.New("gpg sign info is required")
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
			delete(m.pending, req.ID)
			m.mu.Unlock()
			m.notify(Event{Type: EventRequestExpired, Request: req})
		}
	}()
	return req.ID, nil
}
