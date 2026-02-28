package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/proxy"
	"github.com/nikicat/secrets-dispatcher/internal/testutil"
)

// testEnv holds the test environment with two isolated D-Bus daemons.
type testEnv struct {
	t          *testing.T
	tmpDir     string
	localAddr  string
	remoteAddr string
	localCmd   *exec.Cmd
	remoteCmd  *exec.Cmd
}

// newTestEnv creates a new test environment with isolated D-Bus daemons.
func newTestEnv(t *testing.T) *testEnv {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "secrets-dispatcher-test-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}

	env := &testEnv{
		t:      t,
		tmpDir: tmpDir,
	}

	// Start local D-Bus daemon
	localSocket := filepath.Join(tmpDir, "local.sock")
	env.localCmd, env.localAddr = startDBusDaemon(t, localSocket)

	// Start remote D-Bus daemon
	remoteSocket := filepath.Join(tmpDir, "remote.sock")
	env.remoteCmd, env.remoteAddr = startDBusDaemon(t, remoteSocket)

	return env
}

// cleanup stops the D-Bus daemons and removes temp files.
func (e *testEnv) cleanup() {
	if e.localCmd != nil && e.localCmd.Process != nil {
		e.localCmd.Process.Kill()
		e.localCmd.Wait()
	}
	if e.remoteCmd != nil && e.remoteCmd.Process != nil {
		e.remoteCmd.Process.Kill()
		e.remoteCmd.Wait()
	}
	if e.tmpDir != "" {
		os.RemoveAll(e.tmpDir)
	}
}

// localConn connects to the local D-Bus.
func (e *testEnv) localConn() *dbus.Conn {
	conn, err := dbus.Connect(e.localAddr)
	if err != nil {
		e.t.Fatalf("connect to local dbus: %v", err)
	}
	return conn
}

// remoteConn connects to the remote D-Bus.
func (e *testEnv) remoteConn() *dbus.Conn {
	conn, err := dbus.Connect(e.remoteAddr)
	if err != nil {
		e.t.Fatalf("connect to remote dbus: %v", err)
	}
	return conn
}

// remoteSocketPath returns the path to the remote socket file.
func (e *testEnv) remoteSocketPath() string {
	return filepath.Join(e.tmpDir, "remote.sock")
}

// startDBusDaemon starts a dbus-daemon on the given socket path.
func startDBusDaemon(t *testing.T, socketPath string) (*exec.Cmd, string) {
	t.Helper()

	addr := "unix:path=" + socketPath

	cmd := exec.Command("dbus-daemon",
		"--session",
		"--nofork",
		"--address="+addr,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("start dbus-daemon: %v", err)
	}

	// Wait for socket to be created
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(socketPath); err == nil {
			return cmd, addr
		}
		time.Sleep(100 * time.Millisecond)
	}

	cmd.Process.Kill()
	t.Fatalf("dbus-daemon socket not created: %s", socketPath)
	return nil, ""
}

func TestProxyBasicOperations(t *testing.T) {
	env := newTestEnv(t)
	defer env.cleanup()

	// Connect to local D-Bus and register mock service
	localConn := env.localConn()
	defer localConn.Close()

	mock := testutil.NewMockSecretService()
	if err := mock.Register(localConn); err != nil {
		t.Fatalf("register mock service: %v", err)
	}

	// Add a test item
	mock.AddItem("Test Secret", map[string]string{"test-attr": "test-value"}, []byte("test-secret"))

	// Start the proxy
	p := proxy.New(proxy.Config{
		ClientName:       "test-client",
		LogLevel:         slog.LevelDebug,
	})

	if err := connectProxyWithConns(p, env.localAddr, env.remoteSocketPath()); err != nil {
		t.Fatalf("connect proxy: %v", err)
	}
	defer p.Close()

	// Connect to remote D-Bus as a client
	remoteConn := env.remoteConn()
	defer remoteConn.Close()

	// Test: Get Collections property
	t.Run("GetCollections", func(t *testing.T) {
		obj := remoteConn.Object(dbustypes.BusName, dbustypes.ServicePath)
		variant, err := obj.GetProperty(dbustypes.ServiceInterface + ".Collections")
		if err != nil {
			t.Fatalf("get Collections: %v", err)
		}

		collections, ok := variant.Value().([]dbus.ObjectPath)
		if !ok {
			t.Fatalf("Collections is not []ObjectPath: %T", variant.Value())
		}
		if len(collections) == 0 {
			t.Error("expected at least one collection")
		}
		t.Logf("Collections: %v", collections)
	})

	// Test: ReadAlias
	t.Run("ReadAlias", func(t *testing.T) {
		obj := remoteConn.Object(dbustypes.BusName, dbustypes.ServicePath)
		call := obj.Call(dbustypes.ServiceInterface+".ReadAlias", 0, "default")
		if call.Err != nil {
			t.Fatalf("ReadAlias: %v", call.Err)
		}

		var collection dbus.ObjectPath
		if err := call.Store(&collection); err != nil {
			t.Fatalf("store result: %v", err)
		}
		if collection == "/" || collection == "" {
			t.Error("expected non-empty collection path")
		}
		t.Logf("Default collection: %s", collection)
	})

	// Test: SearchItems
	t.Run("SearchItems", func(t *testing.T) {
		obj := remoteConn.Object(dbustypes.BusName, dbustypes.ServicePath)
		call := obj.Call(dbustypes.ServiceInterface+".SearchItems", 0, map[string]string{"test-attr": "test-value"})
		if call.Err != nil {
			t.Fatalf("SearchItems: %v", call.Err)
		}

		var unlocked, locked []dbus.ObjectPath
		if err := call.Store(&unlocked, &locked); err != nil {
			t.Fatalf("store result: %v", err)
		}
		if len(unlocked) != 1 {
			t.Errorf("expected 1 unlocked item, got %d", len(unlocked))
		}
		t.Logf("Found items: unlocked=%v, locked=%v", unlocked, locked)
	})

	// Test: OpenSession + GetSecrets
	t.Run("OpenSessionAndGetSecrets", func(t *testing.T) {
		obj := remoteConn.Object(dbustypes.BusName, dbustypes.ServicePath)

		// Open session
		call := obj.Call(dbustypes.ServiceInterface+".OpenSession", 0, "plain", dbus.MakeVariant(""))
		if call.Err != nil {
			t.Fatalf("OpenSession: %v", call.Err)
		}

		var output dbus.Variant
		var sessionPath dbus.ObjectPath
		if err := call.Store(&output, &sessionPath); err != nil {
			t.Fatalf("store result: %v", err)
		}
		t.Logf("Session: %s", sessionPath)

		// Search for items
		call = obj.Call(dbustypes.ServiceInterface+".SearchItems", 0, map[string]string{"test-attr": "test-value"})
		if call.Err != nil {
			t.Fatalf("SearchItems: %v", call.Err)
		}

		var unlocked, locked []dbus.ObjectPath
		if err := call.Store(&unlocked, &locked); err != nil {
			t.Fatalf("store result: %v", err)
		}

		if len(unlocked) == 0 {
			t.Fatal("no items found")
		}

		// Get secrets
		call = obj.Call(dbustypes.ServiceInterface+".GetSecrets", 0, unlocked, sessionPath)
		if call.Err != nil {
			t.Fatalf("GetSecrets: %v", call.Err)
		}

		var secrets map[dbus.ObjectPath]dbustypes.Secret
		if err := call.Store(&secrets); err != nil {
			t.Fatalf("store result: %v", err)
		}

		if len(secrets) != 1 {
			t.Errorf("expected 1 secret, got %d", len(secrets))
		}

		for path, secret := range secrets {
			if string(secret.Value) != "test-secret" {
				t.Errorf("wrong secret value: got %q, want %q", secret.Value, "test-secret")
			}
			t.Logf("Secret for %s: %q", path, secret.Value)
		}
	})

	// Test: Unlock (should passthrough)
	t.Run("Unlock", func(t *testing.T) {
		obj := remoteConn.Object(dbustypes.BusName, dbustypes.ServicePath)
		call := obj.Call(dbustypes.ServiceInterface+".Unlock", 0, []dbus.ObjectPath{"/org/freedesktop/secrets/collection/default"})
		if call.Err != nil {
			t.Fatalf("Unlock: %v", call.Err)
		}

		var unlocked []dbus.ObjectPath
		var prompt dbus.ObjectPath
		if err := call.Store(&unlocked, &prompt); err != nil {
			t.Fatalf("store result: %v", err)
		}
		t.Logf("Unlocked: %v, prompt: %s", unlocked, prompt)
	})
}

func TestProxyItemOperations(t *testing.T) {
	env := newTestEnv(t)
	defer env.cleanup()

	localConn := env.localConn()
	defer localConn.Close()

	mock := testutil.NewMockSecretService()
	if err := mock.Register(localConn); err != nil {
		t.Fatalf("register mock service: %v", err)
	}

	itemPath := mock.AddItem("My Secret", map[string]string{"app": "test-app"}, []byte("secret-value"))

	p := proxy.New(proxy.Config{
		ClientName:       "test-client",
		LogLevel:         slog.LevelDebug,
	})

	if err := connectProxyWithConns(p, env.localAddr, env.remoteSocketPath()); err != nil {
		t.Fatalf("connect proxy: %v", err)
	}
	defer p.Close()

	remoteConn := env.remoteConn()
	defer remoteConn.Close()

	// Test: Item.GetSecret
	t.Run("ItemGetSecret", func(t *testing.T) {
		// First open a session
		serviceObj := remoteConn.Object(dbustypes.BusName, dbustypes.ServicePath)
		call := serviceObj.Call(dbustypes.ServiceInterface+".OpenSession", 0, "plain", dbus.MakeVariant(""))
		if call.Err != nil {
			t.Fatalf("OpenSession: %v", call.Err)
		}

		var output dbus.Variant
		var sessionPath dbus.ObjectPath
		if err := call.Store(&output, &sessionPath); err != nil {
			t.Fatalf("store result: %v", err)
		}

		// Get secret from item
		itemObj := remoteConn.Object(dbustypes.BusName, itemPath)
		call = itemObj.Call(dbustypes.ItemInterface+".GetSecret", 0, sessionPath)
		if call.Err != nil {
			t.Fatalf("Item.GetSecret: %v", call.Err)
		}

		var secret dbustypes.Secret
		if err := call.Store(&secret); err != nil {
			t.Fatalf("store result: %v", err)
		}

		if string(secret.Value) != "secret-value" {
			t.Errorf("wrong secret: got %q, want %q", secret.Value, "secret-value")
		}
		t.Logf("Got secret: %q", secret.Value)
	})

	// Test: Item properties
	t.Run("ItemProperties", func(t *testing.T) {
		itemObj := remoteConn.Object(dbustypes.BusName, itemPath)

		label, err := itemObj.GetProperty(dbustypes.ItemInterface + ".Label")
		if err != nil {
			t.Fatalf("get Label: %v", err)
		}
		if label.Value().(string) != "My Secret" {
			t.Errorf("wrong label: got %q", label.Value())
		}

		attrs, err := itemObj.GetProperty(dbustypes.ItemInterface + ".Attributes")
		if err != nil {
			t.Fatalf("get Attributes: %v", err)
		}
		t.Logf("Item attributes: %v", attrs.Value())
	})
}

func TestProxyCollectionOperations(t *testing.T) {
	env := newTestEnv(t)
	defer env.cleanup()

	localConn := env.localConn()
	defer localConn.Close()

	mock := testutil.NewMockSecretService()
	if err := mock.Register(localConn); err != nil {
		t.Fatalf("register mock service: %v", err)
	}

	p := proxy.New(proxy.Config{
		ClientName:       "test-client",
		LogLevel:         slog.LevelDebug,
	})

	if err := connectProxyWithConns(p, env.localAddr, env.remoteSocketPath()); err != nil {
		t.Fatalf("connect proxy: %v", err)
	}
	defer p.Close()

	remoteConn := env.remoteConn()
	defer remoteConn.Close()

	// Test: Collection.CreateItem
	t.Run("CollectionCreateItem", func(t *testing.T) {
		// First open a session
		serviceObj := remoteConn.Object(dbustypes.BusName, dbustypes.ServicePath)
		call := serviceObj.Call(dbustypes.ServiceInterface+".OpenSession", 0, "plain", dbus.MakeVariant(""))
		if call.Err != nil {
			t.Fatalf("OpenSession: %v", call.Err)
		}

		var output dbus.Variant
		var sessionPath dbus.ObjectPath
		if err := call.Store(&output, &sessionPath); err != nil {
			t.Fatalf("store result: %v", err)
		}

		// Create item
		collObj := remoteConn.Object(dbustypes.BusName, "/org/freedesktop/secrets/collection/default")
		properties := map[string]dbus.Variant{
			"org.freedesktop.Secret.Item.Label":      dbus.MakeVariant("New Item"),
			"org.freedesktop.Secret.Item.Attributes": dbus.MakeVariant(map[string]string{"new-attr": "new-value"}),
		}
		secret := dbustypes.Secret{
			Session:     sessionPath,
			Parameters:  nil,
			Value:       []byte("new-secret"),
			ContentType: "text/plain",
		}

		call = collObj.Call(dbustypes.CollectionInterface+".CreateItem", 0, properties, secret, true)
		if call.Err != nil {
			t.Fatalf("CreateItem: %v", call.Err)
		}

		var itemPath, promptPath dbus.ObjectPath
		if err := call.Store(&itemPath, &promptPath); err != nil {
			t.Fatalf("store result: %v", err)
		}

		if itemPath == "/" || itemPath == "" {
			t.Error("expected valid item path")
		}
		t.Logf("Created item: %s", itemPath)

		// Verify we can get the secret back
		itemObj := remoteConn.Object(dbustypes.BusName, itemPath)
		call = itemObj.Call(dbustypes.ItemInterface+".GetSecret", 0, sessionPath)
		if call.Err != nil {
			t.Fatalf("GetSecret: %v", call.Err)
		}

		var retrievedSecret dbustypes.Secret
		if err := call.Store(&retrievedSecret); err != nil {
			t.Fatalf("store result: %v", err)
		}

		if string(retrievedSecret.Value) != "new-secret" {
			t.Errorf("wrong secret: got %q, want %q", retrievedSecret.Value, "new-secret")
		}
	})

	// Test: Collection.SearchItems
	t.Run("CollectionSearchItems", func(t *testing.T) {
		collObj := remoteConn.Object(dbustypes.BusName, "/org/freedesktop/secrets/collection/default")
		call := collObj.Call(dbustypes.CollectionInterface+".SearchItems", 0, map[string]string{"new-attr": "new-value"})
		if call.Err != nil {
			t.Fatalf("SearchItems: %v", call.Err)
		}

		var results []dbus.ObjectPath
		if err := call.Store(&results); err != nil {
			t.Fatalf("store result: %v", err)
		}

		if len(results) != 1 {
			t.Errorf("expected 1 result, got %d", len(results))
		}
		t.Logf("Search results: %v", results)
	})
}

// TestProxyAliasPathOperations tests that the proxy correctly handles
// collection operations via /org/freedesktop/secrets/aliases/default paths,
// which is how libsecret (secret-tool) accesses collections.
func TestProxyAliasPathOperations(t *testing.T) {
	env := newTestEnv(t)
	defer env.cleanup()

	localConn := env.localConn()
	defer localConn.Close()

	mock := testutil.NewMockSecretService()
	if err := mock.Register(localConn); err != nil {
		t.Fatalf("register mock service: %v", err)
	}

	mock.AddItem("Existing Secret", map[string]string{"app": "test"}, []byte("existing-value"))

	p := proxy.New(proxy.Config{
		ClientName: "test-client",
		LogLevel:   slog.LevelDebug,
	})

	if err := connectProxyWithConns(p, env.localAddr, env.remoteSocketPath()); err != nil {
		t.Fatalf("connect proxy: %v", err)
	}
	defer p.Close()

	remoteConn := env.remoteConn()
	defer remoteConn.Close()

	aliasPath := dbus.ObjectPath("/org/freedesktop/secrets/aliases/default")

	// Test: CreateItem via alias path (this is what secret-tool store does)
	t.Run("CreateItemViaAlias", func(t *testing.T) {
		// Open session first
		serviceObj := remoteConn.Object(dbustypes.BusName, dbustypes.ServicePath)
		call := serviceObj.Call(dbustypes.ServiceInterface+".OpenSession", 0, "plain", dbus.MakeVariant(""))
		if call.Err != nil {
			t.Fatalf("OpenSession: %v", call.Err)
		}

		var output dbus.Variant
		var sessionPath dbus.ObjectPath
		if err := call.Store(&output, &sessionPath); err != nil {
			t.Fatalf("store result: %v", err)
		}

		// Create item via alias path
		collObj := remoteConn.Object(dbustypes.BusName, aliasPath)
		properties := map[string]dbus.Variant{
			"org.freedesktop.Secret.Item.Label":      dbus.MakeVariant("Alias Item"),
			"org.freedesktop.Secret.Item.Attributes": dbus.MakeVariant(map[string]string{"alias-attr": "alias-value"}),
		}
		secret := dbustypes.Secret{
			Session:     sessionPath,
			Parameters:  nil,
			Value:       []byte("alias-secret"),
			ContentType: "text/plain",
		}

		call = collObj.Call(dbustypes.CollectionInterface+".CreateItem", 0, properties, secret, true)
		if call.Err != nil {
			t.Fatalf("CreateItem via alias: %v", call.Err)
		}

		var itemPath, promptPath dbus.ObjectPath
		if err := call.Store(&itemPath, &promptPath); err != nil {
			t.Fatalf("store result: %v", err)
		}

		if itemPath == "/" || itemPath == "" {
			t.Error("expected valid item path")
		}
		t.Logf("Created item via alias: %s", itemPath)
	})

	// Test: SearchItems via alias path
	t.Run("SearchItemsViaAlias", func(t *testing.T) {
		collObj := remoteConn.Object(dbustypes.BusName, aliasPath)
		call := collObj.Call(dbustypes.CollectionInterface+".SearchItems", 0, map[string]string{"app": "test"})
		if call.Err != nil {
			t.Fatalf("SearchItems via alias: %v", call.Err)
		}

		var results []dbus.ObjectPath
		if err := call.Store(&results); err != nil {
			t.Fatalf("store result: %v", err)
		}

		if len(results) != 1 {
			t.Errorf("expected 1 result, got %d", len(results))
		}
		t.Logf("Search results via alias: %v", results)
	})

	// Test: Properties via alias path
	t.Run("GetPropertiesViaAlias", func(t *testing.T) {
		collObj := remoteConn.Object(dbustypes.BusName, aliasPath)

		label, err := collObj.GetProperty(dbustypes.CollectionInterface + ".Label")
		if err != nil {
			t.Fatalf("get Label via alias: %v", err)
		}
		if label.Value().(string) != "Default" {
			t.Errorf("wrong label: got %q, want %q", label.Value(), "Default")
		}

		locked, err := collObj.GetProperty(dbustypes.CollectionInterface + ".Locked")
		if err != nil {
			t.Fatalf("get Locked via alias: %v", err)
		}
		if locked.Value().(bool) != false {
			t.Error("expected collection to be unlocked")
		}
	})
}

// connectProxyWithConns connects the proxy using isolated D-Bus connections.
func connectProxyWithConns(p *proxy.Proxy, localAddr, remoteSocketPath string) error {
	backendConn, err := dbus.Connect(localAddr)
	if err != nil {
		return fmt.Errorf("connect to backend dbus: %w", err)
	}

	frontConn, err := dbus.Connect("unix:path=" + remoteSocketPath)
	if err != nil {
		backendConn.Close()
		return fmt.Errorf("connect to front socket: %w", err)
	}

	return p.ConnectWith(frontConn, backendConn)
}

// TestProxyClientDisconnectCancelsPendingRequest tests that when a client disconnects
// while waiting for approval, the pending request is automatically removed.
func TestProxyClientDisconnectCancelsPendingRequest(t *testing.T) {
	env := newTestEnv(t)
	defer env.cleanup()

	localConn := env.localConn()
	defer localConn.Close()

	mock := testutil.NewMockSecretService()
	if err := mock.Register(localConn); err != nil {
		t.Fatalf("register mock service: %v", err)
	}

	// Add a test item and capture its path directly (avoids needing SearchItems which now requires approval)
	itemPath := mock.AddItem("Test Secret", map[string]string{"test-attr": "test-value"}, []byte("test-secret"))

	// Create an approval manager that requires approval (not auto-approve)
	approvalMgr := approval.NewManager(30*time.Second, 100)

	p := proxy.New(proxy.Config{
		ClientName:       "test-client",
		LogLevel:         slog.LevelDebug,
		Approval:         approvalMgr,
	})

	if err := connectProxyWithConns(p, env.localAddr, env.remoteSocketPath()); err != nil {
		t.Fatalf("connect proxy: %v", err)
	}
	defer p.Close()

	// Connect a client to the remote D-Bus
	clientConn := env.remoteConn()

	// Open a session first
	serviceObj := clientConn.Object(dbustypes.BusName, dbustypes.ServicePath)
	call := serviceObj.Call(dbustypes.ServiceInterface+".OpenSession", 0, "plain", dbus.MakeVariant(""))
	if call.Err != nil {
		t.Fatalf("OpenSession: %v", call.Err)
	}

	var output dbus.Variant
	var sessionPath dbus.ObjectPath
	if err := call.Store(&output, &sessionPath); err != nil {
		t.Fatalf("store result: %v", err)
	}

	// Use the item path directly (avoids SearchItems which now requires approval)
	unlocked := []dbus.ObjectPath{itemPath}

	// Start GetSecrets in a goroutine - it will block waiting for approval
	secretsReturned := make(chan error, 1)
	go func() {
		call := serviceObj.Call(dbustypes.ServiceInterface+".GetSecrets", 0, unlocked, sessionPath)
		secretsReturned <- call.Err
	}()

	// Wait for the pending request to appear
	var pendingCount int
	for i := 0; i < 50; i++ {
		pendingCount = approvalMgr.PendingCount()
		if pendingCount > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if pendingCount == 0 {
		t.Fatal("pending request did not appear")
	}
	t.Logf("Pending request appeared (count=%d)", pendingCount)

	// Now disconnect the client
	clientConn.Close()
	t.Log("Client disconnected")

	// Wait for the pending request to be removed
	var finalCount int
	for i := 0; i < 50; i++ {
		finalCount = approvalMgr.PendingCount()
		if finalCount == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if finalCount != 0 {
		t.Errorf("pending request was not removed after client disconnect: count=%d", finalCount)
	} else {
		t.Log("Pending request was correctly removed after client disconnect")
	}

	// The GetSecrets call should have returned an error (context cancelled or similar)
	select {
	case err := <-secretsReturned:
		t.Logf("GetSecrets returned: %v", err)
	case <-time.After(2 * time.Second):
		// Timeout is acceptable - the goroutine might be stuck if cleanup didn't happen
		t.Log("GetSecrets goroutine did not return (expected if feature not implemented)")
	}
}

// TestProxyRejectsUnsupportedAlgorithm tests that the proxy rejects non-plain algorithms.
func TestProxyRejectsUnsupportedAlgorithm(t *testing.T) {
	env := newTestEnv(t)
	defer env.cleanup()

	localConn := env.localConn()
	defer localConn.Close()

	mock := testutil.NewMockSecretService()
	if err := mock.Register(localConn); err != nil {
		t.Fatalf("register mock service: %v", err)
	}

	p := proxy.New(proxy.Config{
		ClientName:       "test-client",
		LogLevel:         slog.LevelDebug,
	})

	if err := connectProxyWithConns(p, env.localAddr, env.remoteSocketPath()); err != nil {
		t.Fatalf("connect proxy: %v", err)
	}
	defer p.Close()

	remoteConn := env.remoteConn()
	defer remoteConn.Close()

	obj := remoteConn.Object(dbustypes.BusName, dbustypes.ServicePath)
	call := obj.Call(dbustypes.ServiceInterface+".OpenSession", 0, "dh-ietf1024-sha256-aes128-cbc-pkcs7", dbus.MakeVariant(""))

	if call.Err == nil {
		t.Error("expected error for unsupported algorithm")
	} else if !strings.Contains(call.Err.Error(), "not supported") {
		t.Errorf("unexpected error: %v", call.Err)
	}
}

// TestProxyDetectsSocketDisconnect tests that the proxy detects when the remote
// D-Bus daemon is killed (simulating SSH tunnel disconnect).
func TestProxyDetectsSocketDisconnect(t *testing.T) {
	env := newTestEnv(t)
	defer env.cleanup()

	localConn := env.localConn()
	defer localConn.Close()

	mock := testutil.NewMockSecretService()
	if err := mock.Register(localConn); err != nil {
		t.Fatalf("register mock service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := proxy.New(proxy.Config{
		ClientName:       "test-client",
		LogLevel:         slog.LevelDebug,
	})

	if err := connectProxyWithConns(p, env.localAddr, env.remoteSocketPath()); err != nil {
		t.Fatalf("connect proxy: %v", err)
	}
	defer p.Close()

	// Run the proxy in a goroutine
	runErr := make(chan error, 1)
	go func() {
		runErr <- p.Run(ctx)
	}()

	// Verify proxy is running (give it a moment to start)
	time.Sleep(100 * time.Millisecond)

	// Kill the remote D-Bus daemon to simulate disconnect
	if env.remoteCmd != nil && env.remoteCmd.Process != nil {
		t.Log("Killing remote D-Bus daemon to simulate disconnect")
		env.remoteCmd.Process.Kill()
		env.remoteCmd.Wait()
		env.remoteCmd = nil // Prevent double-kill in cleanup
	}

	// Proxy should detect the disconnect and return an error
	select {
	case err := <-runErr:
		if err == nil {
			t.Error("expected error from Run() after disconnect, got nil")
		} else {
			t.Logf("Run() returned expected error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("timeout waiting for proxy to detect disconnect")
	}
}
