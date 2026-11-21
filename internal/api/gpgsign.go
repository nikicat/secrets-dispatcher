package api

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os/exec"
	"strings"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/gpgsign"
	"github.com/nikicat/secrets-dispatcher/internal/proxy"
)

// GPGRunner finds and executes the real gpg binary.
type GPGRunner interface {
	FindGPG() (string, error)
	RunGPG(gpgPath, keyID string, commitObject []byte) (approval.GPGResult, error)
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
func (d *defaultGPGRunner) RunGPG(gpgPath, keyID string, commitObject []byte) (approval.GPGResult, error) {
	cmd := exec.Command(gpgPath, "--status-fd=2", "-bsau", keyID)
	cmd.Stdin = bytes.NewReader(commitObject)
	var sigBuf, statusBuf bytes.Buffer
	cmd.Stdout = &sigBuf
	cmd.Stderr = &statusBuf
	err := cmd.Run()
	var exitCode approval.GPGExitCode
	if err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			exitCode = approval.GPGExitCode(exitErr.ExitCode())
		} else {
			return approval.GPGResult{Status: statusBuf.Bytes(), ExitCode: approval.GPGNotStarted}, fmt.Errorf("gpg exec failed: %w", err)
		}
	}
	res := approval.GPGResult{Status: statusBuf.Bytes(), ExitCode: exitCode}
	if res.Succeeded() {
		res.Signature = sigBuf.Bytes()
	}
	return res, nil
}

// GPGSignRequest is the POST body for /api/v1/gpg-sign/request.
type GPGSignRequest struct {
	Client      approval.ClientName   `json:"client"`
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
	var req GPGSignRequest
	if !decodePost(w, r, &req) || !requireSignedBytes(w, req.GPGSignInfo) {
		return
	}
	if req.Client == "" {
		req.Client = approval.ClientUnknown
	}
	senderInfo := resolvePeerInfo(r.Context(), h.chains)

	bindDisplayToSignedPayload(req.GPGSignInfo, senderInfo)

	commitSubject := req.GPGSignInfo.CommitMsg
	if i := strings.IndexByte(commitSubject, '\n'); i >= 0 {
		commitSubject = commitSubject[:i]
	}

	// Trusted signer: run gpg and record the result directly, bypassing the
	// pending request flow so no desktop notification appears.
	if h.resolver.Manager.CheckTrustedSigner(senderInfo, req.GPGSignInfo.RepoName, req.GPGSignInfo.ChangedFiles) {
		h.signAndRecordAutoApproved(w, &req, senderInfo, commitSubject, "trusted signer")
		return
	}

	// Ephemeral auto-approve rule (created by "approve and auto-approve" on a
	// prior notification). Same effect as trusted signer for the rule's TTL.
	if rule := h.resolver.Manager.CheckAutoApproveRules(approval.RequestInfo{Type: approval.RequestTypeGPGSign, SenderInfo: senderInfo}); rule != nil {
		h.signAndRecordAutoApproved(w, &req, senderInfo, commitSubject,
			fmt.Sprintf("auto-approve rule %s", rule.ID))
		return
	}

	id, err := h.resolver.Manager.CreateGPGSignRequest(req.Client, req.GPGSignInfo, senderInfo)
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

// Writes a 400 and returns false when info has no bytes to sign.
func requireSignedBytes(w http.ResponseWriter, info *approval.GPGSignInfo) bool {
	switch err := info.RequireSignedBytes(); {
	case err == nil:
		return true
	case errors.Is(err, approval.ErrNoGPGSignInfo):
		writeError(w, "gpg_sign_info is required", http.StatusBadRequest)
	case errors.Is(err, approval.ErrNoCommitObject):
		writeError(w, "gpg_sign_info.commit_object is required", http.StatusBadRequest)
	}
	return false
}

// Overwrites info's display fields with the ones parsed from info.CommitObject,
// and logs when the client sent others (ADR 0008). RepoName and ChangedFiles
// describe the working tree, not the signed bytes, so they stay as sent.
func bindDisplayToSignedPayload(info *approval.GPGSignInfo, senderInfo approval.SenderInfo) {
	p := gpgsign.ParseSignedPayload([]byte(info.CommitObject))
	if p.Signer != info.Author || p.Committer != info.Committer ||
		p.Message != info.CommitMsg || string(p.Kind) != info.Kind {
		slog.Warn("gpg sign: client-supplied metadata does not match signed bytes; overriding with parsed values",
			"client", senderInfo.Client,
			"pid", senderInfo.PID,
			"process", senderInfo.InvokerName,
		)
	}
	info.Kind = string(p.Kind)
	info.Author = p.Signer
	info.Committer = p.Committer
	info.CommitMsg = p.Message
	info.ParentHash = p.ParentHash
	info.TagName = p.TagName
	info.Target = p.Target
	info.Pushee = p.Pushee
}

// signAndRecordAutoApproved runs gpg and records an auto-approved gpg_sign
// request. Shared by the trusted-signer and ephemeral-auto-approve-rule paths;
// both want the same outcome — sign without showing a notification — and differ
// only in the log line.
func (h *Handlers) signAndRecordAutoApproved(w http.ResponseWriter, req *GPGSignRequest, senderInfo approval.SenderInfo, commitSubject, reason string) {
	res, err := h.resolver.signGPG(req.GPGSignInfo, proxy.UpstreamCallContext{SenderInfo: senderInfo})
	if err != nil {
		writeError(w, fmt.Sprintf("gpg exec failed: %v", err), http.StatusInternalServerError)
		return
	}
	if !res.Succeeded() {
		slog.Error("auto-approved gpg failed", "reason", reason, "exit_code", res.ExitCode)
	}

	id, err := h.resolver.Manager.RecordAutoApprovedGPGSign(req.Client, req.GPGSignInfo, senderInfo, res)
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
