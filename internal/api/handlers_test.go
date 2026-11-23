package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nikicat/secrets-dispatcher/internal/apiclient"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/nikicat/secrets-dispatcher/internal/proxy"
	"github.com/nikicat/secrets-dispatcher/internal/testutil"
	"github.com/nikicat/secrets-dispatcher/internal/unixsock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The proxy client, its socket and the D-Bus session that the fixtures report.
const (
	testClient  approval.ClientName = "test-client"
	testSocket  unixsock.Path       = "/path/to/socket"
	testSession dbus.ObjectPath     = "/session/1"
)

// Mints a live browser session on auth and returns its "session=<id>" Cookie
// header value.
func mintSession(t *testing.T, auth *Auth) string {
	t.Helper()
	rr := httptest.NewRecorder()
	require.NoError(t, auth.SetSessionCookie(rr), "mint session")
	cookies := rr.Result().Cookies()
	require.NotEmpty(t, cookies, "SetSessionCookie set no cookie")
	return cookies[0].Name + "=" + cookies[0].Value
}

// Fails the test unless the response has the given status, and prints the body
// when it does not.
func requireStatus(t *testing.T, rr *httptest.ResponseRecorder, want int) {
	t.Helper()
	require.Equalf(t, want, rr.Code, "body: %s", rr.Body.String())
}

// Fails the test as requireStatus does, and returns the JSON body decoded as T.
func decodeResponse[T any](t *testing.T, rr *httptest.ResponseRecorder, wantStatus int) T {
	t.Helper()
	requireStatus(t, rr, wantStatus)
	var body T
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&body), "decode response")
	return body
}

// testHandlers creates handlers with a test auth for use in tests.
func testHandlers(t *testing.T, mgr *approval.Manager) *Handlers {
	t.Helper()
	auth := newTestAuth(t)
	return NewHandlers(ServerConfig{Resolver: NewResolver(mgr, proxy.SlowUpstream{}), Auth: auth}, oneClient(testClient, testSocket))
}

func mustAddRule(t *testing.T, mgr *approval.Manager, req *approval.Request) approval.AutoApproveRuleID {
	t.Helper()
	scope, err := req.AutoApproveScope()
	require.NoError(t, err)
	return mgr.AddAutoApproveRule(scope, req.SenderInfo.InvokerName)
}

// A get_secret request that a test started and that its manager holds pending.
type pendingRequest struct {
	ID   approval.RequestID
	done <-chan error
}

// Returns RequireApproval's error once the request is resolved, and fails the
// test when that takes over a second.
func (p pendingRequest) wait(t *testing.T) error {
	t.Helper()
	return testutil.Recv(t, p.done, time.Second, "RequireApproval did not unblock")
}

// Starts a get_secret request from testClient for items on testSession in the
// background, and returns the channel that receives RequireApproval's error.
func startRequest(ctx context.Context, mgr *approval.Manager, items ...approval.ItemInfo) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := mgr.RequireApproval(ctx, approval.RequestInfo{Items: items, Session: testSession, Type: approval.RequestTypeGetSecret, SenderInfo: approval.SenderInfo{Client: testClient}})
		done <- err
	}()
	return done
}

// Starts a request as startRequest does, and returns once mgr lists it as
// pending. The request is cancelled when the test ends.
func startPending(t *testing.T, mgr *approval.Manager, items ...approval.ItemInfo) pendingRequest {
	t.Helper()
	done := startRequest(t.Context(), mgr, items...)
	var id approval.RequestID
	require.Eventually(t, func() bool {
		if reqs := mgr.Pending(); len(reqs) > 0 {
			id = reqs[0].ID
		}
		return id != ""
	}, time.Second, 10*time.Millisecond, "request did not appear")
	return pendingRequest{ID: id, done: done}
}

func TestHandleStatus(t *testing.T) {
	handlers := testHandlers(t, approval.NewManagerForTest(approval.ManagerConfig{}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	rr := httptest.NewRecorder()

	handlers.HandleStatus(rr, req)

	resp := decodeResponse[StatusResponse](t, rr, http.StatusOK)

	assert.True(t, resp.Running)
	assert.Zero(t, resp.PendingCount)
	require.Len(t, resp.Clients, 1)
	assert.Equal(t, testClient, resp.Clients[0].Name)
	assert.Equal(t, testSocket, resp.Clients[0].SocketPath)
}

func TestHandleStatus_WrongMethod(t *testing.T) {
	handlers := testHandlers(t, approval.NewManagerForTest(approval.ManagerConfig{}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/status", nil)
	rr := httptest.NewRecorder()

	handlers.HandleStatus(rr, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestHandlePendingList_Empty(t *testing.T) {
	handlers := testHandlers(t, approval.NewManagerForTest(approval.ManagerConfig{}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/pending", nil)
	rr := httptest.NewRecorder()

	handlers.HandlePendingList(rr, req)

	resp := decodeResponse[PendingListResponse](t, rr, http.StatusOK)

	assert.Empty(t, resp.Requests)
}

func TestHandlePendingList_WithRequests(t *testing.T) {
	mgr := approval.NewManagerForTest(approval.ManagerConfig{})
	handlers := testHandlers(t, mgr)

	startPending(t, mgr, approval.ItemInfo{Path: "/test/item1"}, approval.ItemInfo{Path: "/test/item2"})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/pending", nil)
	rr := httptest.NewRecorder()

	handlers.HandlePendingList(rr, req)

	resp := decodeResponse[PendingListResponse](t, rr, http.StatusOK)

	require.Len(t, resp.Requests, 1)

	r := resp.Requests[0]
	assert.Equal(t, testClient, r.Client)
	assert.Len(t, r.Items, 2)
	assert.Equal(t, testSession, r.Session)
}

func TestHandleApprove_Success(t *testing.T) {
	mgr := approval.NewManagerForTest(approval.ManagerConfig{})
	handlers := testHandlers(t, mgr)

	pending := startPending(t, mgr, approval.ItemInfo{Path: "/test/item"})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/pending/"+string(pending.ID)+"/approve", nil)
	rr := httptest.NewRecorder()

	handlers.HandleApprove(rr, req)

	resp := decodeResponse[ActionResponse](t, rr, http.StatusOK)

	assert.Equal(t, ActionApproved, resp.Status)

	assert.NoError(t, pending.wait(t), "RequireApproval on approve")
}

func TestHandleApprove_NotFound(t *testing.T) {
	handlers := testHandlers(t, approval.NewManagerForTest(approval.ManagerConfig{}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/pending/nonexistent-id/approve", nil)
	rr := httptest.NewRecorder()

	handlers.HandleApprove(rr, req)

	assertErrorBody(t, rr, http.StatusNotFound, "request not found or expired")
}

func TestHandleApprove_WrongMethod(t *testing.T) {
	handlers := testHandlers(t, approval.NewManagerForTest(approval.ManagerConfig{}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/pending/some-id/approve", nil)
	rr := httptest.NewRecorder()

	handlers.HandleApprove(rr, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestHandleDeny_Success(t *testing.T) {
	mgr := approval.NewManagerForTest(approval.ManagerConfig{})
	handlers := testHandlers(t, mgr)

	pending := startPending(t, mgr, approval.ItemInfo{Path: "/test/item"})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/pending/"+string(pending.ID)+"/deny", nil)
	rr := httptest.NewRecorder()

	handlers.HandleDeny(rr, req)

	resp := decodeResponse[ActionResponse](t, rr, http.StatusOK)

	assert.Equal(t, ActionDenied, resp.Status)

	assert.Equal(t, approval.ErrDenied, pending.wait(t), "RequireApproval on deny")
}

func TestHandleCancel_Success(t *testing.T) {
	mgr := approval.NewManagerForTest(approval.ManagerConfig{})
	handlers := testHandlers(t, mgr)

	pending := startPending(t, mgr, approval.ItemInfo{Path: "/test/item"})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/pending/"+string(pending.ID)+"/cancel", nil)
	rr := httptest.NewRecorder()

	handlers.HandleCancel(rr, req)

	resp := decodeResponse[ActionResponse](t, rr, http.StatusOK)

	assert.Equal(t, ActionCancelled, resp.Status)
}

func TestHandleCancel_NotFound(t *testing.T) {
	handlers := testHandlers(t, approval.NewManagerForTest(approval.ManagerConfig{}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/pending/nonexistent-id/cancel", nil)
	rr := httptest.NewRecorder()

	handlers.HandleCancel(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestHandleCancel_WrongMethod(t *testing.T) {
	handlers := testHandlers(t, approval.NewManagerForTest(approval.ManagerConfig{}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/pending/some-id/cancel", nil)
	rr := httptest.NewRecorder()

	handlers.HandleCancel(rr, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rr.Code)
}

func TestHandleLog_Empty(t *testing.T) {
	handlers := testHandlers(t, approval.NewManagerForTest(approval.ManagerConfig{}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/log", nil)
	rr := httptest.NewRecorder()

	handlers.HandleLog(rr, req)

	resp := decodeResponse[HistoryResponse](t, rr, http.StatusOK)

	assert.Empty(t, resp.Entries, "no history yet")
}

func TestHandleLog_WithHistory(t *testing.T) {
	mgr := approval.NewManagerForTest(approval.ManagerConfig{})
	handlers := testHandlers(t, mgr)

	pending := startPending(t, mgr, approval.ItemInfo{Path: "/test/item", Label: "Test Item"})

	// Approve the request
	require.NoError(t, mgr.Approve(pending.ID))
	require.NoError(t, pending.wait(t))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/log", nil)
	rr := httptest.NewRecorder()

	handlers.HandleLog(rr, req)

	resp := decodeResponse[HistoryResponse](t, rr, http.StatusOK)

	require.Len(t, resp.Entries, 1)

	entry := resp.Entries[0]
	assert.Equal(t, approval.ResolutionApproved, entry.Resolution)
	assert.Equal(t, pending.ID, entry.Request.ID)
	assert.Equal(t, testClient, entry.Request.Client)
}

func TestExtractRequestID(t *testing.T) {
	tests := []struct {
		path     string
		prefix   string
		suffix   string
		expected approval.RequestID
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
			assert.Equal(t, tc.expected, extractRequestID(tc.path, tc.prefix, tc.suffix))
		})
	}
}

func TestWriteJSON(t *testing.T) {
	rr := httptest.NewRecorder()

	writeJSON(rr, ActionResponse{Status: ActionApproved})

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	assert.Equal(t, `{"status":"approved"}`+"\n", rr.Body.String())
}

func TestWriteJSON_Unencodable(t *testing.T) {
	rr := httptest.NewRecorder()

	writeJSON(rr, make(chan int))

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	assert.Equal(t, `{"error":"failed to encode response"}`+"\n", rr.Body.String())
}

// POST /api/v1/test/history refuses an entry whose resolution is not one of the
// approval.Resolution constants, and History stays empty.
func TestHandleTestInjectHistory_UnknownResolution(t *testing.T) {
	mgr := approval.NewManagerForTest(approval.ManagerConfig{})
	h := testHandlers(t, mgr)
	h.testMode = true
	body := `{"request":{"id":"r1","items":[],"type":"get_secret"},"resolution":"bogus","resolved_at":"2026-11-21T00:00:00Z"}`

	rr := httptest.NewRecorder()
	h.HandleTestInjectHistory(rr, httptest.NewRequest(http.MethodPost, "/api/v1/test/history", strings.NewReader(body)))

	require.Equal(t, http.StatusBadRequest, rr.Code)
	require.Empty(t, mgr.History())
}

// The client reads the message of an error answer that the API writes.
func TestWriteError_ClientReadsMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, "request not found or expired", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	client := apiclient.New(apiclient.BaseURL(srv.URL), config.AuthToken("token"), srv.Client())

	_, err := apiclient.GetJSON[struct{}](t.Context(), client, "/")
	require.EqualError(t, err, "daemon returned status 404: request not found or expired")
}
