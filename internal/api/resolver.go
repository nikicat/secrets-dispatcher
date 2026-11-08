package api

import (
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
		return r.approveGPGSign(id, req)
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
		gpgPath, err := r.GPGRunner.FindGPG()
		if err != nil {
			slog.Error("failed to find real gpg", "error", err)
			return r.Manager.ApproveGPGSign(id, approval.GPGResult{ExitCode: approval.GPGNotFound})
		}

		res, err := r.runGPGWithNotify(gpgPath, req.GPGSignInfo.KeyID, []byte(req.GPGSignInfo.CommitObject), req.GPGSignInfo, req.SenderInfo)
		if err != nil || res.ExitCode != 0 {
			slog.Error("gpg signing failed", "error", err, "exit_code", res.ExitCode)
			return r.Manager.ApproveGPGSign(id, res)
		}

		if err := r.Manager.ApproveGPGSign(id, res); err != nil {
			return err
		}
		r.Manager.AddAutoApproveRule(req)
		return nil
	}

	return r.Manager.ApproveAndAutoApprove(id)
}

// runGPGWithNotify wraps RunGPG with slow upstream notification.
func (r *Resolver) runGPGWithNotify(gpgPath, keyID string, commitObject []byte, info *approval.GPGSignInfo, senderInfo approval.SenderInfo) (approval.GPGResult, error) {
	// WithSlowNotify carries one value, so the result and its error travel together.
	type run struct {
		res approval.GPGResult
		err error
	}
	items := gpgSignItems(info)
	out := proxy.WithSlowNotify(r.SlowThreshold, r.UpstreamNotifier, proxy.UpstreamCallContext{
		RequestType: approval.RequestTypeGPGSign,
		Items:       items,
		SenderInfo:  senderInfo,
	}, func() run {
		res, err := r.GPGRunner.RunGPG(gpgPath, keyID, commitObject)
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

func (r *Resolver) approveGPGSign(id string, req *approval.Request) error {
	gpgPath, err := r.GPGRunner.FindGPG()
	if err != nil {
		slog.Error("failed to find real gpg", "error", err)
		return r.Manager.ApproveGPGSign(id, approval.GPGResult{ExitCode: approval.GPGNotFound})
	}

	res, err := r.runGPGWithNotify(gpgPath, req.GPGSignInfo.KeyID, []byte(req.GPGSignInfo.CommitObject), req.GPGSignInfo, req.SenderInfo)
	if err != nil || res.ExitCode != 0 {
		slog.Error("gpg signing failed", "error", err, "exit_code", res.ExitCode)
	}
	return r.Manager.ApproveGPGSign(id, res)
}
