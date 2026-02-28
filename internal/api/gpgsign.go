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

// runGPG finds the real gpg binary and signs the commit object.
func (h *Handlers) runGPG(info *approval.GPGSignInfo) (sig, status []byte, exitCode int, err error) {
	gpgPath, err := h.resolver.GPGRunner.FindGPG()
	if err != nil {
		return nil, nil, 0, err
	}
	return h.resolver.GPGRunner.RunGPG(gpgPath, info.KeyID, []byte(info.CommitObject))
}

// GPGSignRequest is the POST body for /api/v1/gpg-sign/request.
type GPGSignRequest struct {
	Client      string               `json:"client"`
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

	commitSubject := req.GPGSignInfo.CommitMsg
	if i := strings.IndexByte(commitSubject, '\n'); i >= 0 {
		commitSubject = commitSubject[:i]
	}

	// Trusted signer: run gpg and record the result directly, bypassing the
	// pending request flow so no desktop notification appears.
	if h.manager.CheckTrustedSigner(senderInfo, req.GPGSignInfo.RepoName, req.GPGSignInfo.ChangedFiles) {
		sig, status, exitCode, err := h.runGPG(req.GPGSignInfo)
		if err != nil {
			writeError(w, fmt.Sprintf("gpg exec failed: %v", err), http.StatusInternalServerError)
			return
		}
		if exitCode != 0 {
			slog.Error("trusted signer gpg failed", "exit_code", exitCode)
		}

		id, err := h.manager.RecordAutoApprovedGPGSign(req.Client, req.GPGSignInfo, senderInfo, sig, status)
		if err != nil {
			writeError(w, err.Error(), http.StatusInternalServerError)
			return
		}
		slog.Info("gpg sign auto-approved (trusted signer)",
			"request_id", id,
			"repo", req.GPGSignInfo.RepoName,
			"process", senderInfo.UnitName,
			"pid", senderInfo.PID,
			"commit", commitSubject,
		)
		writeJSON(w, GPGSignResponse{RequestID: id})
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
		"process", senderInfo.UnitName,
		"pid", senderInfo.PID,
		"commit", commitSubject,
	)

	writeJSON(w, GPGSignResponse{RequestID: id})
}
