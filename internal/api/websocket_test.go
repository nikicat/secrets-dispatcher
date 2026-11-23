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

	"github.com/nikicat/secrets-dispatcher/internal/apiclient"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/gpgsign"
	"github.com/nikicat/secrets-dispatcher/internal/proxy"
	"github.com/nikicat/secrets-dispatcher/internal/unixsock"
	"github.com/nikicat/secrets-dispatcher/internal/wsmsg"
)

// mockClientProvider implements ClientProvider for testing.
type mockClientProvider struct {
	clients []proxy.ClientInfo
}

func (m *mockClientProvider) Clients() []proxy.ClientInfo {
	return m.clients
}

// Returns a provider that reports one client.
func oneClient(name approval.ClientName, socketPath unixsock.Path) *mockClientProvider {
	return &mockClientProvider{clients: []proxy.ClientInfo{{Name: name, SocketPath: socketPath}}}
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

// Serves a WSHandler over mgr, with a ClientProvider that reports one proxy
// client, until the test ends.
func newWSFixture(t *testing.T, mgr *approval.Manager) *wsFixture {
	t.Helper()
	return newWSFixtureWith(t, mgr, oneClient(testClient, "/test/socket"))
}

// Serves a WSHandler over mgr and provider until the test ends.
func newWSFixtureWith(t *testing.T, mgr *approval.Manager, provider ClientProvider) *wsFixture {
	t.Helper()
	auth := newTestAuth(t)
	handler := NewWSHandler(mgr, provider, auth, 0)
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
		url:     apiclient.BaseURL(server.URL).WebSocketURL(""),
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

// Reads the next message on conn and fails the test unless its type is want.
// Returns the message's JSON.
func (f *wsFixture) read(conn *websocket.Conn, want wsmsg.Type) []byte {
	f.t.Helper()
	_, data, err := conn.Read(f.ctx)
	require.NoError(f.t, err, "read message")
	var msg wsmsg.Header
	require.NoError(f.t, json.Unmarshal(data, &msg), "decode message type")
	require.Equal(f.t, want, msg.Type)
	return data
}

// Reads the next message on conn and fails the test unless it has payload type
// P. Returns the payload.
func readMsg[P wsPayload](f *wsFixture, conn *websocket.Conn) P {
	f.t.Helper()
	var p P
	data := f.read(conn, p.wsType())
	require.NoError(f.t, json.Unmarshal(data, &p), "decode message")
	return p
}

func TestWSHandler_Unauthorized(t *testing.T) {
	f := newWSFixture(t, approval.NewManagerForTest(approval.ManagerConfig{}))

	_, _, err := websocket.Dial(f.ctx, f.url, nil)
	assert.Error(t, err, "expected connection to fail without auth")
}

// Checks that a WebSocket from another origin is refused (ADR 0006).
func TestWSHandler_Origin(t *testing.T) {
	tests := []struct {
		name   string
		origin func(host string) string
		ok     bool
	}{
		{"same origin", func(host string) string { return "http://" + host }, true},
		{"other port", func(string) string { return "http://127.0.0.1:1" }, false},
		{"other host", func(string) string { return "https://evil.example" }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newWSFixture(t, approval.NewManagerForTest(approval.ManagerConfig{}))
			origin := tt.origin(strings.TrimPrefix(f.url, "ws://"))
			conn, resp, err := websocket.Dial(f.ctx, f.url, &websocket.DialOptions{
				HTTPHeader: http.Header{
					"Cookie": []string{mintSession(t, f.auth)},
					"Origin": []string{origin},
				},
			})
			if !tt.ok {
				require.Error(t, err)
				assert.Equal(t, http.StatusForbidden, resp.StatusCode)
				return
			}
			require.NoError(t, err)
			defer conn.Close(websocket.StatusNormalClosure, "")
			f.read(conn, wsmsg.Snapshot)
		})
	}
}

func TestWSHandler_Snapshot(t *testing.T) {
	provider := &mockClientProvider{
		clients: []proxy.ClientInfo{
			{Name: "client1", SocketPath: "/socket1"},
			{Name: "client2", SocketPath: "/socket2"},
		},
	}
	f := newWSFixtureWith(t, approval.NewManagerForTest(approval.ManagerConfig{}), provider)

	msg := readMsg[snapshotMsg](f, f.dial())

	assert.Len(t, msg.Clients, 2)
	assert.Empty(t, msg.Requests)
	// Version should match BuildVersion (may be empty in dev mode)
	assert.Equal(t, BuildVersion, msg.Version)
}

func TestWSHandler_RequestCreated(t *testing.T) {
	f := newWSFixture(t, approval.NewManagerForTest(approval.ManagerConfig{}))
	conn := f.dial()
	f.read(conn, wsmsg.Snapshot)

	reqCtx, reqCancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
	defer reqCancel()
	done := startRequest(reqCtx, f.mgr, approval.ItemInfo{Path: "/test/item"})

	msg := readMsg[requestCreatedMsg](f, conn)

	assert.Equal(t, testClient, msg.Request.Client)

	<-done
}

func TestWSHandler_RequestResolved(t *testing.T) {
	f := newWSFixture(t, approval.NewManagerForTest(approval.ManagerConfig{}))
	conn := f.dial()
	f.read(conn, wsmsg.Snapshot)

	done := startRequest(t.Context(), f.mgr, approval.ItemInfo{Path: "/test/item"})

	created := readMsg[requestCreatedMsg](f, conn)
	f.mgr.Approve(created.Request.ID)

	msg := readMsg[requestResolvedMsg](f, conn)

	assert.Equal(t, created.Request.ID, msg.ID)
	assert.Equal(t, approval.ResolutionApproved, msg.Result)

	<-done
}

func TestWSHandler_HistoryEntryIsTheStoredEntry(t *testing.T) {
	f := newWSFixture(t, approval.NewManagerForTest(approval.ManagerConfig{}))
	conn := f.dial()
	f.read(conn, wsmsg.Snapshot)

	done := startRequest(t.Context(), f.mgr, approval.ItemInfo{Path: "/test/item"})

	created := readMsg[requestCreatedMsg](f, conn)
	f.mgr.Approve(created.Request.ID)
	f.read(conn, wsmsg.RequestResolved)
	msg := readMsg[historyEntryMsg](f, conn)
	<-done

	history := f.mgr.History()
	require.Len(t, history, 1)
	want, err := json.Marshal(convertHistoryEntry(history[0]))
	require.NoError(t, err)
	got, err := json.Marshal(msg.HistoryEntry)
	require.NoError(t, err)
	assert.JSONEq(t, string(want), string(got))
}

func TestWSHandler_RequestExpired(t *testing.T) {
	mgr := approval.NewManagerForTest(approval.ManagerConfig{Timeout: 100 * time.Millisecond})
	f := newWSFixture(t, mgr)
	conn := f.dial()
	f.read(conn, wsmsg.Snapshot)

	done := startRequest(t.Context(), mgr, approval.ItemInfo{Path: "/test/item"})

	f.read(conn, wsmsg.RequestCreated)
	f.read(conn, wsmsg.RequestExpired)

	<-done
}

func TestWSHandler_RequestCancelled(t *testing.T) {
	f := newWSFixture(t, approval.NewManagerForTest(approval.ManagerConfig{}))
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
	f := newWSFixture(t, approval.NewManagerForTest(approval.ManagerConfig{}))
	conn := f.dial()
	f.read(conn, wsmsg.Snapshot)

	client := proxy.ClientInfo{Name: "new-client", SocketPath: "/new/socket"}
	f.handler.BroadcastClientConnected(client)

	msg := readMsg[clientConnectedMsg](f, conn)

	assert.Equal(t, approval.ClientName("new-client"), msg.Client.Name)
}

// Returns a request from invoker gh to read one secret in the default
// collection, for the auto-approve rule tests to build rules from.
func ghSecretRequest() *approval.Request {
	return &approval.Request{
		Type:       approval.RequestTypeGetSecret,
		Items:      []approval.ItemInfo{{Path: "/org/freedesktop/secrets/collection/default/i1"}},
		SenderInfo: approval.LocalSenderForTest("gh", "/usr/bin/gh"),
	}
}

func TestConvertRequest_EncodesAutoApproveScope(t *testing.T) {
	noExe := ghSecretRequest()
	noExe.SenderInfo.ProcessChain[0].Exe = nil

	tests := []struct {
		name string
		req  *approval.Request
		want any
	}{
		{"local process with exe", ghSecretRequest(), map[string]any{"invoker_exe": "/usr/bin/gh", "request_type": "get_secret", "collection": "default"}},
		{"process without exe", noExe, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(convertRequest(tt.req))
			require.NoError(t, err)

			var fields map[string]any
			require.NoError(t, json.Unmarshal(data, &fields))
			assert.Equal(t, tt.want, fields["auto_approve_scope"])
		})
	}
}

func TestWSHandler_SnapshotIncludesAutoApproveRules(t *testing.T) {
	f := newWSFixture(t, approval.NewManagerForTest(approval.ManagerConfig{}))

	// Add a rule before connecting
	mustAddRule(t, f.mgr, ghSecretRequest())

	msg := readMsg[snapshotMsg](f, f.dial())

	require.Len(t, msg.AutoApproveRules, 1)
	assert.Equal(t, "gh", msg.AutoApproveRules[0].InvokerName)
	assert.Equal(t, approval.RequestTypeGetSecret, msg.AutoApproveRules[0].RequestType)
}

func TestWSHandler_AutoApproveRuleAdded(t *testing.T) {
	f := newWSFixture(t, approval.NewManagerForTest(approval.ManagerConfig{}))
	conn := f.dial()
	f.read(conn, wsmsg.Snapshot)

	req := ghSecretRequest()
	req.Items[0].Attributes = map[string]string{"service": "gh:github.com"}
	mustAddRule(t, f.mgr, req)

	msg := readMsg[autoApproveRuleAddedMsg](f, conn)

	assert.Equal(t, "gh", msg.AutoApproveRule.InvokerName)
	assert.Equal(t, approval.RequestTypeGetSecret, msg.AutoApproveRule.RequestType)
	assert.Equal(t, approval.OnlyCollection("default"), msg.AutoApproveRule.Collection)
	assert.NotEmpty(t, msg.AutoApproveRule.ID, "rule ID")
}

func TestWSHandler_AutoApproveRuleRemoved(t *testing.T) {
	f := newWSFixture(t, approval.NewManagerForTest(approval.ManagerConfig{}))
	conn := f.dial()
	f.read(conn, wsmsg.Snapshot)

	ruleID := mustAddRule(t, f.mgr, ghSecretRequest())
	f.read(conn, wsmsg.AutoApproveRuleAdded)

	require.NoError(t, f.mgr.RemoveAutoApproveRule(ruleID), "remove rule")

	msg := readMsg[autoApproveRuleRemovedMsg](f, conn)

	assert.Equal(t, ruleID, msg.ID)
}

func TestWSHandler_MultipleConnections(t *testing.T) {
	f := newWSFixture(t, approval.NewManagerForTest(approval.ManagerConfig{}))

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
// request: approved, denied or cancelled.
func TestWSHandler_GPGSignClientReadsResolution(t *testing.T) {
	signed := &approval.GPGResult{Signature: []byte("SIG")}
	tests := []struct {
		resolve func(mgr *approval.Manager, id approval.RequestID) error
		want    approval.Resolution
		wantGPG *approval.GPGResult
	}{
		{func(mgr *approval.Manager, id approval.RequestID) error { return mgr.ApproveGPGSign(id, *signed) }, approval.ResolutionApproved, signed},
		{(*approval.Manager).Deny, approval.ResolutionDenied, nil},
		{(*approval.Manager).Cancel, approval.ResolutionCancelled, nil},
	}
	for _, tt := range tests {
		t.Run(string(tt.want), func(t *testing.T) {
			f := newWSFixture(t, approval.NewManagerForTest(approval.ManagerConfig{}))
			conn := f.dial()
			f.read(conn, wsmsg.Snapshot)

			id := f.mgr.Create(approval.CommitSignRequestForTest(approval.RemoteSenderForTest("git"), "repo"))
			require.NoError(t, tt.resolve(f.mgr, id), "resolve request")

			outcome, err := gpgsign.WaitForResolution(f.ctx, conn, id)
			require.NoError(t, err, "wait for resolution")
			assert.Equal(t, tt.want, outcome.Result)
			assert.Equal(t, tt.wantGPG, outcome.GPGResult)
		})
	}
}
