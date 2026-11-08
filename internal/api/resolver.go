package api

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/proxy"
)

// Resolver resolves approval requests, handling GPG signing when needed.
// It implements the notification.Approver interface structurally.
type Resolver struct {
	Manager          *approval.Manager
	GPGRunner        GPGRunner
	UpstreamNotifier proxy.UpstreamNotifier
	SlowThreshold    time.Duration
}

// NewResolver creates a Resolver with the default GPG runner.
func NewResolver(manager *approval.Manager, upstreamNotifier proxy.UpstreamNotifier, slowThreshold time.Duration) *Resolver {
	return &Resolver{
		Manager:          manager,
		GPGRunner:        &defaultGPGRunner{},
		UpstreamNotifier: upstreamNotifier,
		SlowThreshold:    slowThreshold,
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

// AutoApprove creates an auto-approve rule from a cancelled request.
func (r *Resolver) AutoApprove(requestID string) error {
	entry := r.Manager.GetHistoryEntry(requestID)
	if entry == nil {
		return approval.ErrNotFound
	}
	if entry.Resolution != approval.ResolutionCancelled {
		return approval.ErrNotFound
	}
	r.Manager.AddAutoApproveRule(entry.Request)
	return nil
}

// ApproveAndAutoApprove approves a pending request and creates an auto-approve
// rule for similar future requests. For GPG signing requests, it runs the real
// gpg binary to produce the signature before approving.
func (r *Resolver) ApproveAndAutoApprove(id string) error {
	req := r.Manager.GetPending(id)
	if req == nil {
		return approval.ErrNotFound
	}

	if req.Type == approval.RequestTypeGPGSign && req.GPGSignInfo != nil {
		// A failed signature is approved too, but earns no rule.
		res, err := r.approveGPGSign(id, req)
		if err != nil || !res.Succeeded() {
			return err
		}
		r.Manager.AddAutoApproveRule(req)
		return nil
	}

	return r.Manager.ApproveAndAutoApprove(id)
}

// Signs info's commit object with the real gpg, notifying the user when gpg is
// slow. Errors when gpg cannot be found or started. The result then carries
// GPGNotFound or GPGNotStarted.
func (r *Resolver) signGPG(info *approval.GPGSignInfo, senderInfo approval.SenderInfo) (approval.GPGResult, error) {
	gpgPath, err := r.GPGRunner.FindGPG()
	if err != nil {
		return approval.GPGResult{ExitCode: approval.GPGNotFound}, fmt.Errorf("find gpg: %w", err)
	}
	// WithSlowNotify carries one value, so the result and its error travel together.
	type run struct {
		res approval.GPGResult
		err error
	}
	out := proxy.WithSlowNotify(r.SlowThreshold, r.UpstreamNotifier, proxy.UpstreamCallContext{
		RequestType: approval.RequestTypeGPGSign,
		Items:       gpgSignItems(info),
		SenderInfo:  senderInfo,
	}, func() run {
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
	label := info.RepoName
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
	res, err := r.signGPG(req.GPGSignInfo, req.SenderInfo)
	if err != nil || !res.Succeeded() {
		slog.Error("gpg signing failed", "error", err, "exit_code", res.ExitCode)
	}
	return res, r.Manager.ApproveGPGSign(id, res)
}
