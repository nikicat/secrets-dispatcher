package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/proxy"
)

// mockClientProvider implements ClientProvider for testing.
type mockClientProvider struct {
	clients []proxy.ClientInfo
}

func (m *mockClientProvider) Clients() []proxy.ClientInfo {
	return m.clients
}

func TestWSHandler_Unauthorized(t *testing.T) {
	mgr := approval.NewManager(approval.ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
	auth, err := NewAuth(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create auth: %v", err)
	}

	handler := NewWSHandler(mgr, nil, auth, "/test/socket", "test-client")

	// Create test server
	server := httptest.NewServer(http.HandlerFunc(handler.HandleWS))
	defer server.Close()

	// Try to connect without auth - should fail
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _, err = websocket.Dial(ctx, wsURL, nil)
	if err == nil {
		t.Error("expected connection to fail without auth")
	}
}

func TestWSHandler_Snapshot(t *testing.T) {
	mgr := approval.NewManager(approval.ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
	auth, err := NewAuth(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create auth: %v", err)
	}

	provider := &mockClientProvider{
		clients: []proxy.ClientInfo{
			{Name: "client1", SocketPath: "/socket1"},
			{Name: "client2", SocketPath: "/socket2"},
		},
	}
	handler := NewWSHandler(mgr, provider, auth, "", "")

	// Create test server (no middleware - handler does its own auth)
	server := httptest.NewServer(http.HandlerFunc(handler.HandleWS))
	defer server.Close()

	// Connect with session cookie
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Cookie": []string{"session=" + auth.Token()},
		},
	})
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Read snapshot message
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read message: %v", err)
	}

	var msg WSMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("failed to parse message: %v", err)
	}

	if msg.Type != "snapshot" {
		t.Errorf("expected snapshot message, got %s", msg.Type)
	}
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
	mgr := approval.NewManager(approval.ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
	auth, err := NewAuth(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create auth: %v", err)
	}

	handler := NewWSHandler(mgr, nil, auth, "/test/socket", "test-client")

	// Create test server
	server := httptest.NewServer(http.HandlerFunc(handler.HandleWS))
	defer server.Close()

	// Connect
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Cookie": []string{"session=" + auth.Token()},
		},
	})
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Read and discard snapshot
	_, _, err = conn.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read snapshot: %v", err)
	}

	// Create a request in background
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		reqCtx, reqCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer reqCancel()
		mgr.RequireApproval(reqCtx, "test-client", []approval.ItemInfo{{Path: "/test/item"}}, "/session/1", approval.RequestTypeGetSecret, nil, approval.SenderInfo{})
	}()

	// Read request_created message
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read message: %v", err)
	}

	var msg WSMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("failed to parse message: %v", err)
	}

	if msg.Type != "request_created" {
		t.Errorf("expected request_created message, got %s", msg.Type)
	}
	if msg.Request == nil {
		t.Error("expected request in message")
	}
	if msg.Request.Client != "test-client" {
		t.Errorf("expected client 'test-client', got '%s'", msg.Request.Client)
	}

	wg.Wait()
}

func TestWSHandler_RequestResolved(t *testing.T) {
	mgr := approval.NewManager(approval.ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
	auth, err := NewAuth(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create auth: %v", err)
	}

	handler := NewWSHandler(mgr, nil, auth, "/test/socket", "test-client")

	// Create test server
	server := httptest.NewServer(http.HandlerFunc(handler.HandleWS))
	defer server.Close()

	// Connect
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Cookie": []string{"session=" + auth.Token()},
		},
	})
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Read and discard snapshot
	_, _, _ = conn.Read(ctx)

	// Create a request
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		mgr.RequireApproval(context.Background(), "test-client", []approval.ItemInfo{{Path: "/test/item"}}, "/session/1", approval.RequestTypeGetSecret, nil, approval.SenderInfo{})
	}()

	// Read request_created
	_, _, _ = conn.Read(ctx)

	// Get the request ID and approve it
	reqs := mgr.List()
	if len(reqs) == 0 {
		t.Fatal("no pending requests")
	}
	mgr.Approve(reqs[0].ID)

	// Read request_resolved message
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read message: %v", err)
	}

	var msg WSMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("failed to parse message: %v", err)
	}

	if msg.Type != "request_resolved" {
		t.Errorf("expected request_resolved message, got %s", msg.Type)
	}
	if msg.ID != reqs[0].ID {
		t.Errorf("expected ID '%s', got '%s'", reqs[0].ID, msg.ID)
	}
	if msg.Result != "approved" {
		t.Errorf("expected result 'approved', got '%s'", msg.Result)
	}

	wg.Wait()
}

func TestWSHandler_RequestExpired(t *testing.T) {
	mgr := approval.NewManager(approval.ManagerConfig{Timeout: 100 * time.Millisecond, HistoryMax: 100})
	auth, err := NewAuth(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create auth: %v", err)
	}

	handler := NewWSHandler(mgr, nil, auth, "/test/socket", "test-client")

	// Create test server
	server := httptest.NewServer(http.HandlerFunc(handler.HandleWS))
	defer server.Close()

	// Connect
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Cookie": []string{"session=" + auth.Token()},
		},
	})
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Read and discard snapshot
	_, _, _ = conn.Read(ctx)

	// Create a request that will timeout
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		mgr.RequireApproval(context.Background(), "test-client", []approval.ItemInfo{{Path: "/test/item"}}, "/session/1", approval.RequestTypeGetSecret, nil, approval.SenderInfo{})
	}()

	// Read request_created
	_, _, _ = conn.Read(ctx)

	// Read request_expired message
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read message: %v", err)
	}

	var msg WSMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("failed to parse message: %v", err)
	}

	if msg.Type != "request_expired" {
		t.Errorf("expected request_expired message, got %s", msg.Type)
	}

	wg.Wait()
}

func TestWSHandler_RequestCancelled(t *testing.T) {
	mgr := approval.NewManager(approval.ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
	auth, err := NewAuth(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create auth: %v", err)
	}

	handler := NewWSHandler(mgr, nil, auth, "/test/socket", "test-client")

	// Create test server
	server := httptest.NewServer(http.HandlerFunc(handler.HandleWS))
	defer server.Close()

	// Connect
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Cookie": []string{"session=" + auth.Token()},
		},
	})
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Read and discard snapshot
	_, _, _ = conn.Read(ctx)

	// Create a request with a cancellable context
	reqCtx, reqCancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		mgr.RequireApproval(reqCtx, "test-client", []approval.ItemInfo{{Path: "/test/item"}}, "/session/1", approval.RequestTypeGetSecret, nil, approval.SenderInfo{})
	}()

	// Read request_created
	_, _, _ = conn.Read(ctx)

	// Cancel the request context
	reqCancel()

	// Read request_cancelled message
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read message: %v", err)
	}

	var msg WSMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("failed to parse message: %v", err)
	}

	if msg.Type != "request_cancelled" {
		t.Errorf("expected request_cancelled message, got %s", msg.Type)
	}

	wg.Wait()
}

func TestWSHandler_BroadcastClient(t *testing.T) {
	mgr := approval.NewManager(approval.ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
	auth, err := NewAuth(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create auth: %v", err)
	}

	handler := NewWSHandler(mgr, nil, auth, "/test/socket", "test-client")

	// Create test server
	server := httptest.NewServer(http.HandlerFunc(handler.HandleWS))
	defer server.Close()

	// Connect
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Cookie": []string{"session=" + auth.Token()},
		},
	})
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Read and discard snapshot
	_, _, _ = conn.Read(ctx)

	// Broadcast client connected
	client := proxy.ClientInfo{Name: "new-client", SocketPath: "/new/socket"}
	handler.BroadcastClientConnected(client)

	// Read client_connected message
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read message: %v", err)
	}

	var msg WSMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("failed to parse message: %v", err)
	}

	if msg.Type != "client_connected" {
		t.Errorf("expected client_connected message, got %s", msg.Type)
	}
	if msg.Client == nil {
		t.Error("expected client in message")
	}
	if msg.Client.Name != "new-client" {
		t.Errorf("expected client name 'new-client', got '%s'", msg.Client.Name)
	}
}

func TestWSHandler_SnapshotIncludesAutoApproveRules(t *testing.T) {
	mgr := approval.NewManager(approval.ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100, AutoApproveDuration: 2 * time.Minute})
	auth, err := NewAuth(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create auth: %v", err)
	}

	// Add a rule before connecting
	req := &approval.Request{
		Type:       approval.RequestTypeGetSecret,
		Items:      []approval.ItemInfo{{Path: "/org/freedesktop/secrets/collection/default/i1"}},
		SenderInfo: approval.SenderInfo{UnitName: "gh"},
	}
	mgr.AddAutoApproveRule(req)

	handler := NewWSHandler(mgr, nil, auth, "", "")
	server := httptest.NewServer(http.HandlerFunc(handler.HandleWS))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Cookie": []string{"session=" + auth.Token()},
		},
	})
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Read snapshot
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read snapshot: %v", err)
	}

	var msg WSMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("failed to parse message: %v", err)
	}

	if msg.Type != "snapshot" {
		t.Fatalf("expected snapshot, got %s", msg.Type)
	}
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
	mgr := approval.NewManager(approval.ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100, AutoApproveDuration: 2 * time.Minute})
	auth, err := NewAuth(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create auth: %v", err)
	}

	handler := NewWSHandler(mgr, nil, auth, "", "")
	server := httptest.NewServer(http.HandlerFunc(handler.HandleWS))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Cookie": []string{"session=" + auth.Token()},
		},
	})
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Read and discard snapshot
	_, _, _ = conn.Read(ctx)

	// Add an auto-approve rule
	req := &approval.Request{
		Type:       approval.RequestTypeGetSecret,
		Items:      []approval.ItemInfo{{Path: "/org/freedesktop/secrets/collection/default/i1", Attributes: map[string]string{"service": "gh:github.com"}}},
		SenderInfo: approval.SenderInfo{UnitName: "gh"},
	}
	mgr.AddAutoApproveRule(req)

	// Read auto_approve_rule_added message
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read message: %v", err)
	}

	var msg WSMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("failed to parse message: %v", err)
	}

	if msg.Type != "auto_approve_rule_added" {
		t.Errorf("expected auto_approve_rule_added, got %s", msg.Type)
	}
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
	mgr := approval.NewManager(approval.ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100, AutoApproveDuration: 2 * time.Minute})
	auth, err := NewAuth(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create auth: %v", err)
	}

	handler := NewWSHandler(mgr, nil, auth, "", "")
	server := httptest.NewServer(http.HandlerFunc(handler.HandleWS))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Cookie": []string{"session=" + auth.Token()},
		},
	})
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Read and discard snapshot
	_, _, _ = conn.Read(ctx)

	// Add a rule
	req := &approval.Request{
		Type:       approval.RequestTypeGetSecret,
		Items:      []approval.ItemInfo{{Path: "/org/freedesktop/secrets/collection/default/i1"}},
		SenderInfo: approval.SenderInfo{UnitName: "gh"},
	}
	ruleID := mgr.AddAutoApproveRule(req)

	// Read and discard the rule_added message
	_, _, _ = conn.Read(ctx)

	// Remove the rule
	if err := mgr.RemoveAutoApproveRule(ruleID); err != nil {
		t.Fatalf("failed to remove rule: %v", err)
	}

	// Read auto_approve_rule_removed message
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("failed to read message: %v", err)
	}

	var msg WSMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("failed to parse message: %v", err)
	}

	if msg.Type != "auto_approve_rule_removed" {
		t.Errorf("expected auto_approve_rule_removed, got %s", msg.Type)
	}
	if msg.ID != ruleID {
		t.Errorf("expected rule ID '%s', got '%s'", ruleID, msg.ID)
	}
}

func TestWSHandler_MultipleConnections(t *testing.T) {
	mgr := approval.NewManager(approval.ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
	auth, err := NewAuth(t.TempDir())
	if err != nil {
		t.Fatalf("failed to create auth: %v", err)
	}

	handler := NewWSHandler(mgr, nil, auth, "/test/socket", "test-client")

	// Create test server
	server := httptest.NewServer(http.HandlerFunc(handler.HandleWS))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Connect multiple clients
	var conns []*websocket.Conn
	for i := 0; i < 3; i++ {
		conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
			HTTPHeader: http.Header{
				"Cookie": []string{"session=" + auth.Token()},
			},
		})
		if err != nil {
			t.Fatalf("failed to connect client %d: %v", i, err)
		}
		conns = append(conns, conn)
		// Read and discard snapshot
		_, _, _ = conn.Read(ctx)
	}

	defer func() {
		for _, conn := range conns {
			conn.Close(websocket.StatusNormalClosure, "")
		}
	}()

	// Broadcast to all
	client := proxy.ClientInfo{Name: "broadcast-client", SocketPath: "/broadcast"}
	handler.BroadcastClientConnected(client)

	// All connections should receive the message
	for i, conn := range conns {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Errorf("client %d failed to read: %v", i, err)
			continue
		}

		var msg WSMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			t.Errorf("client %d failed to parse: %v", i, err)
			continue
		}

		if msg.Type != "client_connected" {
			t.Errorf("client %d expected client_connected, got %s", i, msg.Type)
		}
	}
}
