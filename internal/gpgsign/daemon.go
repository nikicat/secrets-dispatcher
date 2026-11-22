package gpgsign

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"

	"github.com/coder/websocket"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/nikicat/secrets-dispatcher/internal/unixsock"
	"github.com/nikicat/secrets-dispatcher/internal/wsmsg"
)

// The largest WebSocket message the thin client reads from the daemon. A
// snapshot carries the signed objects of pending requests, so coder/websocket's
// 32KB default is too small.
const wsReadLimit = 1 << 20

// DaemonClient communicates with the secrets-dispatcher daemon over a Unix socket.
type DaemonClient struct {
	socketPath unixsock.Path
	api        socketClient
}

// Placeholder host of URLs sent over a unix socket. The dialer ignores it.
const socketHost = "localhost"

// Builds the http URL of path on a server reached through UnixSocketClient.
func SocketURL(path string) string { return "http://" + socketHost + path }

// Builds the WebSocket URL of path on a server reached through UnixSocketClient.
func socketWSURL(path string) string { return "ws://" + socketHost + path }

// Builds an HTTP client that sends every request to the unix socket at socketPath.
func UnixSocketClient(socketPath unixsock.Path) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return socketPath.Dial(ctx)
		},
	}}
}

// An HTTP client of a daemon socket.
type socketClient struct {
	http       *http.Client
	credential socketCredential
}

// Proves who sends the requests to a daemon socket. The local API takes the
// master token (ADR 0006).
type socketCredential interface {
	Authorize(h http.Header)
}

// Sets no header, because the socket that a remote's request arrives on is the
// remote's identity (ADR 0011).
type remoteSocketIdentity struct{}

func (remoteSocketIdentity) Authorize(http.Header) {}

// Sends the given request with the client's credential. Errors when the server
// is unreachable or answers a status other than 200.
func (c socketClient) do(req *http.Request) (*http.Response, error) {
	c.credential.Authorize(req.Header)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("daemon unreachable: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("daemon returned status %d", resp.StatusCode)
	}
	return resp, nil
}

// POSTs an empty request to the given path. Errors as socketClient.do does.
func (c socketClient) postEmpty(ctx context.Context, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, SocketURL(path), nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	resp, err := c.do(req)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// POSTs the given sign request as JSON to the given path and decodes the
// answer. Errors as socketClient.do does, and when the answer does not decode.
func postSign[Answer SignResponse | SignOutcome](ctx context.Context, c socketClient, path string, sign SignRequest) (Answer, error) {
	var answer Answer
	data, err := json.Marshal(sign)
	if err != nil {
		return answer, fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, SocketURL(path), bytes.NewReader(data))
	if err != nil {
		return answer, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.do(req)
	if err != nil {
		return answer, err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&answer); err != nil {
		return answer, fmt.Errorf("decode response: %w", err)
	}
	return answer, nil
}

// Returns a client of the daemon API at socketPath.
func NewDaemonClient(socketPath unixsock.Path, token config.AuthToken) *DaemonClient {
	return &DaemonClient{
		socketPath: socketPath,
		api:        socketClient{http: UnixSocketClient(socketPath), credential: token},
	}
}

// Opens the daemon's event WebSocket. The caller closes it.
func (c *DaemonClient) DialWebSocket(ctx context.Context) (*websocket.Conn, error) {
	header := http.Header{}
	c.api.credential.Authorize(header)
	conn, _, err := websocket.Dial(ctx, socketWSURL("/api/v1/ws"), &websocket.DialOptions{
		HTTPClient: c.api.http,
		HTTPHeader: header,
	})
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(wsReadLimit)
	return conn, nil
}

// Files info as a gpg-sign request with the daemon, and returns the request's
// ID without waiting for its resolution, unlike SignRemote.
func (c *DaemonClient) PostSigningRequest(ctx context.Context, info *approval.GPGSignInfo) (approval.RequestID, error) {
	result, err := postSign[SignResponse](ctx, c.api, "/api/v1/gpg-sign/request", SignRequest{GPGSignInfo: info})
	if err != nil {
		return "", err
	}
	if result.RequestID == "" {
		return "", fmt.Errorf("daemon returned empty request_id")
	}
	return result.RequestID, nil
}

// Cancels the pending request with requestID. Errors when the daemon is
// unreachable or does not cancel it.
func (c *DaemonClient) CancelSigningRequest(ctx context.Context, requestID approval.RequestID) error {
	if err := c.api.postEmpty(ctx, "/api/v1/pending/"+string(requestID)+"/cancel"); err != nil {
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

// Files info as a gpg-sign request and returns it while it is pending. The
// returned request already listens for its resolution, so Wait cannot miss it.
// The caller closes it.
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

// Cancels the request on the daemon. Errors when the daemon does not answer
// or does not find it.
func (r *pendingSignRequest) Cancel(ctx context.Context) error {
	return r.client.CancelSigningRequest(ctx, r.id)
}

// Closes the event WebSocket.
func (r *pendingSignRequest) Close() { r.conn.CloseNow() }

type wsMsg struct {
	wsmsg.Header
	// A removed auto-approve rule's message carries the rule's id here, which
	// matches no request.
	ID approval.RequestID `json:"id,omitempty"`
	SignOutcome
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

		var msg wsMsg
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

		switch msg.Type {
		case wsmsg.RequestResolved:
			return msg.SignOutcome, msg.SignOutcome.check()
		case wsmsg.RequestExpired:
			return SignOutcome{Result: approval.ResolutionExpired}, nil
		case wsmsg.RequestCancelled:
			return SignOutcome{Result: approval.ResolutionCancelled}, nil
		}
	}
}
