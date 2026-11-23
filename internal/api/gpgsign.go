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

type GPGRunner interface {
	// Returns what gpg produced when it signed the object with the given key. A gpg that runs
	// and fails is no error. Errors when gpg cannot be found or started, and the result's
	// ExitCode is then approval.GPGNotFound or approval.GPGNotStarted.
	Sign(keyID approval.GPGKeyID, obj approval.SignedObject) (approval.GPGResult, error)
}

// Runs the first gpg in PATH that is not this binary.
type defaultGPGRunner struct{}

// Runs gpg with the flags that ADR 0008 records.
func (d *defaultGPGRunner) Sign(keyID approval.GPGKeyID, obj approval.SignedObject) (approval.GPGResult, error) {
	gpgPath, err := gpgsign.FindRealGPG()
	if err != nil {
		return approval.GPGResult{ExitCode: approval.GPGNotFound}, fmt.Errorf("find gpg: %w", err)
	}
	cmd := exec.Command(gpgPath, "--status-fd=2", "-bsau", string(keyID))
	cmd.Stdin = bytes.NewReader(obj)
	// The signature and the status lines go to separate buffers, since status
	// lines mixed into stdout would corrupt the signature.
	var sigBuf, statusBuf bytes.Buffer
	cmd.Stdout = &sigBuf
	cmd.Stderr = &statusBuf
	err = cmd.Run()
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

// Signs the request at once when its signer is trusted or an auto-approve rule
// matches it, and otherwise leaves it pending. Responds with its ID. Errors as
// decodeSignRequest does, and with 500 when gpg cannot run.
func (h *Handlers) HandleGPGSignRequest(w http.ResponseWriter, r *http.Request) {
	sign, ok := decodeSignRequest(w, r)
	if !ok {
		return
	}
	request := sign.RequestInfo(resolvePeerInfo(r.Context(), h.chains))

	if h.resolver.Manager.CheckTrustedSigner(request) {
		h.signAndRecordAutoApproved(w, sign, request, "trusted signer")
		return
	}
	if rule, ok := h.resolver.Manager.CheckAutoApproveRules(request); ok {
		h.signAndRecordAutoApproved(w, sign, request, fmt.Sprintf("auto-approve rule %s", rule.ID))
		return
	}

	id := h.resolver.Manager.Create(request)
	slog.Info("gpg sign request created", "request_id", id, signLogAttrs(sign, request.SenderInfo))
	writeJSON(w, gpgsign.SignResponse{RequestID: id})
}

// Decodes a gpg-sign request body. Errors as decodePost does, and with 400
// when the body has no gpg_sign_info.
func decodeSignRequest(w http.ResponseWriter, r *http.Request) (*approval.GPGSignInfo, bool) {
	var body gpgsign.SignRequest
	if !decodePost(w, r, &body) {
		return nil, false
	}
	if body.GPGSignInfo == nil {
		writeError(w, "gpg_sign_info is required", http.StatusBadRequest)
		return nil, false
	}
	return body.GPGSignInfo, true
}

// Returns the log attributes that name a gpg_sign request's repo, invoker and
// commit subject, as a group that log handlers inline.
func signLogAttrs(sign *approval.GPGSignInfo, sender approval.SenderInfo) slog.Attr {
	subject, _, _ := strings.Cut(sign.Signed().Message, "\n")
	return slog.GroupAttrs("",
		slog.String("repo", string(sign.RepoName)),
		slog.String("process", sender.InvokerName),
		slog.Uint64("pid", uint64(sender.PID)),
		slog.String("commit", subject),
	)
}

// Signs the given request without a prompt, records it as auto-approved with
// what gpg produced, failure included, and responds with its ID. Responds with
// 500 instead when gpg cannot run.
func (h *Handlers) signAndRecordAutoApproved(w http.ResponseWriter, sign *approval.GPGSignInfo, request approval.RequestInfo, reason string) {
	res, err := h.resolver.signGPG(sign, proxy.RequestCall(request))
	if err != nil {
		writeError(w, fmt.Sprintf("gpg exec failed: %v", err), http.StatusInternalServerError)
		return
	}
	if !res.Succeeded() {
		slog.Error("auto-approved gpg failed", "reason", reason, "exit_code", res.ExitCode, signLogAttrs(sign, request.SenderInfo))
	}

	id := h.resolver.Manager.RecordAutoApprovedGPGSign(request, res)
	slog.Info("gpg sign auto-approved", "request_id", id, "reason", reason, signLogAttrs(sign, request.SenderInfo))
	writeJSON(w, gpgsign.SignResponse{RequestID: id})
}
