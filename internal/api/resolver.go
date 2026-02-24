package api

import (
	"log/slog"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

// Resolver resolves approval requests, handling GPG signing when needed.
// It implements the notification.Approver interface structurally.
type Resolver struct {
	Manager   *approval.Manager
	GPGRunner GPGRunner
}

// NewResolver creates a Resolver with the default GPG runner.
func NewResolver(manager *approval.Manager) *Resolver {
	return &Resolver{
		Manager:   manager,
		GPGRunner: &defaultGPGRunner{},
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

func (r *Resolver) approveGPGSign(id string, req *approval.Request) error {
	gpgPath, err := r.GPGRunner.FindGPG()
	if err != nil {
		slog.Error("failed to find real gpg", "error", err)
		return r.Manager.ApproveGPGFailed(id, nil, 2)
	}

	sig, status, exitCode, err := r.GPGRunner.RunGPG(gpgPath, req.GPGSignInfo.KeyID, []byte(req.GPGSignInfo.CommitObject))
	if err != nil || exitCode != 0 {
		slog.Error("gpg signing failed", "error", err, "exit_code", exitCode)
		return r.Manager.ApproveGPGFailed(id, status, exitCode)
	}

	return r.Manager.ApproveWithSignature(id, sig, status)
}
