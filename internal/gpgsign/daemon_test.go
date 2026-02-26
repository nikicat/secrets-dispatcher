package gpgsign

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

// roundTripFunc is an adapter to allow the use of ordinary functions as http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestCancelSigningRequest_Success(t *testing.T) {
	var gotPath string
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		json.NewEncoder(w).Encode(map[string]string{"status": "cancelled"})
	}))
	defer srv.Close()

	client := &DaemonClient{
		token: "test-token",
		httpClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				req.URL.Scheme = "http"
				req.URL.Host = strings.TrimPrefix(srv.URL, "http://")
				return http.DefaultTransport.RoundTrip(req)
			}),
		},
	}

	err := client.CancelSigningRequest(context.Background(), "test-request-id")
	if err != nil {
		t.Fatalf("CancelSigningRequest failed: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", gotMethod)
	}
	if gotPath != "/api/v1/pending/test-request-id/cancel" {
		t.Errorf("expected /api/v1/pending/test-request-id/cancel, got %s", gotPath)
	}
}

func TestCancelSigningRequest_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
	}))
	defer srv.Close()

	client := &DaemonClient{
		token: "test-token",
		httpClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				req.URL.Scheme = "http"
				req.URL.Host = strings.TrimPrefix(srv.URL, "http://")
				return http.DefaultTransport.RoundTrip(req)
			}),
		},
	}

	err := client.CancelSigningRequest(context.Background(), "nonexistent-id")
	if err == nil {
		t.Fatal("expected error for 404 response, got nil")
	}
}

// newTestClient creates a DaemonClient that redirects requests to the given httptest server.
func newTestClient(srv *httptest.Server, token string) *DaemonClient {
	return &DaemonClient{
		token: token,
		httpClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				req.URL.Scheme = "http"
				req.URL.Host = strings.TrimPrefix(srv.URL, "http://")
				return http.DefaultTransport.RoundTrip(req)
			}),
		},
	}
}

func TestPostSigningRequest_Success(t *testing.T) {
	var gotPath, gotMethod, gotContentType, gotAuth string
	var gotBody struct {
		Client      string               `json:"client"`
		GPGSignInfo *approval.GPGSignInfo `json:"gpg_sign_info"`
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		gotAuth = r.Header.Get("Authorization")
		json.NewDecoder(r.Body).Decode(&gotBody)
		json.NewEncoder(w).Encode(map[string]string{"request_id": "req-abc-123"})
	}))
	defer srv.Close()

	client := newTestClient(srv, "test-token")
	reqID, err := client.PostSigningRequest(context.Background(), "my-repo", &approval.GPGSignInfo{
		RepoName:  "my-repo",
		CommitMsg: "feat: add tests",
		Author:    "Alice <alice@example.com>",
		KeyID:     "ABCD1234",
	})
	if err != nil {
		t.Fatalf("PostSigningRequest failed: %v", err)
	}
	if reqID != "req-abc-123" {
		t.Errorf("request_id = %q, want %q", reqID, "req-abc-123")
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/api/v1/gpg-sign/request" {
		t.Errorf("path = %q, want /api/v1/gpg-sign/request", gotPath)
	}
	if gotContentType != "application/json" {
		t.Errorf("content-type = %q, want application/json", gotContentType)
	}
	if gotAuth != "Bearer test-token" {
		t.Errorf("authorization = %q, want %q", gotAuth, "Bearer test-token")
	}
	if gotBody.Client != "my-repo" {
		t.Errorf("body.client = %q, want %q", gotBody.Client, "my-repo")
	}
	if gotBody.GPGSignInfo.CommitMsg != "feat: add tests" {
		t.Errorf("body.gpg_sign_info.commit_msg = %q, want %q", gotBody.GPGSignInfo.CommitMsg, "feat: add tests")
	}
}

func TestPostSigningRequest_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := newTestClient(srv, "test-token")
	_, err := client.PostSigningRequest(context.Background(), "repo", &approval.GPGSignInfo{})
	if err == nil {
		t.Fatal("expected error for 500 response, got nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error = %q, want to contain '500'", err.Error())
	}
}

func TestPostSigningRequest_EmptyRequestID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"request_id": ""})
	}))
	defer srv.Close()

	client := newTestClient(srv, "test-token")
	_, err := client.PostSigningRequest(context.Background(), "repo", &approval.GPGSignInfo{})
	if err == nil {
		t.Fatal("expected error for empty request_id, got nil")
	}
	if !strings.Contains(err.Error(), "empty request_id") {
		t.Errorf("error = %q, want to contain 'empty request_id'", err.Error())
	}
}

// wsTestServer creates an httptest server that upgrades to WebSocket
// and sends the provided JSON messages, then closes.
func wsTestServer(t *testing.T, messages []any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("websocket accept: %v", err)
			return
		}
		defer conn.CloseNow()

		for _, msg := range messages {
			data, err := json.Marshal(msg)
			if err != nil {
				t.Errorf("marshal message: %v", err)
				return
			}
			if err := conn.Write(r.Context(), websocket.MessageText, data); err != nil {
				return // client may have closed
			}
		}
		conn.Close(websocket.StatusNormalClosure, "done")
	}))
}

func dialTestWS(t *testing.T, srvURL string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(srvURL, "http")
	conn, _, err := websocket.Dial(context.Background(), wsURL, nil)
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	conn.SetReadLimit(1 << 20)
	return conn
}

func TestWaitForResolution_Approved(t *testing.T) {
	wantSig := []byte("-----BEGIN PGP SIGNATURE-----\ntest\n-----END PGP SIGNATURE-----")
	srv := wsTestServer(t, []any{
		wsMsg{
			Type:      "request_resolved",
			ID:        "req-1",
			Result:    "approved",
			Signature: base64.StdEncoding.EncodeToString(wantSig),
			GPGStatus: "[GNUPG:] SIG_CREATED",
		},
	})
	defer srv.Close()

	conn := dialTestWS(t, srv.URL)
	defer conn.CloseNow()

	client := &DaemonClient{}
	sig, gpgStatus, exitCode, denied, err := client.WaitForResolution(context.Background(), conn, "req-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if denied {
		t.Error("denied = true, want false")
	}
	if exitCode != 0 {
		t.Errorf("exitCode = %d, want 0", exitCode)
	}
	if string(sig) != string(wantSig) {
		t.Errorf("signature = %q, want %q", sig, wantSig)
	}
	if string(gpgStatus) != "[GNUPG:] SIG_CREATED" {
		t.Errorf("gpgStatus = %q, want %q", gpgStatus, "[GNUPG:] SIG_CREATED")
	}
}

func TestWaitForResolution_Denied(t *testing.T) {
	srv := wsTestServer(t, []any{
		wsMsg{Type: "request_resolved", ID: "req-2", Result: "denied"},
	})
	defer srv.Close()

	conn := dialTestWS(t, srv.URL)
	defer conn.CloseNow()

	client := &DaemonClient{}
	sig, _, _, denied, err := client.WaitForResolution(context.Background(), conn, "req-2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !denied {
		t.Error("denied = false, want true")
	}
	if sig != nil {
		t.Errorf("signature = %q, want nil", sig)
	}
}

func TestWaitForResolution_Expired(t *testing.T) {
	srv := wsTestServer(t, []any{
		wsMsg{Type: "request_expired", ID: "req-3"},
	})
	defer srv.Close()

	conn := dialTestWS(t, srv.URL)
	defer conn.CloseNow()

	client := &DaemonClient{}
	_, _, _, _, err := client.WaitForResolution(context.Background(), conn, "req-3")
	if err == nil {
		t.Fatal("expected error for expired request, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %q, want to contain 'timed out'", err.Error())
	}
}

func TestWaitForResolution_Cancelled(t *testing.T) {
	srv := wsTestServer(t, []any{
		wsMsg{Type: "request_cancelled", ID: "req-4"},
	})
	defer srv.Close()

	conn := dialTestWS(t, srv.URL)
	defer conn.CloseNow()

	client := &DaemonClient{}
	_, _, _, denied, err := client.WaitForResolution(context.Background(), conn, "req-4")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !denied {
		t.Error("denied = false, want true (cancelled treated as denial)")
	}
}

func TestWaitForResolution_GPGFailure(t *testing.T) {
	srv := wsTestServer(t, []any{
		wsMsg{
			Type:      "request_resolved",
			ID:        "req-5",
			Result:    "approved",
			ExitCode:  2,
			GPGStatus: "[GNUPG:] ERROR",
		},
	})
	defer srv.Close()

	conn := dialTestWS(t, srv.URL)
	defer conn.CloseNow()

	client := &DaemonClient{}
	sig, gpgStatus, exitCode, denied, err := client.WaitForResolution(context.Background(), conn, "req-5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if denied {
		t.Error("denied = true, want false")
	}
	if exitCode != 2 {
		t.Errorf("exitCode = %d, want 2", exitCode)
	}
	if sig != nil {
		t.Errorf("signature = %q, want nil", sig)
	}
	if string(gpgStatus) != "[GNUPG:] ERROR" {
		t.Errorf("gpgStatus = %q, want %q", gpgStatus, "[GNUPG:] ERROR")
	}
}

func TestWaitForResolution_IgnoresOtherRequests(t *testing.T) {
	wantSig := []byte("real-sig")
	srv := wsTestServer(t, []any{
		// Messages for other request IDs should be skipped.
		wsMsg{Type: "request_resolved", ID: "other-req", Result: "denied"},
		wsMsg{Type: "request_expired", ID: "other-req-2"},
		wsMsg{Type: "snapshot", ID: "req-6"}, // non-resolution message type
		// The real resolution for our request.
		wsMsg{
			Type:      "request_resolved",
			ID:        "req-6",
			Result:    "approved",
			Signature: base64.StdEncoding.EncodeToString(wantSig),
		},
	})
	defer srv.Close()

	conn := dialTestWS(t, srv.URL)
	defer conn.CloseNow()

	client := &DaemonClient{}
	sig, _, _, denied, err := client.WaitForResolution(context.Background(), conn, "req-6")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if denied {
		t.Error("denied = true, want false")
	}
	if string(sig) != string(wantSig) {
		t.Errorf("signature = %q, want %q", sig, wantSig)
	}
}

func TestWaitForResolution_ContextCancelled(t *testing.T) {
	// Server that never sends our message — the context will cancel first.
	srv := wsTestServer(t, []any{
		wsMsg{Type: "snapshot", ID: "req-7"},
	})
	defer srv.Close()

	conn := dialTestWS(t, srv.URL)
	defer conn.CloseNow()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	client := &DaemonClient{}
	_, _, _, _, err := client.WaitForResolution(ctx, conn, "req-7")
	if err == nil {
		t.Fatal("expected error for cancelled context, got nil")
	}
}

func TestLoadAuthToken(t *testing.T) {
	t.Run("reads token from cookie file", func(t *testing.T) {
		dir := t.TempDir()
		cookieDir := filepath.Join(dir, "secrets-dispatcher")
		if err := os.MkdirAll(cookieDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cookieDir, ".cookie"), []byte("my-secret-token\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		t.Setenv("XDG_STATE_HOME", dir)
		token, err := loadAuthToken()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if token != "my-secret-token" {
			t.Errorf("token = %q, want %q", token, "my-secret-token")
		}
	})

	t.Run("returns error for missing file", func(t *testing.T) {
		t.Setenv("XDG_STATE_HOME", t.TempDir())
		_, err := loadAuthToken()
		if err == nil {
			t.Fatal("expected error for missing cookie file, got nil")
		}
	})

	t.Run("returns error for empty file", func(t *testing.T) {
		dir := t.TempDir()
		cookieDir := filepath.Join(dir, "secrets-dispatcher")
		if err := os.MkdirAll(cookieDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(cookieDir, ".cookie"), []byte(""), 0o600); err != nil {
			t.Fatal(err)
		}

		t.Setenv("XDG_STATE_HOME", dir)
		_, err := loadAuthToken()
		if err == nil {
			t.Fatal("expected error for empty cookie file, got nil")
		}
	})
}

func TestUnixSocketPath(t *testing.T) {
	t.Run("uses XDG_RUNTIME_DIR", func(t *testing.T) {
		t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
		got := unixSocketPath()
		want := "/run/user/1000/secrets-dispatcher/api.sock"
		if got != want {
			t.Errorf("unixSocketPath() = %q, want %q", got, want)
		}
	})

	t.Run("falls back to /run/user/<uid>", func(t *testing.T) {
		t.Setenv("XDG_RUNTIME_DIR", "")
		got := unixSocketPath()
		if !strings.HasPrefix(got, "/run/user/") || !strings.HasSuffix(got, "/secrets-dispatcher/api.sock") {
			t.Errorf("unixSocketPath() = %q, want /run/user/<uid>/secrets-dispatcher/api.sock", got)
		}
	})
}
