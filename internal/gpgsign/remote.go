package gpgsign

import (
	"context"
	"fmt"

	"github.com/nikicat/secrets-dispatcher/internal/apiclient"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/unixsock"
)

// The env var naming the forwarded remote socket that the thin client signs
// through (ADR 0011).
const remoteSocketEnv = "SECRETS_DISPATCHER_REMOTE_SOCKET"

// The route of a remote sign request.
const RemoteSignPath = "/remote/v1/gpg-sign"

// The body of a gpg-sign request, posted to RemoteSignPath and to the local
// API's /api/v1/gpg-sign/request. Both routes take the same body, because the
// thin client changes only the socket it uses (ADR 0011).
type SignRequest struct {
	GPGSignInfo *approval.GPGSignInfo `json:"gpg_sign_info"`
}

// The answer of the local API's /api/v1/gpg-sign/request, which names the
// filed request. RemoteSignPath answers with a SignOutcome instead.
type SignResponse struct {
	RequestID approval.RequestID `json:"request_id"`
}

// Submits the given sign info through the given remote socket and blocks until
// the request is resolved. Cancelling the given context cancels the request on
// the daemon. Errors when the socket approves the request without gpg's output.
func SignRemote(ctx context.Context, socketPath unixsock.Path, info *approval.GPGSignInfo) (SignOutcome, error) {
	remote := socketAPI(socketPath, remoteSocketIdentity{})
	res, err := apiclient.PostJSON[SignRequest, SignOutcome](ctx, remote, RemoteSignPath, SignRequest{GPGSignInfo: info})
	if err != nil {
		return SignOutcome{}, fmt.Errorf("remote socket %s: %w", socketPath, err)
	}
	if err := res.check(); err != nil {
		return SignOutcome{}, fmt.Errorf("remote socket %s: %w", socketPath, err)
	}
	return res, nil
}
