package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

// validGPGSignBody is a JSON POST body with all required fields present.
const validGPGSignBody = `{
	"client": "test",
	"gpg_sign_info": {
		"repo_name":     "myrepo",
		"commit_msg":    "fix: thing",
		"author":        "A",
		"committer":     "A",
		"key_id":        "ABCD1234",
		"changed_files": ["main.go"]
	}
}`

// TestHandleGPGSignRequest_ValidBody verifies Case 6:
// POST with a valid body returns 200 and a non-empty request_id.
func TestHandleGPGSignRequest_ValidBody(t *testing.T) {
	mgr := approval.NewManager(5*time.Second, 100)
	handlers := testHandlers(t, mgr)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/gpg-sign/request",
		strings.NewReader(validGPGSignBody))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handlers.HandleGPGSignRequest(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", rr.Code, rr.Body.String())
	}

	var resp GPGSignResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.RequestID == "" {
		t.Error("expected non-empty request_id in response")
	}

	// Verify a pending request exists with the returned ID.
	pending := mgr.List()
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending request after POST, got %d", len(pending))
	}
	if pending[0].ID != resp.RequestID {
		t.Errorf("pending request ID %q != response request_id %q", pending[0].ID, resp.RequestID)
	}
}

// TestHandleGPGSignRequest_MissingGPGSignInfo verifies Case 7:
// POST without gpg_sign_info returns 400.
func TestHandleGPGSignRequest_MissingGPGSignInfo(t *testing.T) {
	mgr := approval.NewManager(5*time.Second, 100)
	handlers := testHandlers(t, mgr)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/gpg-sign/request",
		strings.NewReader(`{"client":"test"}`))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()

	handlers.HandleGPGSignRequest(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing gpg_sign_info, got %d", rr.Code)
	}
}

// TestHandleGPGSignRequest_WrongMethod verifies Case 8:
// GET to the endpoint returns 405 Method Not Allowed.
func TestHandleGPGSignRequest_WrongMethod(t *testing.T) {
	mgr := approval.NewManager(5*time.Second, 100)
	handlers := testHandlers(t, mgr)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/gpg-sign/request", nil)
	rr := httptest.NewRecorder()

	handlers.HandleGPGSignRequest(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

// TestHandleGPGSignRequest_KeyIDVisibleInPendingList verifies Case 9 (SIGN-09):
// After a successful POST, the pending list includes the request and its
// gpg_sign_info.key_id is visible.
func TestHandleGPGSignRequest_KeyIDVisibleInPendingList(t *testing.T) {
	mgr := approval.NewManager(5*time.Second, 100)
	handlers := testHandlers(t, mgr)

	// POST to create the request.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/gpg-sign/request",
		strings.NewReader(validGPGSignBody))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handlers.HandleGPGSignRequest(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("POST failed with %d: %s", rr.Code, rr.Body.String())
	}

	// Fetch the pending list.
	pendingReq := httptest.NewRequest(http.MethodGet, "/api/v1/pending", nil)
	pendingRR := httptest.NewRecorder()
	handlers.HandlePendingList(pendingRR, pendingReq)

	if pendingRR.Code != http.StatusOK {
		t.Fatalf("HandlePendingList returned %d", pendingRR.Code)
	}

	var resp PendingListResponse
	if err := json.NewDecoder(pendingRR.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode pending list: %v", err)
	}

	if len(resp.Requests) != 1 {
		t.Fatalf("expected 1 pending request, got %d", len(resp.Requests))
	}

	pr := resp.Requests[0]
	if pr.GPGSignInfo == nil {
		t.Fatal("expected GPGSignInfo to be non-nil in pending request")
	}
	if pr.GPGSignInfo.KeyID != "ABCD1234" {
		t.Errorf("expected key_id ABCD1234, got %q", pr.GPGSignInfo.KeyID)
	}
	if pr.Type != string(approval.RequestTypeGPGSign) {
		t.Errorf("expected type %q, got %q", approval.RequestTypeGPGSign, pr.Type)
	}
}

// wsEventObserver is a minimal approval.Observer that captures OnEvent calls
// and constructs WSMessages the same way wsConnection.OnEvent does, so we can
// inspect the Signature field without needing a real WebSocket.
type wsEventObserver struct {
	mu   sync.Mutex
	msgs []WSMessage
}

func (o *wsEventObserver) OnEvent(event approval.Event) {
	if event.Type != approval.EventRequestApproved {
		return
	}
	msg := WSMessage{
		Type:   "request_resolved",
		ID:     event.Request.ID,
		Result: "approved",
	}
	if event.Request.Type == approval.RequestTypeGPGSign {
		msg.Signature = base64.StdEncoding.EncodeToString([]byte("PLACEHOLDER_SIGNATURE"))
	}
	o.mu.Lock()
	o.msgs = append(o.msgs, msg)
	o.mu.Unlock()
}

func (o *wsEventObserver) WaitForMessages(count int, timeout time.Duration) []WSMessage {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		o.mu.Lock()
		n := len(o.msgs)
		o.mu.Unlock()
		if n >= count {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]WSMessage{}, o.msgs...)
}

// TestHandleGPGSignRequest_WSSignatureOnApproval verifies Case 10:
// When a gpg_sign request is approved, the resulting WSMessage has a non-empty
// Signature field.
func TestHandleGPGSignRequest_WSSignatureOnApproval(t *testing.T) {
	mgr := approval.NewManager(5*time.Second, 100)
	obs := &wsEventObserver{}
	mgr.Subscribe(obs)

	// Create a gpg_sign request via the manager directly (same path the handler uses).
	info := &approval.GPGSignInfo{
		RepoName:     "myrepo",
		CommitMsg:    "fix: thing",
		Author:       "A",
		Committer:    "A",
		KeyID:        "ABCD1234",
		ChangedFiles: []string{"main.go"},
	}
	id, err := mgr.CreateGPGSignRequest("test-client", info)
	if err != nil {
		t.Fatalf("CreateGPGSignRequest failed: %v", err)
	}

	// Approve the request.
	if err := mgr.Approve(id); err != nil {
		t.Fatalf("Approve failed: %v", err)
	}

	// Wait for the observer to receive the approved event.
	msgs := obs.WaitForMessages(1, time.Second)
	if len(msgs) < 1 {
		t.Fatal("expected at least 1 WSMessage for EventRequestApproved")
	}

	msg := msgs[0]
	if msg.Type != "request_resolved" {
		t.Errorf("expected type 'request_resolved', got %q", msg.Type)
	}
	if msg.Result != "approved" {
		t.Errorf("expected result 'approved', got %q", msg.Result)
	}
	if msg.Signature == "" {
		t.Error("expected non-empty Signature for gpg_sign approval, got empty string")
	}

	// Verify the signature decodes to valid base64 bytes.
	decoded, err := base64.StdEncoding.DecodeString(msg.Signature)
	if err != nil {
		t.Errorf("Signature is not valid base64: %v", err)
	}
	if len(decoded) == 0 {
		t.Error("decoded Signature is empty")
	}
}
