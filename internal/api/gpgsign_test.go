package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/gpgsign"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A JSON request body whose display fields agree with its commit_object.
const validGPGSignBody = `{
	"gpg_sign_info": {
		"repo_name":     "myrepo",
		"commit_msg":    "fix: thing",
		"author":        "A",
		"committer":     "A",
		"key_id":        "ABCD1234",
		"changed_files": ["main.go"],
		"commit_object": "tree 4b825dc642cb6eb9a060e54bf8d69288fbee4904\nauthor A\ncommitter A\n\nfix: thing\n"
	}
}`

// Posts the given body to the gpg-sign request handler and returns its response.
func postGPGSign(t *testing.T, h *Handlers, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/gpg-sign/request", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.HandleGPGSignRequest(rr, req)
	return rr
}

// Returns the signing context of the given request, failing the test when it
// is not a gpg_sign request.
func requireGPGSign(t *testing.T, req *approval.Request) *approval.GPGSignInfo {
	t.Helper()
	info, ok := req.GPGSign()
	require.True(t, ok, "not a gpg_sign request")
	return info
}

// Returns the JSON request body that asks to sign the given object and context.
func gpgSignBody(t *testing.T, info approval.GPGSignInfo) string {
	t.Helper()
	raw, err := json.Marshal(gpgsign.SignRequest{GPGSignInfo: &info})
	require.NoError(t, err)
	return string(raw)
}

// The response carries the ID of the new pending request.
func TestHandleGPGSignRequest_ValidBody(t *testing.T) {
	mgr := newTestManager()
	handlers := testHandlers(t, mgr)

	rr := postGPGSign(t, handlers, validGPGSignBody)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d — body: %s", rr.Code, rr.Body.String())
	}

	var resp gpgsign.SignResponse
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

func TestHandleGPGSignRequest_MissingGPGSignInfo(t *testing.T) {
	handlers := testHandlers(t, newTestManager())

	rr := postGPGSign(t, handlers, `{}`)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing gpg_sign_info, got %d", rr.Code)
	}
}

// TestHandleGPGSignRequest_MissingCommitObject: a request without the raw
// commit bytes has nothing to sign and nothing to bind the approval display
// to, so it must fail loudly with 400 instead of rendering blank fields.
func TestHandleGPGSignRequest_MissingCommitObject(t *testing.T) {
	mgr := newTestManager()
	handlers := testHandlers(t, mgr)

	rr := postGPGSign(t, handlers, gpgSignBody(t, approval.GPGSignInfo{
		RepoName: "myrepo",
		KeyID:    "ABCD1234",
		Signed: approval.SignedPayload{
			Message: "fix: thing",
			Signer:  "A",
			Details: approval.CommitDetails{Committer: "A"},
		},
	}))

	assert.Equal(t, http.StatusBadRequest, rr.Code, "empty commit_object must be rejected")
	assert.Empty(t, mgr.List(), "no pending request may be created")
}

// A commit object the daemon cannot parse in full is refused with 400, so the
// prompt never shows a truncated message.
func TestHandleGPGSignRequest_OversizedLineRejected(t *testing.T) {
	mgr := newTestManager()
	handlers := testHandlers(t, mgr)

	object, err := json.Marshal("tree 4b825dc642cb6eb9a060e54bf8d69288fbee4904\nauthor A\n\n" + strings.Repeat("a", 2<<20) + "\n")
	require.NoError(t, err)
	rr := postGPGSign(t, handlers, `{"gpg_sign_info":{"repo_name":"myrepo","key_id":"ABCD1234","commit_object":`+string(object)+`}}`)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Empty(t, mgr.List(), "no pending request may be created")
}

func TestHandleGPGSignRequest_WrongMethod(t *testing.T) {
	handlers := testHandlers(t, newTestManager())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/gpg-sign/request", nil)
	rr := httptest.NewRecorder()

	handlers.HandleGPGSignRequest(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

func TestHandleGPGSignRequest_KeyIDVisibleInPendingList(t *testing.T) {
	handlers := testHandlers(t, newTestManager())

	rr := postGPGSign(t, handlers, validGPGSignBody)

	if rr.Code != http.StatusOK {
		t.Fatalf("POST failed with %d: %s", rr.Code, rr.Body.String())
	}

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
	if pr.Type != approval.RequestTypeGPGSign {
		t.Errorf("expected type %q, got %q", approval.RequestTypeGPGSign, pr.Type)
	}
	// httptest gives the handler no unix peer, so the client is unknown.
	assert.Equal(t, approval.ClientUnknown, pr.Client)
}

// Guards against consent spoofing, which made the daemon a signing oracle: the
// prompt shows the fields of the signed commit object, not the display fields
// that the caller sent.
func TestHandleGPGSignRequest_DisplayBoundToCommitObject(t *testing.T) {
	mgr := newTestManager()
	handlers := testHandlers(t, mgr)

	// The bytes that will actually be signed describe an attacker identity and a
	// dangerous commit message; the client lies about all of them in the display
	// fields, claiming a benign typo fix.
	commitObject := approval.SignedObject("tree 4b825dc642cb6eb9a060e54bf8d69288fbee4904\n" +
		"parent 1111111111111111111111111111111111111111\n" +
		"author Real Attacker <evil@example.com> 1700000000 +0000\n" +
		"committer Real Attacker <evil@example.com> 1700000000 +0000\n" +
		"\n" +
		"backdoor: exfiltrate secrets\n")

	rr := postGPGSign(t, handlers, gpgSignBody(t, approval.GPGSignInfo{
		RepoName:     "myrepo",
		KeyID:        "ABCD1234",
		SignedObject: commitObject,
		Signed: approval.SignedPayload{
			Message: "docs: fix typo",
			Signer:  "Benign Author <good@example.com>",
			Details: approval.CommitDetails{
				Committer:    "Benign Author <good@example.com>",
				ParentHash:   "2222222222222222222222222222222222222222",
				ChangedFiles: []string{"README.md"},
			},
		},
	}))

	require.Equalf(t, http.StatusOK, rr.Code, "body: %s", rr.Body.String())

	pending := mgr.List()
	require.Len(t, pending, 1)
	info := requireGPGSign(t, pending[0].Request)

	// The displayed fields must come from the signed bytes, not the client's lie.
	// The staged files are not in the bytes, so the client's stay.
	assert.Equal(t, approval.SignedPayload{
		Message: "backdoor: exfiltrate secrets",
		Signer:  "Real Attacker <evil@example.com> 1700000000 +0000",
		Details: approval.CommitDetails{
			Committer:    "Real Attacker <evil@example.com> 1700000000 +0000",
			ParentHash:   "1111111111111111111111111111111111111111",
			ChangedFiles: []string{"README.md"},
		},
	}, info.Signed, "the display must be bound to the commit object")
	// The bytes to be signed must be preserved verbatim.
	assert.Equal(t, commitObject, info.SignedObject, "commit object must not be mutated")
}

// The prompt for an annotated tag shows the fields of the signed tag object,
// even when the caller claims to sign a commit.
func TestHandleGPGSignRequest_DisplayBoundToTagObject(t *testing.T) {
	mgr := newTestManager()
	handlers := testHandlers(t, mgr)

	tagObject := approval.SignedObject("object 1111111111111111111111111111111111111111\n" +
		"type commit\n" +
		"tag v9.9.9\n" +
		"tagger Real Tagger <tag@example.com> 1700000000 +0000\n" +
		"\n" +
		"release: v9.9.9\n")

	rr := postGPGSign(t, handlers, gpgSignBody(t, approval.GPGSignInfo{
		RepoName:     "myrepo",
		KeyID:        "ABCD1234",
		SignedObject: tagObject,
		Signed: approval.SignedPayload{
			Message: "docs: fix typo",
			Signer:  "Benign Author <good@example.com>",
			Details: approval.CommitDetails{
				Committer:    "Benign Author <good@example.com>",
				ChangedFiles: []string{"README.md"},
			},
		},
	}))

	require.Equalf(t, http.StatusOK, rr.Code, "body: %s", rr.Body.String())

	pending := mgr.List()
	require.Len(t, pending, 1)
	info := requireGPGSign(t, pending[0].Request)

	// A tag has no staged files, so the client's are dropped.
	assert.Equal(t, approval.SignedPayload{
		Message: "release: v9.9.9",
		Signer:  "Real Tagger <tag@example.com> 1700000000 +0000",
		Details: approval.TagDetails{Name: "v9.9.9", Target: "1111111111111111111111111111111111111111"},
	}, info.Signed, "kind and display must be re-derived from the tag object")
	assert.Equal(t, tagObject, info.SignedObject, "signed bytes must not be mutated")
}

// An approval.Observer that keeps the messages a WebSocket would send when a
// request ends, so a test reads them without a socket. Manager calls it on the resolving
// goroutine.
type wsEventObserver struct {
	msgs []WSMessage
}

func (o *wsEventObserver) RequestEnded(entry approval.HistoryEntry) {
	o.msgs = append(o.msgs, endedMessages(entry)...)
}

func (o *wsEventObserver) RequestCreated(approval.Pending)      {}
func (o *wsEventObserver) RuleAdded(approval.AutoApproveRule)   {}
func (o *wsEventObserver) RuleRemoved(approval.AutoApproveRule) {}

// A GPGRunner that returns a fixed result without running gpg.
type fakeGPGRunner struct {
	res approval.GPGResult
}

func (f *fakeGPGRunner) Sign(approval.GPGKeyID, approval.SignedObject) (approval.GPGResult, error) {
	return f.res, nil
}

// Guards the fail-closed half of the comm-spoofing fix: a gpg_sign auto-approve
// rule keys on the invoker's exe, so a caller whose exe cannot be resolved
// stays pending. The podman E2E suite covers a matching exe.
func TestHandleGPGSignRequest_UnverifiableInvokerStaysPending(t *testing.T) {
	mgr := newTestManager()
	handlers := testHandlers(t, mgr)
	handlers.resolver.GPGRunner = &fakeGPGRunner{res: approval.GPGResult{
		Signature: []byte("FAKE_SIG"),
		Status:    []byte("[GNUPG:] SIG_CREATED D"),
	}}

	// A rule with no invoker exe must never match, because exe is the sole
	// identity and an empty exe fails closed. No request can produce such a
	// rule, so the test writes one by hand.
	mgr.AddAutoApproveRule(approval.AutoApproveRule{
		ID:          "no-exe",
		RequestType: approval.RequestTypeGPGSign,
	})

	rr := postGPGSign(t, handlers, validGPGSignBody)

	require.Equalf(t, http.StatusOK, rr.Code, "body: %s", rr.Body.String())

	// The request must be pending (awaiting a decision), not silently signed.
	assert.Len(t, mgr.List(), 1, "unverifiable invoker must not be auto-approved")
}

// A gpg_sign auto-approve rule matches a caller with the rule's invoker exe,
// never one that only shares its comm.
func TestGPGSignEphemeralRuleMatchesOnInvokerExe(t *testing.T) {
	mgr := newTestManager()

	exeSender := localSender("git", "/usr/bin/git")
	mustAddRule(t, mgr, &approval.Request{Type: approval.RequestTypeGPGSign, SenderInfo: exeSender})

	// Same exe → matches.
	assert.NotNil(t, mgr.CheckAutoApproveRules(approval.RequestInfo{Type: approval.RequestTypeGPGSign, SenderInfo: exeSender}),
		"rule should match a caller with the same invoker exe")

	// Same spoofable comm but a different real exe → must NOT match.
	spoofed := localSender("git", "/tmp/malware") // attacker set comm to "git"
	assert.Nil(t, mgr.CheckAutoApproveRules(approval.RequestInfo{Type: approval.RequestTypeGPGSign, SenderInfo: spoofed}),
		"rule must not match a caller that only spoofed the comm")
}

// An auto-approve rule for another invoker leaves the request pending.
func TestHandleGPGSignRequest_NoMatchingRuleStaysPending(t *testing.T) {
	mgr := newTestManager()
	handlers := testHandlers(t, mgr)
	handlers.resolver.GPGRunner = &fakeGPGRunner{}

	// Rule for a different invoker name.
	mustAddRule(t, mgr, &approval.Request{
		Type:       approval.RequestTypeGPGSign,
		SenderInfo: localSender("some-other-process.service", "/usr/lib/some-other-process"),
	})

	rr := postGPGSign(t, handlers, validGPGSignBody)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if pending := mgr.List(); len(pending) != 1 {
		t.Fatalf("expected 1 pending request when no rule matches, got %d", len(pending))
	}
}

func TestHandleGPGSignRequest_WSSignatureOnApproval(t *testing.T) {
	mgr := newTestManager()
	id := mgr.Create(signableGPGSignInfo().RequestInfo(approval.SenderInfo{}))
	obs := &wsEventObserver{}
	mgr.Subscribe(obs)

	require.NoError(t, mgr.ApproveGPGSign(id, approval.GPGResult{Signature: []byte("SIG")}))

	require.NotEmpty(t, obs.msgs, "no message for the approval")

	msg, ok := obs.msgs[0].payload.(requestResolvedMsg)
	require.Truef(t, ok, "expected type 'request_resolved', got %q", obs.msgs[0].Type())
	if msg.Result != approval.ResolutionApproved {
		t.Errorf("expected result 'approved', got %q", msg.Result)
	}
	if msg.GPGResult == nil || len(msg.Signature) == 0 {
		t.Error("expected a non-empty Signature for gpg_sign approval")
	}
}
