package gpgsign

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"

	"github.com/coder/websocket"

	"github.com/nikicat/secrets-dispatcher/internal/apiclient"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/nikicat/secrets-dispatcher/internal/unixsock"
	"github.com/nikicat/secrets-dispatcher/internal/wsmsg"
)

// The largest WebSocket message the thin client reads from the daemon. A
// snapshot carries the signed objects of pending requests, so coder/websocket's
// 32KB default is too small.
const wsReadLimit = 1 << 20

// A client of the daemon API on its unix socket.
type DaemonClient struct {
	socketPath unixsock.Path
	api        apiclient.Client
}

// The base URL of a server reached through UnixSocketClient. Its host is a
// placeholder that the dialer ignores.
const SocketBaseURL apiclient.BaseURL = "http://localhost"

// Builds an HTTP client that sends every request to the given unix socket.
func UnixSocketClient(socketPath unixsock.Path) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return socketPath.Dial(ctx)
		},
	}}
}

// Returns a client of the daemon API over the given unix socket.
func socketAPI(socketPath unixsock.Path, credential apiclient.Credential) apiclient.Client {
	return apiclient.New(SocketBaseURL, credential, UnixSocketClient(socketPath))
}

// Sets no header, because the socket that a remote's request arrives on is the
// remote's identity (ADR 0011).
type remoteSocketIdentity struct{}

func (remoteSocketIdentity) Authorize(http.Header) {}

// Returns a client of the daemon API on the given socket.
func NewDaemonClient(socketPath unixsock.Path, token config.AuthToken) *DaemonClient {
	return &DaemonClient{socketPath: socketPath, api: socketAPI(socketPath, token)}
}

// Opens the daemon's event WebSocket. The caller closes it.
func (c *DaemonClient) DialWebSocket(ctx context.Context) (*websocket.Conn, error) {
	conn, err := c.api.DialWebSocket(ctx, "/api/v1/ws")
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(wsReadLimit)
	return conn, nil
}

// Files the given sign info as a gpg-sign request with the daemon, and returns
// the request's ID without waiting for its resolution, unlike SignRemote.
func (c *DaemonClient) PostSigningRequest(ctx context.Context, info *approval.GPGSignInfo) (approval.RequestID, error) {
	result, err := apiclient.PostJSON[SignRequest, SignResponse](ctx, c.api, "/api/v1/gpg-sign/request", SignRequest{GPGSignInfo: info})
	if err != nil {
		return "", err
	}
	if result.RequestID == "" {
		return "", fmt.Errorf("daemon returned empty request_id")
	}
	return result.RequestID, nil
}

// Cancels the pending request with the given ID. Errors when the daemon is
// unreachable or does not cancel it.
func (c *DaemonClient) CancelSigningRequest(ctx context.Context, requestID approval.RequestID) error {
	if err := c.api.PostAction(ctx, requestID, "cancel"); err != nil {
		return fmt.Errorf("cancel signing request: %w", err)
	}
	return nil
}

// A gpg-sign request filed with the daemon, with the event WebSocket that
// reports how it ends.
type pendingSignRequest struct {
	client *DaemonClient
	conn   *websocket.Conn
	id     approval.RequestID
}

// Files the given sign info as a gpg-sign request and returns the pending
// request. The request listens for its end before it is filed, so
// pendingSignRequest.Wait cannot miss it. The caller closes it.
func (c *DaemonClient) startSignRequest(ctx context.Context, info *approval.GPGSignInfo) (*pendingSignRequest, error) {
	conn, err := c.DialWebSocket(ctx)
	if err != nil {
		return nil, fmt.Errorf("daemon unreachable at %s (is secrets-dispatcher running?): %w", c.socketPath, err)
	}
	id, err := c.PostSigningRequest(ctx, info)
	if err != nil {
		conn.CloseNow()
		return nil, fmt.Errorf("failed to send signing request: %w", err)
	}
	return &pendingSignRequest{client: c, conn: conn, id: id}, nil
}

// Blocks until the request is resolved, expires or is cancelled, and returns
// how it ended. Errors as WaitForResolution does.
func (r *pendingSignRequest) Wait(ctx context.Context) (SignOutcome, error) {
	return WaitForResolution(ctx, r.conn, r.id)
}

// Cancels the request on the daemon. Errors as
// DaemonClient.CancelSigningRequest does.
func (r *pendingSignRequest) Cancel(ctx context.Context) error {
	return r.client.CancelSigningRequest(ctx, r.id)
}

// Closes the event WebSocket.
func (r *pendingSignRequest) Close() { r.conn.CloseNow() }

// The payload of the messages that report how a request ended.
type RequestEnd struct {
	ID approval.RequestID `json:"id"`
	SignOutcome
}

// Returns the payload that reports how the request of the given History entry
// ended.
func RequestEndOf(entry approval.HistoryEntry) RequestEnd {
	return RequestEnd{ID: entry.Request.ID, SignOutcome: OutcomeOf(entry)}
}

// Returns how the request with the given ID ended, once the daemon reports it
// resolved, expired or cancelled. Errors when the request's message does not
// decode, or approves it without gpg's output.
func WaitForResolution(ctx context.Context, conn *websocket.Conn, requestID approval.RequestID) (SignOutcome, error) {
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return SignOutcome{}, fmt.Errorf("websocket read: %w", err)
		}

		var header wsmsg.Header
		if json.Unmarshal(data, &header) != nil || !header.Type.EndsRequest() {
			continue
		}
		var msg RequestEnd
		// Unmarshal fills the fields it can before it reports a bad value, so
		// the id of a message whose signature does not decode is known.
		err = json.Unmarshal(data, &msg)
		if msg.ID != requestID {
			// Another request's message, or one too broken to tell.
			continue
		}
		if err != nil {
			return SignOutcome{}, fmt.Errorf("decode message for request %s: %w", requestID, err)
		}
		return msg.SignOutcome, msg.SignOutcome.check()
	}
}
