package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/gpgsign"
	"github.com/nikicat/secrets-dispatcher/internal/proxy"
	"github.com/nikicat/secrets-dispatcher/internal/wsmsg"
)

// mockClientProvider implements ClientProvider for testing.
type mockClientProvider struct {
	clients []proxy.ClientInfo
}

func (m *mockClientProvider) Clients() []proxy.ClientInfo {
	return m.clients
}

// A WSHandler served over HTTP for the length of one test, and the deadline
// that bounds the test's dials and reads.
type wsFixture struct {
	t       *testing.T
	ctx     context.Context
	mgr     *approval.Manager
	auth    *Auth
	handler *WSHandler
	url     string
}

// Serves a WSHandler over mgr until the test ends. A nil provider makes the
// handler report the single client "test-client".
func newWSFixture(t *testing.T, mgr *approval.Manager, provider ClientProvider) *wsFixture {
	t.Helper()
	auth := newTestAuth(t)
	handler := NewWSHandler(mgr, provider, auth, "/test/socket", "test-client")
	// No middleware: the handler checks auth itself.
	server := httptest.NewServer(http.HandlerFunc(handler.HandleWS))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	return &wsFixture{
		t:       t,
		ctx:     ctx,
		mgr:     mgr,
		auth:    auth,
		handler: handler,
		url:     "ws" + strings.TrimPrefix(server.URL, "http"),
	}
}

// Opens a WebSocket with a fresh session cookie, closed when the test ends.
func (f *wsFixture) dial() *websocket.Conn {
	f.t.Helper()
	conn, _, err := websocket.Dial(f.ctx, f.url, &websocket.DialOptions{
		HTTPHeader: http.Header{"Cookie": []string{mintSession(f.t, f.auth)}},
	})
	require.NoError(f.t, err, "dial")
	f.t.Cleanup(func() { conn.Close(websocket.StatusNormalClosure, "") })
	return conn
}

// Reads the next message on conn and fails the test unless it decodes as a
// WSMessage of type want.
func (f *wsFixture) read(conn *websocket.Conn, want wsmsg.Type) WSMessage {
	f.t.Helper()
	_, data, err := conn.Read(f.ctx)
	require.NoError(f.t, err, "read message")
	var msg WSMessage
	require.NoError(f.t, json.Unmarshal(data, &msg), "decode message")
	require.Equal(f.t, want, msg.Type)
	return msg
}

func TestWSHandler_Unauthorized(t *testing.T) {
	f := newWSFixture(t, newTestManager(), nil)

	_, _, err := websocket.Dial(f.ctx, f.url, nil)
	assert.Error(t, err, "expected connection to fail without auth")
}

func TestWSHandler_Snapshot(t *testing.T) {
	provider := &mockClientProvider{
		clients: []proxy.ClientInfo{
			{Name: "client1", SocketPath: "/socket1"},
			{Name: "client2", SocketPath: "/socket2"},
		},
	}
	f := newWSFixture(t, newTestManager(), provider)

	msg := f.read(f.dial(), wsmsg.Snapshot)

	if len(msg.Clients) != 2 {
		t.Errorf("expected 2 clients, got %d", len(msg.Clients))
	}
	if len(msg.Requests) != 0 {
		t.Errorf("expected 0 requests, got %d", len(msg.Requests))
	}
	// Version should match BuildVersion (may be empty in dev mode)
	if msg.Version != BuildVersion {
		t.Errorf("expected version %q, got %q", BuildVersion, msg.Version)
	}
}

func TestWSHandler_RequestCreated(t *testing.T) {
	f := newWSFixture(t, newTestManager(), nil)
	conn := f.dial()
	f.read(conn, wsmsg.Snapshot)

	reqCtx, reqCancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer reqCancel()
	done := startRequest(reqCtx, f.mgr, approval.ItemInfo{Path: "/test/item"})

	msg := f.read(conn, wsmsg.RequestCreated)

	require.NotNil(t, msg.Request, "expected request in message")
	if msg.Request.Client != "test-client" {
		t.Errorf("expected client 'test-client', got '%s'", msg.Request.Client)
	}

	<-done
}

func TestWSHandler_RequestResolved(t *testing.T) {
	f := newWSFixture(t, newTestManager(), nil)
	conn := f.dial()
	f.read(conn, wsmsg.Snapshot)

	done := startRequest(t.Context(), f.mgr, approval.ItemInfo{Path: "/test/item"})

	created := f.read(conn, wsmsg.RequestCreated)
	require.NotNil(t, created.Request, "expected request in message")
	f.mgr.Approve(created.Request.ID)

	msg := f.read(conn, wsmsg.RequestResolved)

	if msg.ID != created.Request.ID {
		t.Errorf("expected ID '%s', got '%s'", created.Request.ID, msg.ID)
	}
	if msg.Result != approval.ResolutionApproved {
		t.Errorf("expected result 'approved', got '%s'", msg.Result)
	}

	<-done
}

func TestWSHandler_RequestExpired(t *testing.T) {
	mgr := newTestManager(func(cfg *approval.ManagerConfig) { cfg.Timeout = 100 * time.Millisecond })
	f := newWSFixture(t, mgr, nil)
	conn := f.dial()
	f.read(conn, wsmsg.Snapshot)

	done := startRequest(t.Context(), mgr, approval.ItemInfo{Path: "/test/item"})

	f.read(conn, wsmsg.RequestCreated)
	f.read(conn, wsmsg.RequestExpired)

	<-done
}

func TestWSHandler_RequestCancelled(t *testing.T) {
	f := newWSFixture(t, newTestManager(), nil)
	conn := f.dial()
	f.read(conn, wsmsg.Snapshot)

	reqCtx, reqCancel := context.WithCancel(t.Context())
	done := startRequest(reqCtx, f.mgr, approval.ItemInfo{Path: "/test/item"})

	f.read(conn, wsmsg.RequestCreated)
	reqCancel()
	f.read(conn, wsmsg.RequestCancelled)

	<-done
}

func TestWSHandler_BroadcastClient(t *testing.T) {
	f := newWSFixture(t, newTestManager(), nil)
	conn := f.dial()
	f.read(conn, wsmsg.Snapshot)

	client := proxy.ClientInfo{Name: "new-client", SocketPath: "/new/socket"}
	f.handler.BroadcastClientConnected(client)

	msg := f.read(conn, wsmsg.ClientConnected)

	require.NotNil(t, msg.Client, "expected client in message")
	if msg.Client.Name != "new-client" {
		t.Errorf("expected client name 'new-client', got '%s'", msg.Client.Name)
	}
}

// Returns a request from invoker gh to read one secret in the default
// collection, for the auto-approve rule tests to build rules from.
func ghSecretRequest() *approval.Request {
	return &approval.Request{
		Type:       approval.RequestTypeGetSecret,
		Items:      []approval.ItemInfo{{Path: "/org/freedesktop/secrets/collection/default/i1"}},
		SenderInfo: approval.SenderInfo{InvokerName: "gh"},
	}
}

func TestWSHandler_SnapshotIncludesAutoApproveRules(t *testing.T) {
	f := newWSFixture(t, newTestManager(), nil)

	// Add a rule before connecting
	f.mgr.AddAutoApproveRule(ghSecretRequest())

	msg := f.read(f.dial(), wsmsg.Snapshot)

	if len(msg.AutoApproveRules) != 1 {
		t.Fatalf("expected 1 auto-approve rule in snapshot, got %d", len(msg.AutoApproveRules))
	}
	if msg.AutoApproveRules[0].InvokerName != "gh" {
		t.Errorf("expected invoker 'gh', got '%s'", msg.AutoApproveRules[0].InvokerName)
	}
	if msg.AutoApproveRules[0].RequestType != approval.RequestTypeGetSecret {
		t.Errorf("expected request type 'get_secret', got '%s'", msg.AutoApproveRules[0].RequestType)
	}
}

func TestWSHandler_AutoApproveRuleAdded(t *testing.T) {
	f := newWSFixture(t, newTestManager(), nil)
	conn := f.dial()
	f.read(conn, wsmsg.Snapshot)

	req := ghSecretRequest()
	req.Items[0].Attributes = map[string]string{"service": "gh:github.com"}
	f.mgr.AddAutoApproveRule(req)

	msg := f.read(conn, wsmsg.AutoApproveRuleAdded)

	if msg.AutoApproveRule == nil {
		t.Fatal("expected auto_approve_rule in message")
	}
	if msg.AutoApproveRule.InvokerName != "gh" {
		t.Errorf("expected invoker 'gh', got '%s'", msg.AutoApproveRule.InvokerName)
	}
	if msg.AutoApproveRule.RequestType != approval.RequestTypeGetSecret {
		t.Errorf("expected request type 'get_secret', got '%s'", msg.AutoApproveRule.RequestType)
	}
	if msg.AutoApproveRule.Collection != "default" {
		t.Errorf("expected collection 'default', got '%s'", msg.AutoApproveRule.Collection)
	}
	if msg.AutoApproveRule.ID == "" {
		t.Error("expected non-empty rule ID")
	}
}

func TestWSHandler_AutoApproveRuleRemoved(t *testing.T) {
	f := newWSFixture(t, newTestManager(), nil)
	conn := f.dial()
	f.read(conn, wsmsg.Snapshot)

	ruleID := f.mgr.AddAutoApproveRule(ghSecretRequest())
	f.read(conn, wsmsg.AutoApproveRuleAdded)

	if err := f.mgr.RemoveAutoApproveRule(ruleID); err != nil {
		t.Fatalf("failed to remove rule: %v", err)
	}

	msg := f.read(conn, wsmsg.AutoApproveRuleRemoved)

	if msg.ID != ruleID {
		t.Errorf("expected rule ID '%s', got '%s'", ruleID, msg.ID)
	}
}

func TestWSHandler_MultipleConnections(t *testing.T) {
	f := newWSFixture(t, newTestManager(), nil)

	var conns []*websocket.Conn
	for range 3 {
		conn := f.dial()
		f.read(conn, wsmsg.Snapshot)
		conns = append(conns, conn)
	}

	f.handler.BroadcastClientConnected(proxy.ClientInfo{Name: "broadcast-client", SocketPath: "/broadcast"})

	// All connections should receive the message
	for _, conn := range conns {
		f.read(conn, wsmsg.ClientConnected)
	}
}

// Checks that the gpgsign thin client reads how the server resolved a gpg_sign
// request: approved, denied or cancelled. The client loops past a message
// whose type it does not recognize, so a drift between the two sides shows up
// as a read that runs out of time.
func TestWSHandler_GPGSignClientReadsResolution(t *testing.T) {
	tests := []struct {
		resolve func(mgr *approval.Manager, id string) error
		want    approval.Resolution
	}{
		{(*approval.Manager).Approve, approval.ResolutionApproved},
		{(*approval.Manager).Deny, approval.ResolutionDenied},
		{(*approval.Manager).Cancel, approval.ResolutionCancelled},
	}
	for _, tt := range tests {
		t.Run(string(tt.want), func(t *testing.T) {
			f := newWSFixture(t, newTestManager(), nil)
			conn := f.dial()
			f.read(conn, wsmsg.Snapshot)

			id, err := f.mgr.CreateGPGSignRequest("test-client", &approval.GPGSignInfo{KeyID: "ABCD1234", CommitObject: "tree 0\n"}, approval.SenderInfo{})
			require.NoError(t, err, "create gpg_sign request")
			require.NoError(t, tt.resolve(f.mgr, id), "resolve request")

			outcome, err := gpgsign.WaitForResolution(f.ctx, conn, id)
			require.NoError(t, err, "wait for resolution")
			assert.Equal(t, tt.want, outcome.Result)
		})
	}
}
