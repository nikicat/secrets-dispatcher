package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/apiclient"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/nikicat/secrets-dispatcher/internal/procutil"
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

	const token config.AuthToken = "test-token"
	client := newTestClient(t, token, func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/pending", r.URL.Path)
		got, err := config.ParseAuthorization(r.Header)
		assert.NoError(t, err)
		assert.Equal(t, token, got)
		json.NewEncoder(w).Encode(PendingResponse{Requests: requests})
	})
	result, err := client.Pending(t.Context())
	require.NoError(t, err)

	require.Len(t, result, 1)
	assert.Equal(t, approval.RequestID("req-123"), result[0].Request.ID)
}

func TestClient_History(t *testing.T) {
	entries := []HistoryEntry{
		{
			Request:    RequestRecord{ID: "req-456", Client: "user@host"},
			Resolution: approval.ResolutionApproved,
			ResolvedAt: time.Now(),
		},
	}

	client := newTestClient(t, "test-token", func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/log", r.URL.Path)
		json.NewEncoder(w).Encode(HistoryResponse{Entries: entries})
	})
	result, err := client.History(t.Context())
	require.NoError(t, err)

	require.Len(t, result, 1)
	assert.Equal(t, approval.ResolutionApproved, result[0].Resolution)
}

// Fakes the API with these requests pending; it accepts only the decision POSTed
// for the resolved request and fails on any other path.
type pendingAPI struct {
	ids      []approval.RequestID
	resolved approval.RequestID
	decision Decision
}

func (p pendingAPI) client(t *testing.T) *Client {
	t.Helper()
	return newTestClient(t, "test-token", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/pending":
			requests := make([]PendingEntry, len(p.ids))
			for i, id := range p.ids {
				requests[i].Request.ID = id
			}
			json.NewEncoder(w).Encode(PendingResponse{Requests: requests})
		case p.decision != nil && r.URL.Path == "/api/v1/pending/"+string(p.resolved)+"/"+string(p.decision.action()):
			assert.Equal(t, http.MethodPost, r.Method)
		default:
			assert.Fail(t, "unexpected path", r.URL.Path)
		}
	})
}

func TestClient_Approve(t *testing.T) {
	client := pendingAPI{ids: []approval.RequestID{"abc-123-def"}, resolved: "abc-123-def", decision: Approve{}}.client(t)
	id, err := client.Resolve(t.Context(), "abc-123-def", Approve{})
	require.NoError(t, err)
	assert.Equal(t, approval.RequestID("abc-123-def"), id)
}

func TestClient_Deny(t *testing.T) {
	client := pendingAPI{ids: []approval.RequestID{"xyz-789"}, resolved: "xyz-789", decision: Deny{}}.client(t)
	id, err := client.Resolve(t.Context(), "xyz-789", Deny{})
	require.NoError(t, err)
	assert.Equal(t, approval.RequestID("xyz-789"), id)
}

func TestClient_PartialID_ExactMatch(t *testing.T) {
	client := pendingAPI{ids: []approval.RequestID{"abc", "abcdef"}, resolved: "abc", decision: Approve{}}.client(t)
	// "abc" should match exactly, not be ambiguous with "abcdef"
	id, err := client.Resolve(t.Context(), "abc", Approve{})
	require.NoError(t, err)
	assert.Equal(t, approval.RequestID("abc"), id)
}

func TestClient_PartialID_PrefixMatch(t *testing.T) {
	client := pendingAPI{ids: []approval.RequestID{"abc-123-456", "def-789"}, resolved: "abc-123-456", decision: Approve{}}.client(t)
	id, err := client.Resolve(t.Context(), "abc", Approve{})
	require.NoError(t, err)
	assert.Equal(t, approval.RequestID("abc-123-456"), id)
}

func TestClient_PartialID_Ambiguous(t *testing.T) {
	client := pendingAPI{ids: []approval.RequestID{"abc-123", "abc-456"}}.client(t)
	_, err := client.Resolve(t.Context(), "abc", Approve{})
	assert.ErrorContains(t, err, "ambiguous")
}

func TestClient_PartialID_NotFound(t *testing.T) {
	client := pendingAPI{ids: []approval.RequestID{"xyz-123"}}.client(t)
	_, err := client.Resolve(t.Context(), "abc", Approve{})
	assert.ErrorContains(t, err, "no request found")
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

	result, err := client.Find(t.Context(), "req-222")
	require.NoError(t, err)
	pending, ok := result.(PendingEntry)
	require.True(t, ok, "expected a pending request, got %+v", result)
	assert.Equal(t, approval.ClientName("client2"), pending.Request.Client)
}

func TestClient_Find_History(t *testing.T) {
	resolvedAt := time.Now().Add(-time.Minute)

	client := newTestClient(t, "test-token", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/pending":
			json.NewEncoder(w).Encode(PendingResponse{})
		case "/api/v1/log":
			json.NewEncoder(w).Encode(HistoryResponse{Entries: []HistoryEntry{
				{Request: RequestRecord{ID: "old-req", Client: "client3"}, Resolution: approval.ResolutionApproved, ResolvedAt: resolvedAt},
			}})
		}
	})

	result, err := client.Find(t.Context(), "old-req")
	require.NoError(t, err)
	entry, ok := result.(HistoryEntry)
	require.True(t, ok, "expected a History entry, got %+v", result)
	assert.Equal(t, approval.ClientName("client3"), entry.Request.Client)
	assert.Equal(t, approval.ResolutionApproved, entry.Resolution)
}

func TestClient_Unauthorized(t *testing.T) {
	client := newTestClient(t, "bad-token", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(apiclient.ErrorResponse{Error: "unauthorized"})
	})
	_, err := client.Pending(t.Context())
	assert.ErrorContains(t, err, "unauthorized")
}

// The CLI reads the invoker under the JSON key that the API writes.
func TestPendingEntryDecodesInvokerName(t *testing.T) {
	var req PendingEntry
	require.NoError(t, json.Unmarshal([]byte(`{"sender_info":{"pid":42,"invoker_name":"gh"}}`), &req))
	assert.Equal(t, "gh", req.Request.SenderInfo.InvokerName)
	assert.Equal(t, procutil.PID(42), req.Request.SenderInfo.PID)
}
