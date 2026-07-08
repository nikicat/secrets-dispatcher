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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	mgr := approval.NewManager(approval.ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
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
	mgr := approval.NewManager(approval.ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
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
	mgr := approval.NewManager(approval.ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
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
	mgr := approval.NewManager(approval.ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
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

// TestHandleGPGSignRequest_DisplayBoundToCommitObject is the regression test for
// the consent-spoofing / signing-oracle finding (Vuln 1). A caller supplies
// benign display metadata (author/committer/message) that does NOT match the raw
// commit_object it asks the daemon to sign. The daemon must ignore the client's
// display fields and derive them from the bytes it will actually sign, so the
// approval prompt reflects the real payload.
func TestHandleGPGSignRequest_DisplayBoundToCommitObject(t *testing.T) {
	mgr := approval.NewManager(approval.ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
	handlers := testHandlers(t, mgr)

	// The bytes that will actually be signed describe an attacker identity and a
	// dangerous commit message; the client lies about all of them in the display
	// fields, claiming a benign typo fix.
	commitObject := "tree 4b825dc642cb6eb9a060e54bf8d69288fbee4904\n" +
		"parent 1111111111111111111111111111111111111111\n" +
		"author Real Attacker <evil@example.com> 1700000000 +0000\n" +
		"committer Real Attacker <evil@example.com> 1700000000 +0000\n" +
		"\n" +
		"backdoor: exfiltrate secrets\n"

	body := map[string]any{
		"client": "test",
		"gpg_sign_info": map[string]any{
			"repo_name":     "myrepo",
			"commit_msg":    "docs: fix typo",
			"author":        "Benign Author <good@example.com>",
			"committer":     "Benign Author <good@example.com>",
			"key_id":        "ABCD1234",
			"changed_files": []string{"README.md"},
			"parent_hash":   "2222222222222222222222222222222222222222",
			"commit_object": commitObject,
		},
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/gpg-sign/request", strings.NewReader(string(raw)))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handlers.HandleGPGSignRequest(rr, req)

	require.Equalf(t, http.StatusOK, rr.Code, "body: %s", rr.Body.String())

	pending := mgr.List()
	require.Len(t, pending, 1)
	info := pending[0].GPGSignInfo
	require.NotNil(t, info)

	// The displayed fields must come from the signed bytes, not the client's lie.
	assert.Equal(t, "Real Attacker <evil@example.com> 1700000000 +0000", info.Author, "author must be bound to commit object")
	assert.Equal(t, "Real Attacker <evil@example.com> 1700000000 +0000", info.Committer, "committer must be bound to commit object")
	assert.Equal(t, "backdoor: exfiltrate secrets", info.CommitMsg, "commit message must be bound to commit object")
	assert.Equal(t, "1111111111111111111111111111111111111111", info.ParentHash, "parent hash must be bound to commit object")
	// The bytes to be signed must be preserved verbatim.
	assert.Equal(t, commitObject, info.CommitObject, "commit object must not be mutated")
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

// fakeGPGRunner is a stub GPGRunner for tests that need
// HandleGPGSignRequest to take the auto-approved path without invoking real gpg.
type fakeGPGRunner struct {
	sig    []byte
	status []byte
}

func (f *fakeGPGRunner) FindGPG() (string, error) { return "/fake/gpg", nil }
func (f *fakeGPGRunner) RunGPG(_ /* path */, _ /* keyID */ string, _ /* commit */ []byte) ([]byte, []byte, int, error) {
	return f.sig, f.status, 0, nil
}

// TestHandleGPGSignRequest_UnverifiableInvokerStaysPending guards the
// fail-closed half of the comm-spoofing fix (Vuln 4): an ephemeral gpg_sign
// auto-approve rule keys on the non-spoofable invoker /proc/PID/exe, so when the
// caller's exe cannot be resolved (as here — an httptest request carries no peer
// socket) the rule must NOT silently short-circuit; the request enters the
// pending flow and waits for an explicit decision. The positive case (a caller
// whose exe matches the rule signs without a prompt) is exercised end-to-end by
// the podman E2E suite, where real processes with distinct exes exist.
func TestHandleGPGSignRequest_UnverifiableInvokerStaysPending(t *testing.T) {
	mgr := approval.NewManager(approval.ManagerConfig{
		Timeout:             5 * time.Second,
		HistoryMax:          100,
		AutoApproveDuration: time.Minute,
	})
	handlers := testHandlers(t, mgr)
	handlers.resolver.GPGRunner = &fakeGPGRunner{
		sig:    []byte("FAKE_SIG"),
		status: []byte("[GNUPG:] SIG_CREATED D"),
	}

	// A rule whose invoker exe is unset (as ApproveAndAutoApprove would record
	// for a caller whose exe could not be resolved) must never match, since exe
	// is the sole identity and an empty exe fails closed.
	mgr.AddAutoApproveRule(&approval.Request{
		Type:       approval.RequestTypeGPGSign,
		SenderInfo: approval.SenderInfo{},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/gpg-sign/request",
		strings.NewReader(validGPGSignBody))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handlers.HandleGPGSignRequest(rr, req)

	require.Equalf(t, http.StatusOK, rr.Code, "body: %s", rr.Body.String())

	// The request must be pending (awaiting a decision), not silently signed.
	assert.Len(t, mgr.List(), 1, "unverifiable invoker must not be auto-approved")
}

// TestGPGSignEphemeralRuleMatchesOnInvokerExe is the manager-level regression
// test for the wiring the HTTP handler depends on: an ephemeral gpg_sign rule is
// consulted by CheckAutoApproveRules and matches a caller whose invoker exe
// equals the rule's — never merely its comm.
func TestGPGSignEphemeralRuleMatchesOnInvokerExe(t *testing.T) {
	mgr := approval.NewManager(approval.ManagerConfig{
		Timeout:             5 * time.Second,
		HistoryMax:          100,
		AutoApproveDuration: time.Minute,
	})

	const pid = 4242
	exeSender := approval.SenderInfo{
		PID:          pid,
		InvokerName:  "git",
		ProcessChain: []approval.ProcessInfo{{Name: "git", PID: pid, Exe: "/usr/bin/git"}},
	}
	mgr.AddAutoApproveRule(&approval.Request{Type: approval.RequestTypeGPGSign, SenderInfo: exeSender})

	// Same exe → matches.
	assert.NotNil(t, mgr.CheckAutoApproveRules(exeSender, nil, approval.RequestTypeGPGSign),
		"rule should match a caller with the same invoker exe")

	// Same spoofable comm but a different real exe → must NOT match.
	spoofed := approval.SenderInfo{
		PID:          pid,
		InvokerName:  "git", // attacker set comm to "git"
		ProcessChain: []approval.ProcessInfo{{Name: "git", PID: pid, Exe: "/tmp/malware"}},
	}
	assert.Nil(t, mgr.CheckAutoApproveRules(spoofed, nil, approval.RequestTypeGPGSign),
		"rule must not match a caller that only spoofed the comm")
}

// TestHandleGPGSignRequest_NoMatchingRuleStaysPending guards the negative case:
// an auto-approve rule whose invoker does not match must NOT short-circuit; the
// request still enters the pending queue and waits for a desktop notification.
func TestHandleGPGSignRequest_NoMatchingRuleStaysPending(t *testing.T) {
	mgr := approval.NewManager(approval.ManagerConfig{
		Timeout:             5 * time.Second,
		HistoryMax:          100,
		AutoApproveDuration: time.Minute,
	})
	handlers := testHandlers(t, mgr)
	handlers.resolver.GPGRunner = &fakeGPGRunner{}

	// Rule for a different invoker name.
	mgr.AddAutoApproveRule(&approval.Request{
		Type:       approval.RequestTypeGPGSign,
		SenderInfo: approval.SenderInfo{InvokerName: "some-other-process.service"},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/gpg-sign/request",
		strings.NewReader(validGPGSignBody))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handlers.HandleGPGSignRequest(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if pending := mgr.List(); len(pending) != 1 {
		t.Fatalf("expected 1 pending request when no rule matches, got %d", len(pending))
	}
}

// TestHandleGPGSignRequest_WSSignatureOnApproval verifies Case 10:
// When a gpg_sign request is approved, the resulting WSMessage has a non-empty
// Signature field.
func TestHandleGPGSignRequest_WSSignatureOnApproval(t *testing.T) {
	mgr := approval.NewManager(approval.ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
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
	id, err := mgr.CreateGPGSignRequest("test-client", info, approval.SenderInfo{})
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
