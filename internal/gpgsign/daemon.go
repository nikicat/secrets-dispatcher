package gpgsign

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"

	"github.com/coder/websocket"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

// DaemonClient communicates with the secrets-dispatcher daemon over a Unix socket.
type DaemonClient struct {
	socketPath string
	token      string
	httpClient *http.Client
}

// NewDaemonClient creates a client connected to the daemon's Unix socket.
func NewDaemonClient(socketPath, token string) *DaemonClient {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
	}
	return &DaemonClient{
		socketPath: socketPath,
		token:      token,
		httpClient: &http.Client{Transport: transport},
	}
}

// DialWebSocket opens a WebSocket connection to the daemon's /api/v1/ws endpoint
// using the Unix socket transport with Bearer token authentication.
// The caller must close the connection when done.
func (c *DaemonClient) DialWebSocket(ctx context.Context) (*websocket.Conn, error) {
	conn, _, err := websocket.Dial(ctx, "ws://localhost/api/v1/ws", &websocket.DialOptions{
		HTTPClient: c.httpClient,
		HTTPHeader: http.Header{
			"Authorization": {"Bearer " + c.token},
		},
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
		Client      string               `json:"client"`
		GPGSignInfo *approval.GPGSignInfo `json:"gpg_sign_info"`
	}{Client: client, GPGSignInfo: info}

	data, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/api/v1/gpg-sign/request", bytes.NewReader(data))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("post signing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("daemon returned status %d", resp.StatusCode)
	}

	var result struct {
		RequestID string `json:"request_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	if result.RequestID == "" {
		return "", fmt.Errorf("daemon returned empty request_id")
	}
	return result.RequestID, nil
}

// CancelSigningRequest sends a cancel request to the daemon for a pending GPG sign request.
// Best-effort: errors are returned but callers may choose to ignore them (process is exiting).
func (c *DaemonClient) CancelSigningRequest(ctx context.Context, requestID string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost/api/v1/pending/"+requestID+"/cancel", nil)
	if err != nil {
		return fmt.Errorf("create cancel request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

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

// wsMsg is a local type for parsing WebSocket messages from the daemon.
// Defined locally to avoid a circular import with the api package.
type wsMsg struct {
	Type      string `json:"type"`
	ID        string `json:"id,omitempty"`
	Result    string `json:"result,omitempty"`
	Signature string `json:"signature,omitempty"`
	GPGStatus string `json:"gpg_status,omitempty"`
	ExitCode  int    `json:"exit_code,omitempty"`
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
		case "request_resolved":
			if msg.ID != requestID {
				continue
			}
			if msg.Result == "denied" {
				return nil, nil, 0, true, nil
			}
			if msg.Result == "approved" || msg.Result == "auto_approved" {
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

		case "request_expired":
			if msg.ID != requestID {
				continue
			}
			return nil, nil, 0, false, fmt.Errorf("signing request timed out (request_id=%s)", requestID)

		case "request_cancelled":
			if msg.ID != requestID {
				continue
			}
			return nil, nil, 0, true, nil
		}
		// Other message types (snapshot, request_created, etc.) — keep reading.
	}
}
