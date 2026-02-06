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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := proxy.New(proxy.Config{
		RemoteSocketPath: env.remoteSocketPath(),
		ClientName:       "test-client",
		LogLevel:         slog.LevelDebug,
	})

	// Override the local connection to use our isolated D-Bus
	if err := connectProxyWithLocalAddr(p, ctx, env.localAddr, env.remoteSocketPath()); err != nil {
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := proxy.New(proxy.Config{
		RemoteSocketPath: env.remoteSocketPath(),
		ClientName:       "test-client",
		LogLevel:         slog.LevelDebug,
	})

	if err := connectProxyWithLocalAddr(p, ctx, env.localAddr, env.remoteSocketPath()); err != nil {
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := proxy.New(proxy.Config{
		RemoteSocketPath: env.remoteSocketPath(),
		ClientName:       "test-client",
		LogLevel:         slog.LevelDebug,
	})

	if err := connectProxyWithLocalAddr(p, ctx, env.localAddr, env.remoteSocketPath()); err != nil {
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

// connectProxyWithLocalAddr is a helper that connects the proxy using custom D-Bus addresses.
// This allows tests to use isolated D-Bus daemons instead of the system session bus.
func connectProxyWithLocalAddr(p *proxy.Proxy, ctx context.Context, localAddr, remoteSocketPath string) error {
	// We need to connect manually since proxy.Connect() uses dbus.ConnectSessionBus()
	// which would connect to the user's session bus.

	localConn, err := dbus.Connect(localAddr)
	if err != nil {
		return fmt.Errorf("connect to local dbus: %w", err)
	}

	remoteConn, err := dbus.Connect("unix:path=" + remoteSocketPath)
	if err != nil {
		localConn.Close()
		return fmt.Errorf("connect to remote socket: %w", err)
	}

	// Use reflection or a test-specific method to set connections
	// For now, we'll use a workaround: set DBUS_SESSION_BUS_ADDRESS and call Connect
	// But that's not ideal. Let's add a ConnectWithConns method instead.

	// Actually, the cleanest approach is to modify proxy to accept connections.
	// For now, let's use environment variable hack.
	origAddr := os.Getenv("DBUS_SESSION_BUS_ADDRESS")
	os.Setenv("DBUS_SESSION_BUS_ADDRESS", localAddr)
	defer os.Setenv("DBUS_SESSION_BUS_ADDRESS", origAddr)

	// Close the connections we made - Connect will make new ones
	localConn.Close()
	remoteConn.Close()

	return p.Connect(ctx)
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := proxy.New(proxy.Config{
		RemoteSocketPath: env.remoteSocketPath(),
		ClientName:       "test-client",
		LogLevel:         slog.LevelDebug,
	})

	if err := connectProxyWithLocalAddr(p, ctx, env.localAddr, env.remoteSocketPath()); err != nil {
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
