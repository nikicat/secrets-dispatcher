package api

import (
	"log/slog"

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
func (r *Resolver) Approve(id approval.RequestID) error {
	req := r.Manager.GetPending(id)
	if req == nil {
		return approval.ErrNotFound
	}

	if sign, ok := req.GPGSign(); ok {
		_, err := r.approveGPGSign(req, sign)
		return err
	}

	return r.Manager.Approve(id)
}

// Deny denies a pending request by ID.
func (r *Resolver) Deny(id approval.RequestID) error {
	return r.Manager.Deny(id)
}

// Adds an auto-approve rule made from the cancelled request with the given ID.
// Errors with approval.ErrNotFound when no cancelled request has that ID, and
// as approval.Request.AutoApproveRule does otherwise.
func (r *Resolver) AutoApprove(requestID approval.RequestID) error {
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
func (r *Resolver) ApproveAndAutoApprove(id approval.RequestID) error {
	req := r.Manager.GetPending(id)
	if req == nil {
		return approval.ErrNotFound
	}

	if sign, ok := req.GPGSign(); ok {
		rule, err := req.AutoApproveRule()
		if err != nil {
			return err
		}
		res, err := r.approveGPGSign(req, sign)
		if err != nil || !res.Succeeded() {
			return err
		}
		r.Manager.AddAutoApproveRule(rule)
		return nil
	}

	return r.Manager.ApproveAndAutoApprove(id)
}

// Signs the given object with the real gpg, and reports a slow gpg as
// proxy.WithSlowNotify does for the given call. Errors as GPGRunner.Sign does.
func (r *Resolver) signGPG(sign *approval.GPGSignInfo, call proxy.UpstreamCallContext) (approval.GPGResult, error) {
	// WithSlowNotify carries one value, so the result and its error travel together.
	type run struct {
		res approval.GPGResult
		err error
	}
	out := proxy.WithSlowNotify(r.SlowUpstream, call, func() run {
		res, err := r.GPGRunner.Sign(sign.KeyID, sign.SignedObject)
		return run{res, err}
	})
	return out.res, out.err
}

// Signs the given pending gpg_sign request with the real gpg, and approves it
// with what gpg produced, failure included. Returns that result. Errors as
// Manager.ApproveGPGSign does.
func (r *Resolver) approveGPGSign(req *approval.Request, sign *approval.GPGSignInfo) (approval.GPGResult, error) {
	res, err := r.signGPG(sign, proxy.PromptedCall(req.RequestInfo))
	if err != nil || !res.Succeeded() {
		slog.Error("gpg signing failed", "error", err, "exit_code", res.ExitCode)
	}
	return res, r.Manager.ApproveGPGSign(req.ID, res)
}
