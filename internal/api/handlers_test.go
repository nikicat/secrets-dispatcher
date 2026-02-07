package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

// testHandlers creates handlers with a test auth for use in tests.
func testHandlers(t *testing.T, mgr *approval.Manager) *Handlers {
	t.Helper()
	tmpDir := t.TempDir()
	auth, err := NewAuth(tmpDir)
	if err != nil {
		t.Fatalf("failed to create auth: %v", err)
	}
	return NewHandlers(mgr, "/path/to/socket", "test-client", auth)
}

func TestHandleStatus(t *testing.T) {
	mgr := approval.NewManager(5 * time.Minute)
	handlers := testHandlers(t, mgr)

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
	mgr := approval.NewManager(5 * time.Minute)
	handlers := testHandlers(t, mgr)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/status", nil)
	rr := httptest.NewRecorder()

	handlers.HandleStatus(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

func TestHandlePendingList_Empty(t *testing.T) {
	mgr := approval.NewManager(5 * time.Minute)
	handlers := testHandlers(t, mgr)

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
	mgr := approval.NewManager(5 * time.Minute)
	handlers := testHandlers(t, mgr)

	// Start a pending request
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		mgr.RequireApproval(ctx, "test-client", []string{"/test/item1", "/test/item2"}, "/session/42")
	}()

	// Wait for request to appear
	for i := 0; i < 100; i++ {
		if mgr.PendingCount() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

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
	if r.Session != "/session/42" {
		t.Errorf("expected session '/session/42', got '%s'", r.Session)
	}
}

func TestHandleApprove_Success(t *testing.T) {
	mgr := approval.NewManager(5 * time.Minute)
	handlers := testHandlers(t, mgr)

	// Start a pending request
	done := make(chan error, 1)
	go func() {
		done <- mgr.RequireApproval(context.Background(), "test-client", []string{"/test/item"}, "/session/1")
	}()

	// Wait for request to appear
	var reqID string
	for i := 0; i < 100; i++ {
		reqs := mgr.List()
		if len(reqs) > 0 {
			reqID = reqs[0].ID
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if reqID == "" {
		t.Fatal("request did not appear")
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/pending/"+reqID+"/approve", nil)
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
	case err := <-done:
		if err != nil {
			t.Errorf("RequireApproval should return nil on approve, got: %v", err)
		}
	case <-time.After(time.Second):
		t.Error("RequireApproval did not unblock")
	}
}

func TestHandleApprove_NotFound(t *testing.T) {
	mgr := approval.NewManager(5 * time.Minute)
	handlers := testHandlers(t, mgr)

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
	mgr := approval.NewManager(5 * time.Minute)
	handlers := testHandlers(t, mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/pending/some-id/approve", nil)
	rr := httptest.NewRecorder()

	handlers.HandleApprove(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", rr.Code)
	}
}

func TestHandleDeny_Success(t *testing.T) {
	mgr := approval.NewManager(5 * time.Minute)
	handlers := testHandlers(t, mgr)

	// Start a pending request
	done := make(chan error, 1)
	go func() {
		done <- mgr.RequireApproval(context.Background(), "test-client", []string{"/test/item"}, "/session/1")
	}()

	// Wait for request to appear
	var reqID string
	for i := 0; i < 100; i++ {
		reqs := mgr.List()
		if len(reqs) > 0 {
			reqID = reqs[0].ID
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if reqID == "" {
		t.Fatal("request did not appear")
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/pending/"+reqID+"/deny", nil)
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
	case err := <-done:
		if err != approval.ErrDenied {
			t.Errorf("RequireApproval should return ErrDenied, got: %v", err)
		}
	case <-time.After(time.Second):
		t.Error("RequireApproval did not unblock")
	}
}

func TestHandleLog(t *testing.T) {
	mgr := approval.NewManager(5 * time.Minute)
	handlers := testHandlers(t, mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/log", nil)
	rr := httptest.NewRecorder()

	handlers.HandleLog(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	var resp LogResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Log retrieval not implemented yet, should return empty
	if len(resp.Entries) != 0 {
		t.Errorf("expected empty entries, got %d", len(resp.Entries))
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
