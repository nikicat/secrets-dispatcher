package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os/exec"
	"strings"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/gpgsign"
)

// GPGRunner finds and executes the real gpg binary.
type GPGRunner interface {
	FindGPG() (string, error)
	RunGPG(gpgPath, keyID string, commitObject []byte) (signature, status []byte, exitCode int, err error)
}

// defaultGPGRunner implements GPGRunner using the real gpg binary from PATH.
type defaultGPGRunner struct{}

// FindGPG delegates to gpgsign.FindRealGPG to locate the real gpg binary,
// skipping self (prevents the thin client calling itself when installed as gpg.program).
func (d *defaultGPGRunner) FindGPG() (string, error) {
	return gpgsign.FindRealGPG()
}

// RunGPG invokes the real gpg binary with --status-fd=2 -bsau <keyID>,
// feeding commitObject to stdin. Signature bytes come from stdout; status
// lines from stderr (separate buffers — mixing them corrupts the signature).
func (d *defaultGPGRunner) RunGPG(gpgPath, keyID string, commitObject []byte) ([]byte, []byte, int, error) {
	cmd := exec.Command(gpgPath, "--status-fd=2", "-bsau", keyID)
	cmd.Stdin = bytes.NewReader(commitObject)
	var sigBuf, statusBuf bytes.Buffer
	cmd.Stdout = &sigBuf
	cmd.Stderr = &statusBuf
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, statusBuf.Bytes(), 1, fmt.Errorf("gpg exec failed: %w", err)
		}
	}
	return sigBuf.Bytes(), statusBuf.Bytes(), exitCode, nil
}

// GPGSignRequest is the POST body for /api/v1/gpg-sign/request.
type GPGSignRequest struct {
	Client      string                `json:"client"`
	GPGSignInfo *approval.GPGSignInfo `json:"gpg_sign_info"`
}

// GPGSignResponse is the response body for a successful POST to /api/v1/gpg-sign/request.
type GPGSignResponse struct {
	RequestID string `json:"request_id"`
}

// HandleGPGSignRequest handles POST /api/v1/gpg-sign/request.
// It creates a non-blocking gpg_sign approval request and returns the request ID.
// The result (signature or denial) is delivered via the WebSocket request_resolved event.
func (h *Handlers) HandleGPGSignRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req GPGSignRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.GPGSignInfo == nil {
		writeError(w, "gpg_sign_info is required", http.StatusBadRequest)
		return
	}
	if req.Client == "" {
		req.Client = "unknown"
	}
	senderInfo := resolvePeerInfo(r.Context(), h.trimProcessChain)

	// WYSIWYS: bind the human-visible commit metadata to the exact bytes that
	// will be fed to gpg, discarding the client's own author/committer/message/
	// parent fields. Without this, a caller could present benign metadata in the
	// approval prompt (and desktop notification, and web UI) while signing
	// attacker-chosen bytes — a consent spoof / signing oracle. Every downstream
	// path reads these fields off GPGSignInfo, so binding once here covers them all.
	bindDisplayToCommitObject(req.GPGSignInfo, senderInfo)

	commitSubject := req.GPGSignInfo.CommitMsg
	if i := strings.IndexByte(commitSubject, '\n'); i >= 0 {
		commitSubject = commitSubject[:i]
	}

	// Trusted signer: run gpg and record the result directly, bypassing the
	// pending request flow so no desktop notification appears.
	if h.manager.CheckTrustedSigner(senderInfo, req.GPGSignInfo.RepoName, req.GPGSignInfo.ChangedFiles) {
		h.signAndRecordAutoApproved(w, &req, senderInfo, commitSubject, "trusted signer")
		return
	}

	// Ephemeral auto-approve rule (created by "approve and auto-approve" on a
	// prior notification). Same effect as trusted signer for the rule's TTL.
	if rule := h.manager.CheckAutoApproveRules(senderInfo, nil, approval.RequestTypeGPGSign); rule != nil {
		h.signAndRecordAutoApproved(w, &req, senderInfo, commitSubject,
			fmt.Sprintf("auto-approve rule %s", rule.ID))
		return
	}

	id, err := h.manager.CreateGPGSignRequest(req.Client, req.GPGSignInfo, senderInfo)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	slog.Info("gpg sign request created",
		"request_id", id,
		"repo", req.GPGSignInfo.RepoName,
		"process", senderInfo.InvokerName,
		"pid", senderInfo.PID,
		"commit", commitSubject,
	)

	writeJSON(w, GPGSignResponse{RequestID: id})
}

// bindDisplayToCommitObject re-derives the human-visible commit metadata
// (author, committer, message, parent hash) from the raw CommitObject bytes that
// will actually be fed to gpg, overwriting whatever the client supplied. This
// enforces WYSIWYS: the approval prompt can only ever show metadata computed from
// the exact bytes being signed. A legitimate thin client derives these same fields
// with the same parser (internal/gpgsign.Run), so for honest callers this is a
// no-op; a mismatch means the caller tried to display metadata that does not match
// the signed payload, which we log and then override.
//
// RepoName and ChangedFiles are NOT derivable from a commit object (they describe
// the working tree, not the signed bytes) and are deliberately left untouched here;
// see CheckTrustedSigner for their (necessarily weaker) treatment.
func bindDisplayToCommitObject(info *approval.GPGSignInfo, senderInfo approval.SenderInfo) {
	author, committer, message, parentHash := gpgsign.ParseCommitObject([]byte(info.CommitObject))
	if author != info.Author || committer != info.Committer || message != info.CommitMsg {
		slog.Warn("gpg sign: client-supplied commit metadata does not match signed bytes; overriding with parsed values",
			"pid", senderInfo.PID,
			"process", senderInfo.InvokerName,
		)
	}
	info.Author = author
	info.Committer = committer
	info.CommitMsg = message
	info.ParentHash = parentHash
}

// signAndRecordAutoApproved runs gpg and records an auto-approved gpg_sign
// request. Shared by the trusted-signer and ephemeral-auto-approve-rule paths;
// both want the same outcome — sign without showing a notification — and differ
// only in the log line.
func (h *Handlers) signAndRecordAutoApproved(w http.ResponseWriter, req *GPGSignRequest, senderInfo approval.SenderInfo, commitSubject, reason string) {
	gpgPath, findErr := h.resolver.GPGRunner.FindGPG()
	if findErr != nil {
		writeError(w, fmt.Sprintf("gpg exec failed: %v", findErr), http.StatusInternalServerError)
		return
	}
	res := h.resolver.runGPGWithNotify(gpgPath, req.GPGSignInfo.KeyID, []byte(req.GPGSignInfo.CommitObject), req.GPGSignInfo, senderInfo)
	if res.err != nil {
		writeError(w, fmt.Sprintf("gpg exec failed: %v", res.err), http.StatusInternalServerError)
		return
	}
	if res.exitCode != 0 {
		slog.Error("auto-approved gpg failed", "reason", reason, "exit_code", res.exitCode)
	}

	id, err := h.manager.RecordAutoApprovedGPGSign(req.Client, req.GPGSignInfo, senderInfo, res.sig, res.status)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	slog.Info("gpg sign auto-approved",
		"request_id", id,
		"reason", reason,
		"repo", req.GPGSignInfo.RepoName,
		"process", senderInfo.InvokerName,
		"pid", senderInfo.PID,
		"commit", commitSubject,
	)
	writeJSON(w, GPGSignResponse{RequestID: id})
}
