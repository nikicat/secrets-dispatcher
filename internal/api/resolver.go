package api

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/proxy"
)

// Resolver resolves approval requests, handling GPG signing when needed.
// It implements the notification.Approver interface structurally.
type Resolver struct {
	Manager      *approval.Manager
	GPGRunner    GPGRunner
	SlowUpstream proxy.SlowUpstream
}

// NewResolver creates a Resolver with the default GPG runner.
func NewResolver(manager *approval.Manager, slowUpstream proxy.SlowUpstream) *Resolver {
	return &Resolver{
		Manager:      manager,
		GPGRunner:    &defaultGPGRunner{},
		SlowUpstream: slowUpstream,
	}
}

// Approve resolves a pending request. For GPG signing requests, it runs the
// real gpg binary to produce the signature before approving.
func (r *Resolver) Approve(id string) error {
	req := r.Manager.GetPending(id)
	if req == nil {
		return approval.ErrNotFound
	}

	if req.Type == approval.RequestTypeGPGSign && req.GPGSignInfo != nil {
		_, err := r.approveGPGSign(id, req)
		return err
	}

	return r.Manager.Approve(id)
}

// Deny denies a pending request by ID.
func (r *Resolver) Deny(id string) error {
	return r.Manager.Deny(id)
}

// Adds an auto-approve rule made from the cancelled request requestID, as
// Manager.AddAutoApproveRule does. Errors with approval.ErrNotFound when no
// cancelled request has that ID, and as approval.Request.AutoApproveRule does
// otherwise.
func (r *Resolver) AutoApprove(requestID string) error {
	entry := r.Manager.GetHistoryEntry(requestID)
	if entry == nil {
		return approval.ErrNotFound
	}
	if entry.Resolution != approval.ResolutionCancelled {
		return approval.ErrNotFound
	}
	rule, err := entry.Request.AutoApproveRule()
	if err != nil {
		return err
	}
	r.Manager.AddAutoApproveRule(rule)
	return nil
}

// Approves the pending request id and adds an auto-approve rule, as
// Manager.ApproveAndAutoApprove does, with the same errors. A gpg_sign request
// is approved with gpg's result, and a failed signature earns no rule.
func (r *Resolver) ApproveAndAutoApprove(id string) error {
	req := r.Manager.GetPending(id)
	if req == nil {
		return approval.ErrNotFound
	}

	if req.Type == approval.RequestTypeGPGSign && req.GPGSignInfo != nil {
		rule, err := req.AutoApproveRule()
		if err != nil {
			return err
		}
		res, err := r.approveGPGSign(id, req)
		if err != nil || !res.Succeeded() {
			return err
		}
		r.Manager.AddAutoApproveRule(rule)
		return nil
	}

	return r.Manager.ApproveAndAutoApprove(id)
}

// Signs info's commit object with the real gpg. A slow gpg is reported as
// proxy.WithSlowNotify decides for call. Errors when gpg cannot be found or
// started, and the result then carries GPGNotFound or GPGNotStarted.
func (r *Resolver) signGPG(info *approval.GPGSignInfo, call proxy.UpstreamCallContext) (approval.GPGResult, error) {
	gpgPath, err := r.GPGRunner.FindGPG()
	if err != nil {
		return approval.GPGResult{ExitCode: approval.GPGNotFound}, fmt.Errorf("find gpg: %w", err)
	}
	// WithSlowNotify carries one value, so the result and its error travel together.
	type run struct {
		res approval.GPGResult
		err error
	}
	call.RequestType = approval.RequestTypeGPGSign
	call.Items = gpgSignItems(info)
	out := proxy.WithSlowNotify(r.SlowUpstream, call, func() run {
		res, err := r.GPGRunner.RunGPG(gpgPath, info.KeyID, []byte(info.CommitObject))
		return run{res, err}
	})
	return out.res, out.err
}

// gpgSignItems builds ItemInfo for GPG signing notifications.
func gpgSignItems(info *approval.GPGSignInfo) []approval.ItemInfo {
	if info == nil {
		return nil
	}
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
	return []approval.ItemInfo{{Label: label}}
}

// Signs the pending gpg_sign request req with the real gpg and approves it
// with what gpg produced, failure included. Returns that result. Errors as
// Manager.ApproveGPGSign does.
func (r *Resolver) approveGPGSign(id string, req *approval.Request) (approval.GPGResult, error) {
	res, err := r.signGPG(req.GPGSignInfo, proxy.UpstreamCallContext{SenderInfo: req.SenderInfo, Prompted: true})
	if err != nil || !res.Succeeded() {
		slog.Error("gpg signing failed", "error", err, "exit_code", res.ExitCode)
	}
	return res, r.Manager.ApproveGPGSign(id, res)
}
