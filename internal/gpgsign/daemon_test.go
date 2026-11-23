package gpgsign

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/coder/websocket"
	"github.com/nikicat/secrets-dispatcher/internal/apiclient"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/nikicat/secrets-dispatcher/internal/unixsock"
	"github.com/nikicat/secrets-dispatcher/internal/wsmsg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCancelSigningRequest_Success(t *testing.T) {
	var gotPath string
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		json.NewEncoder(w).Encode(map[string]string{"status": "cancelled"})
	}))
	defer srv.Close()

	client := newTestClient(srv)

	err := client.CancelSigningRequest(context.Background(), "test-request-id")
	require.NoError(t, err)

	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "/api/v1/pending/test-request-id/cancel", gotPath)
}

func TestCancelSigningRequest_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
	}))
	defer srv.Close()

	client := newTestClient(srv)

	err := client.CancelSigningRequest(context.Background(), "nonexistent-id")
	assert.Error(t, err, "404 response")
}

const testToken config.AuthToken = "test-token"

// Returns a DaemonClient of the given httptest server.
func newTestClient(srv *httptest.Server) *DaemonClient {
	return &DaemonClient{api: apiclient.New(apiclient.BaseURL(srv.URL), testToken, srv.Client())}
}

const testCommitMessage = "feat: add tests"

func testSignInfo() *approval.GPGSignInfo {
	return approval.CommitSignInfoForTest("my-repo", testCommitMessage)
}

func TestPostSigningRequest_Success(t *testing.T) {
	var gotPath, gotMethod, gotContentType string
	var gotAuth config.AuthToken
	var rawBody map[string]json.RawMessage
	var gotBody SignRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		gotAuth, _ = config.ParseAuthorization(r.Header)
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &rawBody)
		json.Unmarshal(body, &gotBody)
		json.NewEncoder(w).Encode(SignResponse{RequestID: "req-abc-123"})
	}))
	defer srv.Close()

	client := newTestClient(srv)
	reqID, err := client.PostSigningRequest(context.Background(), testSignInfo())
	require.NoError(t, err)
	assert.Equal(t, approval.RequestID("req-abc-123"), reqID)
	assert.Equal(t, http.MethodPost, gotMethod)
	assert.Equal(t, "/api/v1/gpg-sign/request", gotPath)
	assert.Equal(t, "application/json", gotContentType)
	assert.Equal(t, testToken, gotAuth, "Bearer token")
	assert.NotContains(t, rawBody, "client", "the daemon names the client by its socket")
	assert.Equal(t, testCommitMessage, gotBody.GPGSignInfo.Signed().Message)
}

func TestPostSigningRequest_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := newTestClient(srv)
	_, err := client.PostSigningRequest(context.Background(), testSignInfo())
	assert.ErrorContains(t, err, "500")
}

func TestPostSigningRequest_EmptyRequestID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"request_id": ""})
	}))
	defer srv.Close()

	client := newTestClient(srv)
	_, err := client.PostSigningRequest(context.Background(), testSignInfo())
	assert.ErrorContains(t, err, "empty request_id")
}

// wsTestServer creates an httptest server that upgrades to WebSocket
// and sends the provided JSON messages, then closes.
func wsTestServer(t *testing.T, messages []any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if !assert.NoError(t, err, "websocket accept") {
			return
		}
		defer conn.CloseNow()

		for _, msg := range messages {
			data, err := json.Marshal(msg)
			if !assert.NoError(t, err, "marshal message") {
				return
			}
			if err := conn.Write(r.Context(), websocket.MessageText, data); err != nil {
				return // client may have closed
			}
		}
		conn.Close(websocket.StatusNormalClosure, "done")
	}))
}

// A daemon WebSocket message as the WaitForResolution tests send it.
type wsMsg struct {
	wsmsg.Header
	RequestEnd
}

// The request that the WaitForResolution tests wait for.
const testRequestID = "req"

// Returns how the request testRequestID ends when the WebSocket sends msgs.
func waitForTestResolution(t *testing.T, ctx context.Context, msgs ...any) (SignOutcome, error) {
	t.Helper()
	srv := wsTestServer(t, msgs)
	defer srv.Close()
	conn, err := newTestClient(srv).DialWebSocket(t.Context())
	require.NoError(t, err)
	defer conn.CloseNow()
	return WaitForResolution(ctx, conn, testRequestID)
}

// Returns the message of the given type that reports how the request with the
// given ID ended.
func ended(typ wsmsg.Type, id approval.RequestID, result approval.Resolution) wsMsg {
	return wsMsg{wsmsg.Header{Type: typ}, RequestEnd{ID: id, Result: result}}
}

// Returns the request_resolved message for request testRequestID.
func resolved(result approval.Resolution, out *approval.GPGResult) wsMsg {
	msg := ended(wsmsg.RequestResolved, testRequestID, result)
	msg.GPGResult = out
	return msg
}

func TestWaitForResolution(t *testing.T) {
	sig := []byte("-----BEGIN PGP SIGNATURE-----\ntest\n-----END PGP SIGNATURE-----")
	tests := []struct {
		name string
		msgs []any
		want SignOutcome
	}{
		{
			name: "approved",
			msgs: []any{resolved(approval.ResolutionApproved, &approval.GPGResult{Signature: sig, Status: []byte("[GNUPG:] SIG_CREATED")})},
			want: SignOutcome{Result: approval.ResolutionApproved,
				GPGResult: &approval.GPGResult{Signature: sig, Status: []byte("[GNUPG:] SIG_CREATED")}},
		},
		{
			name: "auto-approved",
			msgs: []any{resolved(approval.ResolutionAutoApproved, &approval.GPGResult{Signature: sig})},
			want: SignOutcome{Result: approval.ResolutionAutoApproved, GPGResult: &approval.GPGResult{Signature: sig}},
		},
		{
			name: "approved, gpg failed",
			msgs: []any{resolved(approval.ResolutionApproved, &approval.GPGResult{Status: []byte("[GNUPG:] ERROR"), ExitCode: 2})},
			want: SignOutcome{Result: approval.ResolutionApproved,
				GPGResult: &approval.GPGResult{Status: []byte("[GNUPG:] ERROR"), ExitCode: 2}},
		},
		{
			name: "denied",
			msgs: []any{resolved(approval.ResolutionDenied, nil)},
			want: SignOutcome{Result: approval.ResolutionDenied},
		},
		{
			name: "expired",
			msgs: []any{ended(wsmsg.RequestExpired, testRequestID, approval.ResolutionExpired)},
			want: SignOutcome{Result: approval.ResolutionExpired},
		},
		{
			name: "cancelled",
			msgs: []any{ended(wsmsg.RequestCancelled, testRequestID, approval.ResolutionCancelled)},
			want: SignOutcome{Result: approval.ResolutionCancelled},
		},
		{
			name: "skips other requests and message types",
			msgs: []any{
				ended(wsmsg.RequestResolved, "other-req", approval.ResolutionDenied),
				ended(wsmsg.RequestExpired, "other-req-2", approval.ResolutionExpired),
				ended(wsmsg.Snapshot, testRequestID, approval.ResolutionApproved),
				ended(wsmsg.RequestCancelled, testRequestID, approval.ResolutionCancelled),
			},
			want: SignOutcome{Result: approval.ResolutionCancelled},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := waitForTestResolution(t, context.Background(), tt.msgs...)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestWaitForResolution_BadSignature(t *testing.T) {
	_, err := waitForTestResolution(t, context.Background(),
		json.RawMessage(`{"type":"request_resolved","id":"other-req","result":"approved","signature":"not base64!"}`),
		json.RawMessage(`{"type":"request_resolved","id":"`+testRequestID+`","result":"approved","signature":"not base64!"}`))
	assert.ErrorContains(t, err, "decode message for request "+testRequestID)
}

func TestWaitForResolution_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := waitForTestResolution(t, ctx, ended(wsmsg.Snapshot, testRequestID, approval.ResolutionApproved))
	assert.Error(t, err)
}

func TestSignRemote_ApprovedWithoutOutput(t *testing.T) {
	socket := unixsock.Dir(t.TempDir()).Socket("remote")
	ln, err := socket.Listen()
	require.NoError(t, err)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"result":"approved"}`)) //nolint:errcheck
	}))
	srv.Listener = ln
	srv.Start()
	defer srv.Close()

	_, err = SignRemote(context.Background(), socket, testSignInfo())
	assert.ErrorContains(t, err, "without gpg output")
}
