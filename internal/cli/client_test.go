package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nikicat/secrets-dispatcher/internal/config"
)

// Closes the server when the test ends.
func newTestClient(t *testing.T, token config.AuthToken, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return NewClient(config.ListenAddr(server.Listener.Addr().String()), token)
}

func TestClient_List(t *testing.T) {
	requests := []PendingRequest{
		{
			ID:        "req-123",
			Client:    "user@host",
			Items:     []ItemInfo{{Label: "Secret1", Path: "/org/secrets/1"}},
			CreatedAt: time.Now(),
			ExpiresAt: time.Now().Add(5 * time.Minute),
			Type:      "get_secret",
		},
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
	result, err := client.List()
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}

	if len(result) != 1 {
		t.Fatalf("expected 1 request, got %d", len(result))
	}
	if result[0].ID != "req-123" {
		t.Errorf("unexpected ID: %s", result[0].ID)
	}
}

func TestClient_History(t *testing.T) {
	entries := []HistoryEntry{
		{
			Request:    PendingRequest{ID: "req-456", Client: "user@host"},
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
				Requests: []PendingRequest{{ID: "abc-123-def"}},
			})
		case "/api/v1/pending/abc-123-def/approve":
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			json.NewEncoder(w).Encode(ActionResponse{Status: "approved"})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	})
	err := client.Approve("abc-123-def")
	if err != nil {
		t.Fatalf("Approve failed: %v", err)
	}
}

func TestClient_Deny(t *testing.T) {
	client := newTestClient(t, "test-token", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/pending":
			json.NewEncoder(w).Encode(PendingResponse{
				Requests: []PendingRequest{{ID: "xyz-789"}},
			})
		case "/api/v1/pending/xyz-789/deny":
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			json.NewEncoder(w).Encode(ActionResponse{Status: "denied"})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	})
	err := client.Deny("xyz-789")
	if err != nil {
		t.Fatalf("Deny failed: %v", err)
	}
}

func TestClient_PartialID_ExactMatch(t *testing.T) {
	client := newTestClient(t, "test-token", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/pending":
			json.NewEncoder(w).Encode(PendingResponse{
				Requests: []PendingRequest{
					{ID: "abc"},
					{ID: "abcdef"},
				},
			})
		case "/api/v1/pending/abc/approve":
			json.NewEncoder(w).Encode(ActionResponse{Status: "approved"})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	})
	// "abc" should match exactly, not be ambiguous with "abcdef"
	err := client.Approve("abc")
	if err != nil {
		t.Fatalf("Approve with exact match failed: %v", err)
	}
}

func TestClient_PartialID_PrefixMatch(t *testing.T) {
	client := newTestClient(t, "test-token", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/pending":
			json.NewEncoder(w).Encode(PendingResponse{
				Requests: []PendingRequest{
					{ID: "abc-123-456"},
					{ID: "def-789"},
				},
			})
		case "/api/v1/pending/abc-123-456/approve":
			json.NewEncoder(w).Encode(ActionResponse{Status: "approved"})
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	})
	err := client.Approve("abc")
	if err != nil {
		t.Fatalf("Approve with prefix failed: %v", err)
	}
}

func TestClient_PartialID_Ambiguous(t *testing.T) {
	client := newTestClient(t, "test-token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(PendingResponse{
			Requests: []PendingRequest{
				{ID: "abc-123"},
				{ID: "abc-456"},
			},
		})
	})
	err := client.Approve("abc")
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
			Requests: []PendingRequest{{ID: "xyz-123"}},
		})
	})
	err := client.Approve("abc")
	if err == nil {
		t.Fatal("expected error for not found")
	}
	if !strings.Contains(err.Error(), "no request found") {
		t.Errorf("expected not found error, got: %v", err)
	}
}

func TestClient_Show_Pending(t *testing.T) {
	requests := []PendingRequest{
		{ID: "req-111", Client: "client1"},
		{ID: "req-222", Client: "client2"},
	}

	client := newTestClient(t, "test-token", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/pending":
			json.NewEncoder(w).Encode(PendingResponse{Requests: requests})
		case "/api/v1/log":
			json.NewEncoder(w).Encode(HistoryResponse{})
		}
	})

	result, err := client.Show("req-222")
	if err != nil {
		t.Fatalf("Show failed: %v", err)
	}
	if result.Request.Client != "client2" {
		t.Errorf("unexpected client: %s", result.Request.Client)
	}
	if result.Resolution != "" {
		t.Errorf("expected empty resolution for pending, got: %s", result.Resolution)
	}
}

func TestClient_Show_History(t *testing.T) {
	resolvedAt := time.Now().Add(-time.Minute)

	client := newTestClient(t, "test-token", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/pending":
			json.NewEncoder(w).Encode(PendingResponse{})
		case "/api/v1/log":
			json.NewEncoder(w).Encode(HistoryResponse{Entries: []HistoryEntry{
				{Request: PendingRequest{ID: "old-req", Client: "client3"}, Resolution: "approved", ResolvedAt: resolvedAt},
			}})
		}
	})

	result, err := client.Show("old-req")
	if err != nil {
		t.Fatalf("Show failed: %v", err)
	}
	if result.Request.Client != "client3" {
		t.Errorf("unexpected client: %s", result.Request.Client)
	}
	if result.Resolution != "approved" {
		t.Errorf("expected approved resolution, got: %s", result.Resolution)
	}
}

func TestClient_Unauthorized(t *testing.T) {
	client := newTestClient(t, "bad-token", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(ErrorResponse{Error: "unauthorized"})
	})
	_, err := client.List()
	if err == nil {
		t.Fatal("expected error for unauthorized")
	}
	if !strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("expected unauthorized error, got: %v", err)
	}
}
