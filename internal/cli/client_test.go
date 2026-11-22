package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nikicat/secrets-dispatcher/internal/api"
	"github.com/nikicat/secrets-dispatcher/internal/config"
)

// Closes the server when the test ends.
func newTestClient(t *testing.T, token config.AuthToken, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return NewClient(config.ListenAddr(server.Listener.Addr().String()), token)
}

func TestClient_Pending(t *testing.T) {
	requests := []PendingEntry{
		{Request: RequestRecord{ID: "req-123", Client: "user@host", Items: []ItemInfo{{Label: "Secret1", Path: "/org/secrets/1"}}, CreatedAt: time.Now(), Type: "get_secret"}, ExpiresAt: time.Now().Add(5 * time.Minute)},
	}

	client := newTestClient(t, "test-token", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/pending" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if token, err := config.ParseAuthorization(r.Header); err != nil || token != "test-token" {
			t.Errorf("missing or invalid auth header")
		}
		json.NewEncoder(w).Encode(PendingResponse{Requests: requests})
	})
	result, err := client.Pending()
	if err != nil {
		t.Fatalf("Pending failed: %v", err)
	}

	if len(result) != 1 {
		t.Fatalf("expected 1 request, got %d", len(result))
	}
	if result[0].Request.ID != "req-123" {
		t.Errorf("unexpected ID: %s", result[0].Request.ID)
	}
}

func TestClient_History(t *testing.T) {
	entries := []HistoryEntry{
		{
			Request:    RequestRecord{ID: "req-456", Client: "user@host"},
			Resolution: "approved",
			ResolvedAt: time.Now(),
		},
	}

	client := newTestClient(t, "test-token", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/log" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(HistoryResponse{Entries: entries})
	})
	result, err := client.History()
	if err != nil {
		t.Fatalf("History failed: %v", err)
	}

	if len(result) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(result))
	}
	if result[0].Resolution != "approved" {
		t.Errorf("unexpected resolution: %s", result[0].Resolution)
	}
}

func TestClient_Approve(t *testing.T) {
	client := newTestClient(t, "test-token", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/pending":
			json.NewEncoder(w).Encode(PendingResponse{
				Requests: []PendingEntry{{Request: RequestRecord{ID: "abc-123-def"}}},
			})
		case "/api/v1/pending/abc-123-def/approve":
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			json.NewEncoder(w).Encode(api.ActionResponse{Status: "approved"})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	})
	id, err := client.Resolve("abc-123-def", Approve{})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if id != "abc-123-def" {
		t.Errorf("Resolve returned %q, want %q", id, "abc-123-def")
	}
}

func TestClient_Deny(t *testing.T) {
	client := newTestClient(t, "test-token", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/pending":
			json.NewEncoder(w).Encode(PendingResponse{
				Requests: []PendingEntry{{Request: RequestRecord{ID: "xyz-789"}}},
			})
		case "/api/v1/pending/xyz-789/deny":
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			json.NewEncoder(w).Encode(api.ActionResponse{Status: "denied"})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	})
	id, err := client.Resolve("xyz-789", Deny{})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if id != "xyz-789" {
		t.Errorf("Resolve returned %q, want %q", id, "xyz-789")
	}
}

func TestClient_PartialID_ExactMatch(t *testing.T) {
	client := newTestClient(t, "test-token", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/pending":
			json.NewEncoder(w).Encode(PendingResponse{
				Requests: []PendingEntry{
					{Request: RequestRecord{ID: "abc"}},
					{Request: RequestRecord{ID: "abcdef"}},
				},
			})
		case "/api/v1/pending/abc/approve":
			json.NewEncoder(w).Encode(api.ActionResponse{Status: "approved"})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	})
	// "abc" should match exactly, not be ambiguous with "abcdef"
	id, err := client.Resolve("abc", Approve{})
	if err != nil {
		t.Fatalf("Resolve with exact match failed: %v", err)
	}
	if id != "abc" {
		t.Errorf("Resolve with exact match returned %q, want %q", id, "abc")
	}
}

func TestClient_PartialID_PrefixMatch(t *testing.T) {
	client := newTestClient(t, "test-token", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/pending":
			json.NewEncoder(w).Encode(PendingResponse{
				Requests: []PendingEntry{
					{Request: RequestRecord{ID: "abc-123-456"}},
					{Request: RequestRecord{ID: "def-789"}},
				},
			})
		case "/api/v1/pending/abc-123-456/approve":
			json.NewEncoder(w).Encode(api.ActionResponse{Status: "approved"})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	})
	id, err := client.Resolve("abc", Approve{})
	if err != nil {
		t.Fatalf("Resolve with prefix failed: %v", err)
	}
	if id != "abc-123-456" {
		t.Errorf("Resolve with prefix returned %q, want %q", id, "abc-123-456")
	}
}

func TestClient_PartialID_Ambiguous(t *testing.T) {
	client := newTestClient(t, "test-token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(PendingResponse{
			Requests: []PendingEntry{
				{Request: RequestRecord{ID: "abc-123"}},
				{Request: RequestRecord{ID: "abc-456"}},
			},
		})
	})
	_, err := client.Resolve("abc", Approve{})
	if err == nil {
		t.Fatal("expected error for ambiguous ID")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("expected ambiguous error, got: %v", err)
	}
}

func TestClient_PartialID_NotFound(t *testing.T) {
	client := newTestClient(t, "test-token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(PendingResponse{
			Requests: []PendingEntry{{Request: RequestRecord{ID: "xyz-123"}}},
		})
	})
	_, err := client.Resolve("abc", Approve{})
	if err == nil {
		t.Fatal("expected error for not found")
	}
	if !strings.Contains(err.Error(), "no request found") {
		t.Errorf("expected not found error, got: %v", err)
	}
}

func TestClient_Find_Pending(t *testing.T) {
	requests := []PendingEntry{
		{Request: RequestRecord{ID: "req-111", Client: "client1"}},
		{Request: RequestRecord{ID: "req-222", Client: "client2"}},
	}

	client := newTestClient(t, "test-token", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/pending":
			json.NewEncoder(w).Encode(PendingResponse{Requests: requests})
		case "/api/v1/log":
			json.NewEncoder(w).Encode(HistoryResponse{})
		}
	})

	result, err := client.Find("req-222")
	if err != nil {
		t.Fatalf("Find failed: %v", err)
	}
	pending, ok := result.(PendingEntry)
	if !ok {
		t.Fatalf("expected a pending request, got %+v", result)
	}
	if pending.Request.Client != "client2" {
		t.Errorf("unexpected client: %s", pending.Request.Client)
	}
}

func TestClient_Find_History(t *testing.T) {
	resolvedAt := time.Now().Add(-time.Minute)

	client := newTestClient(t, "test-token", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/pending":
			json.NewEncoder(w).Encode(PendingResponse{})
		case "/api/v1/log":
			json.NewEncoder(w).Encode(HistoryResponse{Entries: []HistoryEntry{
				{Request: RequestRecord{ID: "old-req", Client: "client3"}, Resolution: "approved", ResolvedAt: resolvedAt},
			}})
		}
	})

	result, err := client.Find("old-req")
	if err != nil {
		t.Fatalf("Find failed: %v", err)
	}
	entry, ok := result.(HistoryEntry)
	if !ok {
		t.Fatalf("expected a History entry, got %+v", result)
	}
	if entry.Request.Client != "client3" {
		t.Errorf("unexpected client: %s", entry.Request.Client)
	}
	if entry.Resolution != "approved" {
		t.Errorf("expected approved resolution, got: %s", entry.Resolution)
	}
}

func TestClient_Unauthorized(t *testing.T) {
	client := newTestClient(t, "bad-token", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "unauthorized"})
	})
	_, err := client.Pending()
	if err == nil {
		t.Fatal("expected error for unauthorized")
	}
	if !strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("expected unauthorized error, got: %v", err)
	}
}

// The CLI reads the invoker under the JSON key that the API writes.
func TestPendingEntryDecodesInvokerName(t *testing.T) {
	var req PendingEntry
	if err := json.Unmarshal([]byte(`{"sender_info":{"pid":42,"invoker_name":"gh"}}`), &req); err != nil {
		t.Fatal(err)
	}
	if req.Request.SenderInfo.InvokerName != "gh" || req.Request.SenderInfo.PID != 42 {
		t.Errorf("sender_info = %+v, want invoker gh with pid 42", req.Request.SenderInfo)
	}
}
