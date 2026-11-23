// Package apiclient calls the daemon's HTTP API.
package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/coder/websocket"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

// Proves who sends the requests to the daemon API.
type Credential interface {
	Authorize(h http.Header)
}

// The last segment of a pending request's route that acts on it, such as
// "approve" or "cancel".
type Action string

// The root URL of an HTTP server, such as "http://127.0.0.1:8484", with no
// trailing slash.
type BaseURL string

// Returns the http URL of the given path on the server.
func (u BaseURL) URL(path string) string { return string(u) + path }

// Returns the WebSocket URL of the given path on the server.
func (u BaseURL) WebSocketURL(path string) string {
	return "ws" + strings.TrimPrefix(string(u), "http") + path
}

func (u BaseURL) String() string { return string(u) }

type Client struct {
	baseURL    BaseURL
	credential Credential
	http       *http.Client
}

func New(baseURL BaseURL, credential Credential, httpClient *http.Client) Client {
	return Client{baseURL: baseURL, credential: credential, http: httpClient}
}

// A request body encoded as JSON.
type jsonBody []byte

// Sends a request with the client's credential. Errors when the daemon is
// unreachable or answers a status other than 200. The caller closes the
// answer's body.
func (c Client) do(ctx context.Context, method, path string, body jsonBody) (*http.Response, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL.URL(path), reader)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c.credential.Authorize(req.Header)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("daemon unreachable: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, statusError(resp)
	}
	return resp, nil
}

// The body of the API's error answers.
type ErrorResponse struct {
	Error string `json:"error"`
}

// Returns the error of an answer whose status is not 200, with the API's
// message when the body carries one.
func statusError(resp *http.Response) error {
	var body ErrorResponse
	if json.NewDecoder(resp.Body).Decode(&body) == nil && body.Error != "" {
		return fmt.Errorf("daemon returned status %d: %s", resp.StatusCode, body.Error)
	}
	return fmt.Errorf("daemon returned status %d", resp.StatusCode)
}

// GETs the given path and decodes the answer. Errors as Client.do does, and
// when the answer does not decode.
func GetJSON[Answer any](ctx context.Context, c Client, path string) (Answer, error) {
	var answer Answer
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return answer, err
	}
	return decode[Answer](resp)
}

// POSTs the given request as JSON to the given path and decodes the answer.
// Errors as Client.do does, and when the answer does not decode.
func PostJSON[Request, Answer any](ctx context.Context, c Client, path string, request Request) (Answer, error) {
	var answer Answer
	data, err := json.Marshal(request)
	if err != nil {
		return answer, fmt.Errorf("marshal request: %w", err)
	}
	resp, err := c.do(ctx, http.MethodPost, path, data)
	if err != nil {
		return answer, err
	}
	return decode[Answer](resp)
}

// Decodes and closes the given answer's body.
func decode[Answer any](resp *http.Response) (Answer, error) {
	defer resp.Body.Close()
	var answer Answer
	if err := json.NewDecoder(resp.Body).Decode(&answer); err != nil {
		return answer, fmt.Errorf("decode response: %w", err)
	}
	return answer, nil
}

// POSTs the given action on the pending request with the given ID. Errors as
// Client.do does.
func (c Client) PostAction(ctx context.Context, id approval.RequestID, action Action) error {
	resp, err := c.do(ctx, http.MethodPost, "/api/v1/pending/"+string(id)+"/"+string(action), nil)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// Opens the WebSocket at the given path with the client's credential. The
// caller closes it.
func (c Client) DialWebSocket(ctx context.Context, path string) (*websocket.Conn, error) {
	header := http.Header{}
	c.credential.Authorize(header)
	conn, _, err := websocket.Dial(ctx, c.baseURL.WebSocketURL(path), &websocket.DialOptions{
		HTTPClient: c.http,
		HTTPHeader: header,
	})
	return conn, err
}
