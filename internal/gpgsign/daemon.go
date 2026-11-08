package gpgsign

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"net/http"

	"github.com/coder/websocket"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/nikicat/secrets-dispatcher/internal/wsmsg"
)

// DaemonClient communicates with the secrets-dispatcher daemon over a Unix socket.
type DaemonClient struct {
	socketPath string
	token      config.AuthToken
	httpClient *http.Client
}

// Placeholder host of URLs sent over a unix socket. The dialer ignores it.
const socketHost = "localhost"

// Largest WebSocket message read from the daemon. The default limit (32KB) is
// too small for snapshot messages that carry large commit objects. 1MB holds
// any realistic commit.
const wsReadLimit = 1 << 20

// Builds the http URL of path on a server reached through UnixSocketClient.
func SocketURL(path string) string { return "http://" + socketHost + path }

// Builds an HTTP client that sends every request to the unix socket at socketPath.
func UnixSocketClient(socketPath string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
	}}
}

// POSTs in as JSON, with header added, and decodes a 200 answer into out.
func postJSON(ctx context.Context, client *http.Client, path string, header http.Header, in, out any) error {
	data, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, SocketURL(path), bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	maps.Copy(req.Header, header)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("daemon unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("daemon returned status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// Returns a client of the daemon API at socketPath.
func NewDaemonClient(socketPath string, token config.AuthToken) *DaemonClient {
	return &DaemonClient{
		socketPath: socketPath,
		token:      token,
		httpClient: UnixSocketClient(socketPath),
	}
}

// Opens the daemon's event WebSocket. The caller closes it.
func (c *DaemonClient) DialWebSocket(ctx context.Context) (*websocket.Conn, error) {
	header := http.Header{}
	c.token.Authorize(header)
	conn, _, err := websocket.Dial(ctx, "ws://"+socketHost+"/api/v1/ws", &websocket.DialOptions{
		HTTPClient: c.httpClient,
		HTTPHeader: header,
	})
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(wsReadLimit)
	return conn, nil
}

// Files info as a gpg-sign approval request on behalf of requester, and
// returns its id.
func (c *DaemonClient) PostSigningRequest(ctx context.Context, requester string, info *approval.GPGSignInfo) (string, error) {
	body := struct {
		Client      string                `json:"client"`
		GPGSignInfo *approval.GPGSignInfo `json:"gpg_sign_info"`
	}{Client: requester, GPGSignInfo: info}

	var result struct {
		RequestID string `json:"request_id"`
	}
	header := http.Header{}
	c.token.Authorize(header)
	if err := postJSON(ctx, c.httpClient, "/api/v1/gpg-sign/request", header, body, &result); err != nil {
		return "", err
	}
	if result.RequestID == "" {
		return "", fmt.Errorf("daemon returned empty request_id")
	}
	return result.RequestID, nil
}

// Cancels the pending request with requestID. Errors when the daemon is
// unreachable or does not cancel it.
func (c *DaemonClient) CancelSigningRequest(ctx context.Context, requestID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, SocketURL("/api/v1/pending/"+requestID+"/cancel"), nil)
	if err != nil {
		return fmt.Errorf("create cancel request: %w", err)
	}
	c.token.Authorize(req.Header)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("cancel signing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("daemon returned status %d for cancel", resp.StatusCode)
	}
	return nil
}

// A gpg-sign request filed with the daemon, with the event WebSocket that
// reports how it ends.
type pendingSignRequest struct {
	client *DaemonClient
	conn   *websocket.Conn
	id     string
}

// Files info as a gpg-sign request on behalf of requester. The event
// WebSocket opens first, so the resolution event cannot come before anyone
// listens. The caller closes the request. Errors when the daemon does not
// answer or refuses the request.
func (c *DaemonClient) startSignRequest(ctx context.Context, requester string, info *approval.GPGSignInfo) (*pendingSignRequest, error) {
	conn, err := c.DialWebSocket(ctx)
	if err != nil {
		return nil, fmt.Errorf("daemon unreachable at %s (is secrets-dispatcher running?): %w", c.socketPath, err)
	}
	id, err := c.PostSigningRequest(ctx, requester, info)
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

// The fields of a daemon WebSocket message that the thin client reads. They
// mirror part of api.WSMessage. The wsmsg package doc says why the client
// cannot import api.
type wsMsg struct {
	Type wsmsg.Type `json:"type"`
	ID   string     `json:"id,omitempty"`
	SignOutcome
}

// Reads WebSocket messages from conn until the request with requestID is
// resolved, expires or is cancelled, and returns how it ended. Errors when
// conn fails, which includes ctx ending, and when a message for the request
// does not decode.
func WaitForResolution(ctx context.Context, conn *websocket.Conn, requestID string) (SignOutcome, error) {
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
		// Other message types (snapshot, request_created, etc.) — keep reading.
	}
}
