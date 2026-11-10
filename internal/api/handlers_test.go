package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/stretchr/testify/require"
)

// mintSession mints a live browser session on auth and returns the matching
// "session=<id>" Cookie header value. Session cookies are independent random IDs
// (not the master token), so tests must obtain one this way rather than reusing
// auth.Token().
func mintSession(t *testing.T, auth *Auth) string {
	t.Helper()
	rr := httptest.NewRecorder()
	if err := auth.SetSessionCookie(rr); err != nil {
		t.Fatalf("failed to mint session: %v", err)
	}
	cookies := rr.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("SetSessionCookie set no cookie")
	}
	return cookies[0].Name + "=" + cookies[0].Value
}

// testHandlers creates handlers with a test auth for use in tests.
func testHandlers(t *testing.T, mgr *approval.Manager) *Handlers {
	t.Helper()
	auth := newTestAuth(t)
	return NewHandlers(mgr, "/path/to/socket", "test-client", auth, false, nil, 0)
}

// Builds the approval manager the api tests share. Its requests wait five
// minutes, so none expires mid-test, and its auto-approve rules last two
// minutes. Each edit adjusts the config before the manager is built.
func newTestManager(edits ...func(*approval.ManagerConfig)) *approval.Manager {
	cfg := approval.ManagerConfig{Timeout: 5 * time.Minute, HistoryMax: 100, AutoApproveDuration: 2 * time.Minute}
	for _, edit := range edits {
		edit(&cfg)
	}
	return approval.NewManager(cfg)
}

// A get_secret request that a test started and that its manager holds pending.
type pendingRequest struct {
	ID string
	// Done receives RequireApproval's error once the request is resolved.
	Done <-chan error
}

// Starts a get_secret request from "test-client" for items on session
// "/session/1" in the background, and returns the channel that receives
// RequireApproval's error.
func startRequest(ctx context.Context, mgr *approval.Manager, items ...approval.ItemInfo) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := mgr.RequireApproval(ctx, items, "/session/1", approval.RequestTypeGetSecret, nil, approval.SenderInfo{Client: "test-client"})
		done <- err
	}()
	return done
}

// Starts a request as startRequest does, and returns once mgr lists it as
// pending. The request is cancelled when the test ends.
func startPending(t *testing.T, mgr *approval.Manager, items ...approval.ItemInfo) pendingRequest {
	t.Helper()
	done := startRequest(t.Context(), mgr, items...)
	var id string
	require.Eventually(t, func() bool {
		if reqs := mgr.List(); len(reqs) > 0 {
			id = reqs[0].ID
		}
		return id != ""
	}, time.Second, 10*time.Millisecond, "request did not appear")
	return pendingRequest{ID: id, Done: done}
}

func TestHandleStatus(t *testing.T) {
	handlers := testHandlers(t, newTestManager())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	rr := httptest.NewRecorder()

	handlers.HandleStatus(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var resp StatusResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !resp.Running {
		t.Error("expected running=true")
	}
	if resp.PendingCount != 0 {
		t.Errorf("expected pending_count=0, got %d", resp.PendingCount)
	}
	// Check new clients field
	if len(resp.Clients) != 1 {
		t.Fatalf("expected 1 client, got %d", len(resp.Clients))
	}
	if resp.Clients[0].Name != "test-client" {
		t.Errorf("expected client name 'test-client', got '%s'", resp.Clients[0].Name)
	}
	if resp.Clients[0].SocketPath != "/path/to/socket" {
		t.Errorf("expected socket_path '/path/to/socket', got '%s'", resp.Clients[0].SocketPath)
	}
	// Check deprecated fields for backward compatibility
	if resp.Client != "test-client" {
		t.Errorf("expected client 'test-client', got '%s'", resp.Client)
	}
	if resp.RemoteSocket != "/path/to/socket" {
		t.Errorf("expected remote_socket '/path/to/socket', got '%s'", resp.RemoteSocket)
	}
}

func TestHandleStatus_WrongMethod(t *testing.T) {
	handlers := testHandlers(t, newTestManager())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/status", nil)
	rr := httptest.NewRecorder()

	handlers.HandleStatus(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

func TestHandlePendingList_Empty(t *testing.T) {
	handlers := testHandlers(t, newTestManager())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/pending", nil)
	rr := httptest.NewRecorder()

	handlers.HandlePendingList(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var resp PendingListResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Requests) != 0 {
		t.Errorf("expected empty requests, got %d", len(resp.Requests))
	}
}

func TestHandlePendingList_WithRequests(t *testing.T) {
	mgr := newTestManager()
	handlers := testHandlers(t, mgr)

	startPending(t, mgr, approval.ItemInfo{Path: "/test/item1"}, approval.ItemInfo{Path: "/test/item2"})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/pending", nil)
	rr := httptest.NewRecorder()

	handlers.HandlePendingList(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var resp PendingListResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(resp.Requests))
	}

	r := resp.Requests[0]
	if r.Client != "test-client" {
		t.Errorf("expected client 'test-client', got '%s'", r.Client)
	}
	if len(r.Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(r.Items))
	}
	if r.Session != "/session/1" {
		t.Errorf("expected session '/session/1', got '%s'", r.Session)
	}
}

func TestHandleApprove_Success(t *testing.T) {
	mgr := newTestManager()
	handlers := testHandlers(t, mgr)

	pending := startPending(t, mgr, approval.ItemInfo{Path: "/test/item"})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/pending/"+pending.ID+"/approve", nil)
	rr := httptest.NewRecorder()

	handlers.HandleApprove(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var resp ActionResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != "approved" {
		t.Errorf("expected status 'approved', got '%s'", resp.Status)
	}

	// Verify the approval unblocked the request
	select {
	case err := <-pending.Done:
		if err != nil {
			t.Errorf("RequireApproval should return nil on approve, got: %v", err)
		}
	case <-time.After(time.Second):
		t.Error("RequireApproval did not unblock")
	}
}

func TestHandleApprove_NotFound(t *testing.T) {
	handlers := testHandlers(t, newTestManager())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/pending/nonexistent-id/approve", nil)
	rr := httptest.NewRecorder()

	handlers.HandleApprove(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rr.Code)
	}

	var resp ErrorResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Error != "request not found or expired" {
		t.Errorf("unexpected error message: %s", resp.Error)
	}
}

func TestHandleApprove_WrongMethod(t *testing.T) {
	handlers := testHandlers(t, newTestManager())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/pending/some-id/approve", nil)
	rr := httptest.NewRecorder()

	handlers.HandleApprove(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

func TestHandleDeny_Success(t *testing.T) {
	mgr := newTestManager()
	handlers := testHandlers(t, mgr)

	pending := startPending(t, mgr, approval.ItemInfo{Path: "/test/item"})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/pending/"+pending.ID+"/deny", nil)
	rr := httptest.NewRecorder()

	handlers.HandleDeny(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var resp ActionResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != "denied" {
		t.Errorf("expected status 'denied', got '%s'", resp.Status)
	}

	// Verify the denial unblocked the request
	select {
	case err := <-pending.Done:
		if err != approval.ErrDenied {
			t.Errorf("RequireApproval should return ErrDenied, got: %v", err)
		}
	case <-time.After(time.Second):
		t.Error("RequireApproval did not unblock")
	}
}

func TestHandleCancel_Success(t *testing.T) {
	mgr := newTestManager()
	handlers := testHandlers(t, mgr)

	pending := startPending(t, mgr, approval.ItemInfo{Path: "/test/item"})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/pending/"+pending.ID+"/cancel", nil)
	rr := httptest.NewRecorder()

	handlers.HandleCancel(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var resp ActionResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != "cancelled" {
		t.Errorf("expected status 'cancelled', got '%s'", resp.Status)
	}
}

func TestHandleCancel_NotFound(t *testing.T) {
	handlers := testHandlers(t, newTestManager())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/pending/nonexistent-id/cancel", nil)
	rr := httptest.NewRecorder()

	handlers.HandleCancel(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rr.Code)
	}
}

func TestHandleCancel_WrongMethod(t *testing.T) {
	handlers := testHandlers(t, newTestManager())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/pending/some-id/cancel", nil)
	rr := httptest.NewRecorder()

	handlers.HandleCancel(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

func TestHandleLog_Empty(t *testing.T) {
	handlers := testHandlers(t, newTestManager())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/log", nil)
	rr := httptest.NewRecorder()

	handlers.HandleLog(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var resp HistoryResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// No history yet, should return empty
	if len(resp.Entries) != 0 {
		t.Errorf("expected empty entries, got %d", len(resp.Entries))
	}
}

func TestHandleLog_WithHistory(t *testing.T) {
	mgr := newTestManager()
	handlers := testHandlers(t, mgr)

	pending := startPending(t, mgr, approval.ItemInfo{Path: "/test/item", Label: "Test Item"})

	// Approve the request
	mgr.Approve(pending.ID)
	<-pending.Done

	// Give time for history to be recorded
	time.Sleep(50 * time.Millisecond)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/log", nil)
	rr := httptest.NewRecorder()

	handlers.HandleLog(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var resp HistoryResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(resp.Entries) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(resp.Entries))
	}

	entry := resp.Entries[0]
	if entry.Resolution != "approved" {
		t.Errorf("expected resolution 'approved', got '%s'", entry.Resolution)
	}
	if entry.Request.ID != pending.ID {
		t.Errorf("expected request ID %s, got %s", pending.ID, entry.Request.ID)
	}
	if entry.Request.Client != "test-client" {
		t.Errorf("expected client 'test-client', got '%s'", entry.Request.Client)
	}
}

func TestExtractRequestID(t *testing.T) {
	tests := []struct {
		path     string
		prefix   string
		suffix   string
		expected string
	}{
		{"/api/v1/pending/abc123/approve", "/api/v1/pending/", "/approve", "abc123"},
		{"/api/v1/pending/abc123/deny", "/api/v1/pending/", "/deny", "abc123"},
		{"/api/v1/pending/550e8400-e29b-41d4-a716-446655440000/approve", "/api/v1/pending/", "/approve", "550e8400-e29b-41d4-a716-446655440000"},
		{"/wrong/prefix/abc123/approve", "/api/v1/pending/", "/approve", ""},
		{"/api/v1/pending/abc123/wrong", "/api/v1/pending/", "/approve", ""},
		{"/api/v1/pending//approve", "/api/v1/pending/", "/approve", ""},
		{"/api/v1/pending/a/b/c/approve", "/api/v1/pending/", "/approve", ""}, // contains slash
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			result := extractRequestID(tc.path, tc.prefix, tc.suffix)
			if result != tc.expected {
				t.Errorf("extractRequestID(%q, %q, %q) = %q, want %q",
					tc.path, tc.prefix, tc.suffix, result, tc.expected)
			}
		})
	}
}
