package gpgsign

import (
	"bytes"
	"context"
	"encoding/base64"
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

// NewDaemonClient creates a client connected to the daemon's Unix socket.
func NewDaemonClient(socketPath string, token config.AuthToken) *DaemonClient {
	return &DaemonClient{
		socketPath: socketPath,
		token:      token,
		httpClient: UnixSocketClient(socketPath),
	}
}

// DialWebSocket opens a WebSocket connection to the daemon's /api/v1/ws endpoint
// using the Unix socket transport with Bearer token authentication.
// The caller must close the connection when done.
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
	// The default read limit (32KB) is too small for snapshot messages
	// containing large commit objects. 1MB accommodates any realistic commit.
	conn.SetReadLimit(1 << 20)
	return conn, nil
}

// PostSigningRequest sends a gpg_sign approval request to the daemon and returns
// the request ID. The caller must have already established a WebSocket connection
// to receive the resolution event.
func (c *DaemonClient) PostSigningRequest(ctx context.Context, client string, info *approval.GPGSignInfo) (string, error) {
	body := struct {
		Client      string                `json:"client"`
		GPGSignInfo *approval.GPGSignInfo `json:"gpg_sign_info"`
	}{Client: client, GPGSignInfo: info}

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

// CancelSigningRequest sends a cancel request to the daemon for a pending GPG sign request.
// Best-effort: errors are returned but callers may choose to ignore them (process is exiting).
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

// The fields of a daemon WebSocket message that the thin client reads. They
// mirror part of api.WSMessage. The wsmsg package doc says why the client
// cannot import api.
type wsMsg struct {
	Type      wsmsg.Type          `json:"type"`
	ID        string              `json:"id,omitempty"`
	Result    approval.Resolution `json:"result,omitempty"`
	Signature string              `json:"signature,omitempty"`
	GPGStatus string              `json:"gpg_status,omitempty"`
	ExitCode  int                 `json:"exit_code,omitempty"`
}

// WaitForResolution reads WebSocket messages until the request with the given ID
// is resolved (approved or denied) or expires.
//
// On success (approved, gpg exit 0): returns (signature, gpgStatus, 0, false, nil).
// On gpg failure (approved, gpg exit != 0): returns (nil, gpgStatus, exitCode, false, nil).
// On denial: returns (nil, nil, 0, true, nil).
// On expiry: returns (nil, nil, 0, false, error).
func (c *DaemonClient) WaitForResolution(ctx context.Context, conn *websocket.Conn, requestID string) (signature, gpgStatus []byte, exitCode int, denied bool, err error) {
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return nil, nil, 0, false, fmt.Errorf("websocket read: %w", err)
		}

		var msg wsMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			// Ignore malformed messages and keep reading.
			continue
		}

		switch msg.Type {
		case wsmsg.RequestResolved:
			if msg.ID != requestID {
				continue
			}
			switch msg.Result {
			case approval.ResolutionDenied:
				return nil, nil, 0, true, nil
			case approval.ResolutionApproved, approval.ResolutionAutoApproved:
				if msg.ExitCode != 0 {
					// gpg ran but failed — propagate its exit code.
					return nil, []byte(msg.GPGStatus), msg.ExitCode, false, nil
				}
				// Decode base64 signature.
				sig, err := base64.StdEncoding.DecodeString(msg.Signature)
				if err != nil {
					return nil, nil, 0, false, fmt.Errorf("decode signature: %w", err)
				}
				return sig, []byte(msg.GPGStatus), 0, false, nil
			}

		case wsmsg.RequestExpired:
			if msg.ID != requestID {
				continue
			}
			return nil, nil, 0, false, fmt.Errorf("signing request timed out (request_id=%s)", requestID)

		case wsmsg.RequestCancelled:
			if msg.ID != requestID {
				continue
			}
			return nil, nil, 0, true, nil
		}
		// Other message types (snapshot, request_created, etc.) — keep reading.
	}
}
