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

// HandleGPGSignRequest handles POST /api/v1/gpg-sign/request.
// It creates a non-blocking gpg_sign approval request and returns the request ID.
// The result (signature or denial) is delivered via the WebSocket request_resolved event.
func (h *Handlers) HandleGPGSignRequest(w http.ResponseWriter, r *http.Request) {
	var body gpgsign.SignRequest
	if !decodePost(w, r, &body) || !requireSignedBytes(w, body.GPGSignInfo) {
		return
	}
	request, ok := boundSignRequest(w, body.GPGSignInfo, resolvePeerInfo(r.Context(), h.chains))
	if !ok {
		return
	}

	commitSubject := request.GPGSignInfo.CommitMsg
	if i := strings.IndexByte(commitSubject, '\n'); i >= 0 {
		commitSubject = commitSubject[:i]
	}

	// Trusted signer: run gpg and record the result directly, bypassing the
	// pending request flow so no desktop notification appears.
	if h.resolver.Manager.CheckTrustedSigner(request) {
		h.signAndRecordAutoApproved(w, request, commitSubject, "trusted signer")
		return
	}

	// Ephemeral auto-approve rule (created by "approve and auto-approve" on a
	// prior notification). Same effect as trusted signer for the rule's TTL.
	if rule := h.resolver.Manager.CheckAutoApproveRules(request); rule != nil {
		h.signAndRecordAutoApproved(w, request, commitSubject,
			fmt.Sprintf("auto-approve rule %s", rule.ID))
		return
	}

	id, err := h.resolver.Manager.CreateGPGSignRequest(request)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	slog.Info("gpg sign request created",
		"request_id", id,
		"repo", request.GPGSignInfo.RepoName,
		"process", request.SenderInfo.InvokerName,
		"pid", request.SenderInfo.PID,
		"commit", commitSubject,
	)

	writeJSON(w, gpgsign.SignResponse{RequestID: id})
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

// Rewrites info's display fields from its signed bytes as BindDisplay does, logs
// a warning when they changed, and returns sender's request for info. Writes a
// 400 and returns false when CommitObject cannot be parsed.
func boundSignRequest(w http.ResponseWriter, info *approval.GPGSignInfo, sender approval.SenderInfo) (approval.RequestInfo, bool) {
	payload, err := gpgsign.ParseSignedPayload([]byte(info.CommitObject))
	if err != nil {
		writeError(w, err.Error(), http.StatusBadRequest)
		return approval.RequestInfo{}, false
	}
	if payload.BindDisplay(info) {
		slog.Warn("gpg sign: client-supplied metadata does not match signed bytes; overriding with parsed values",
			"client", sender.Client,
			"pid", sender.PID,
			"process", sender.InvokerName,
		)
	}
	return info.RequestInfo(sender), true
}

// Signs request without a prompt, records it as auto-approved, and writes its
// ID to w. Writes a 500 instead when gpg cannot run or the record fails.
func (h *Handlers) signAndRecordAutoApproved(w http.ResponseWriter, request approval.RequestInfo, commitSubject, reason string) {
	res, err := h.resolver.signGPG(request, proxy.RequestCall)
	if err != nil {
		writeError(w, fmt.Sprintf("gpg exec failed: %v", err), http.StatusInternalServerError)
		return
	}
	if !res.Succeeded() {
		slog.Error("auto-approved gpg failed", "reason", reason, "exit_code", res.ExitCode)
	}

	id, err := h.resolver.Manager.RecordAutoApprovedGPGSign(request, res)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	slog.Info("gpg sign auto-approved",
		"request_id", id,
		"reason", reason,
		"repo", request.GPGSignInfo.RepoName,
		"process", request.SenderInfo.InvokerName,
		"pid", request.SenderInfo.PID,
		"commit", commitSubject,
	)
	writeJSON(w, gpgsign.SignResponse{RequestID: id})
}
