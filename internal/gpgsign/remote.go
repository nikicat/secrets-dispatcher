package gpgsign

import (
	"context"
	"fmt"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

// The env var naming the forwarded remote socket that the thin client signs
// through (ADR 0011).
const remoteSocketEnv = "SECRETS_DISPATCHER_REMOTE_SOCKET"

// The route of a remote sign request.
const RemoteSignPath = "/remote/v1/gpg-sign"

// RemoteSignRequest is the body POSTed to RemoteSignPath.
type RemoteSignRequest struct {
	GPGSignInfo *approval.GPGSignInfo `json:"gpg_sign_info"`
}

// Submits info through the remote socket and blocks until the request is
// resolved. Cancelling ctx drops the connection, which cancels the request on
// the daemon. Errors when the socket approves the request without gpg's output.
func SignRemote(ctx context.Context, socketPath string, info *approval.GPGSignInfo) (SignOutcome, error) {
	var res SignOutcome
	if err := postJSON(ctx, UnixSocketClient(socketPath), RemoteSignPath, nil, RemoteSignRequest{GPGSignInfo: info}, &res); err != nil {
		return res, fmt.Errorf("remote socket %s: %w", socketPath, err)
	}
	if err := res.check(); err != nil {
		return SignOutcome{}, fmt.Errorf("remote socket %s: %w", socketPath, err)
	}
	return res, nil
}
