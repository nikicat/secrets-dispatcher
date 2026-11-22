package main

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/dhcrypto"
	"github.com/nikicat/secrets-dispatcher/internal/proxy"
	"github.com/nikicat/secrets-dispatcher/internal/testutil"
)

// Two isolated D-Bus daemons: the local one holds the backend, the remote one
// the clients.
type testEnv struct {
	t          *testing.T
	localAddr  string
	remoteAddr string
	killRemote func()
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	env := &testEnv{t: t, localAddr: testutil.StartDBusDaemon(t)}
	env.remoteAddr, env.killRemote = testutil.StartKillableDBusDaemon(t)
	return env
}

func (e *testEnv) localConn() *dbus.Conn {
	conn, err := dbus.Connect(e.localAddr)
	if err != nil {
		e.t.Fatalf("connect to local dbus: %v", err)
	}
	return conn
}

func (e *testEnv) remoteConn() *dbus.Conn {
	conn, err := dbus.Connect(e.remoteAddr)
	if err != nil {
		e.t.Fatalf("connect to remote dbus: %v", err)
	}
	return conn
}

// Connects p to the remote bus as its front and the local bus as its backend.
func (e *testEnv) connectProxy(p *proxy.Proxy) error {
	backendConn, err := dbus.Connect(e.localAddr)
	if err != nil {
		return fmt.Errorf("connect to backend dbus: %w", err)
	}

	frontConn, err := dbus.Connect(e.remoteAddr)
	if err != nil {
		backendConn.Close()
		return fmt.Errorf("connect to front socket: %w", err)
	}

	return p.ConnectWith(frontConn, backendConn)
}

// Registers a mock backend on the local bus, puts a proxy deciding through
// mgr between the two buses, and returns the mock and a client connection on
// the remote bus. All three are closed when the test ends.
func (e *testEnv) startProxy(mgr *approval.Manager) (mock *testutil.MockSecretService, client *dbus.Conn) {
	e.t.Helper()

	localConn := e.localConn()
	e.t.Cleanup(func() { localConn.Close() })

	mock = testutil.NewMockSecretService()
	if err := mock.Register(localConn); err != nil {
		e.t.Fatalf("register mock service: %v", err)
	}

	p := proxy.New("test-client", mgr, proxy.Config{})
	if err := e.connectProxy(p); err != nil {
		e.t.Fatalf("connect proxy: %v", err)
	}
	e.t.Cleanup(func() { p.Close() })

	client = e.remoteConn()
	e.t.Cleanup(func() { client.Close() })
	return mock, client
}

// Opens a plain (unencrypted) session through serviceObj and returns its path.
func openPlainSession(t *testing.T, serviceObj dbus.BusObject) dbus.ObjectPath {
	t.Helper()

	call := serviceObj.Call(dbustypes.ServiceInterface.Member("OpenSession"), 0, "plain", dbus.MakeVariant(""))
	if call.Err != nil {
		t.Fatalf("OpenSession: %v", call.Err)
	}

	var output dbus.Variant
	var sessionPath dbus.ObjectPath
	if err := call.Store(&output, &sessionPath); err != nil {
		t.Fatalf("store result: %v", err)
	}
	return sessionPath
}

// Waits for a request to appear in mgr's pending list and returns its ID.
func waitPendingID(t *testing.T, mgr *approval.Manager) approval.RequestID {
	t.Helper()
	for range 50 {
		if reqs := mgr.List(); len(reqs) > 0 {
			return reqs[0].ID
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("approval request did not appear")
	return ""
}

func TestProxyBasicOperations(t *testing.T) {
	env := newTestEnv(t)

	mock, remoteConn := env.startProxy(approval.NewDisabledManager())

	// Add a test item
	mock.AddItem("Test Secret", map[string]string{"test-attr": "test-value"}, []byte("test-secret"))

	// Test: Get Collections property
	t.Run("GetCollections", func(t *testing.T) {
		obj := remoteConn.Object(dbustypes.BusName, dbustypes.ServicePath)
		variant, err := obj.GetProperty(dbustypes.ServiceInterface.Member("Collections"))
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
		call := obj.Call(dbustypes.ServiceInterface.Member("ReadAlias"), 0, "default")
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
		call := obj.Call(dbustypes.ServiceInterface.Member("SearchItems"), 0, map[string]string{"test-attr": "test-value"})
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

		sessionPath := openPlainSession(t, obj)
		t.Logf("Session: %s", sessionPath)

		// Search for items
		call := obj.Call(dbustypes.ServiceInterface.Member("SearchItems"), 0, map[string]string{"test-attr": "test-value"})
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
		call = obj.Call(dbustypes.ServiceInterface.Member("GetSecrets"), 0, unlocked, sessionPath)
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
		call := obj.Call(dbustypes.ServiceInterface.Member("Unlock"), 0, []dbus.ObjectPath{testutil.DefaultCollectionPath})
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

	mock, remoteConn := env.startProxy(approval.NewDisabledManager())

	itemPath := mock.AddItem("My Secret", map[string]string{"app": "test-app"}, []byte("secret-value"))

	// Test: Item.GetSecret
	t.Run("ItemGetSecret", func(t *testing.T) {
		serviceObj := remoteConn.Object(dbustypes.BusName, dbustypes.ServicePath)
		sessionPath := openPlainSession(t, serviceObj)

		// Get secret from item
		itemObj := remoteConn.Object(dbustypes.BusName, itemPath)
		call := itemObj.Call(dbustypes.ItemInterface.Member("GetSecret"), 0, sessionPath)
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

		label, err := itemObj.GetProperty(dbustypes.ItemLabel)
		if err != nil {
			t.Fatalf("get Label: %v", err)
		}
		if label.Value().(string) != "My Secret" {
			t.Errorf("wrong label: got %q", label.Value())
		}

		attrs, err := itemObj.GetProperty(dbustypes.ItemAttributes)
		if err != nil {
			t.Fatalf("get Attributes: %v", err)
		}
		t.Logf("Item attributes: %v", attrs.Value())
	})
}

func TestProxyCollectionOperations(t *testing.T) {
	env := newTestEnv(t)

	_, remoteConn := env.startProxy(approval.NewDisabledManager())

	// Test: Collection.CreateItem
	t.Run("CollectionCreateItem", func(t *testing.T) {
		serviceObj := remoteConn.Object(dbustypes.BusName, dbustypes.ServicePath)
		sessionPath := openPlainSession(t, serviceObj)

		// Create item
		collObj := remoteConn.Object(dbustypes.BusName, testutil.DefaultCollectionPath)
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

		call := collObj.Call(dbustypes.CollectionInterface.Member("CreateItem"), 0, properties, secret, true)
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
		call = itemObj.Call(dbustypes.ItemInterface.Member("GetSecret"), 0, sessionPath)
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
		collObj := remoteConn.Object(dbustypes.BusName, testutil.DefaultCollectionPath)
		call := collObj.Call(dbustypes.CollectionInterface.Member("SearchItems"), 0, map[string]string{"new-attr": "new-value"})
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

// libsecret (secret-tool) reaches collections through
// /org/freedesktop/secrets/aliases/default paths.
func TestProxyAliasPathOperations(t *testing.T) {
	env := newTestEnv(t)

	mock, remoteConn := env.startProxy(approval.NewDisabledManager())

	mock.AddItem("Existing Secret", map[string]string{"app": "test"}, []byte("existing-value"))

	// Test: CreateItem via alias path (this is what secret-tool store does)
	t.Run("CreateItemViaAlias", func(t *testing.T) {
		serviceObj := remoteConn.Object(dbustypes.BusName, dbustypes.ServicePath)
		sessionPath := openPlainSession(t, serviceObj)

		// Create item via alias path
		collObj := remoteConn.Object(dbustypes.BusName, testutil.DefaultAliasPath)
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

		call := collObj.Call(dbustypes.CollectionInterface.Member("CreateItem"), 0, properties, secret, true)
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
		collObj := remoteConn.Object(dbustypes.BusName, testutil.DefaultAliasPath)
		call := collObj.Call(dbustypes.CollectionInterface.Member("SearchItems"), 0, map[string]string{"app": "test"})
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
		collObj := remoteConn.Object(dbustypes.BusName, testutil.DefaultAliasPath)

		label, err := collObj.GetProperty(dbustypes.CollectionLabel)
		if err != nil {
			t.Fatalf("get Label via alias: %v", err)
		}
		if label.Value().(string) != "Default" {
			t.Errorf("wrong label: got %q, want %q", label.Value(), "Default")
		}

		locked, err := collObj.GetProperty(dbustypes.CollectionInterface.Member("Locked"))
		if err != nil {
			t.Fatalf("get Locked via alias: %v", err)
		}
		if locked.Value().(bool) != false {
			t.Error("expected collection to be unlocked")
		}
	})
}

func TestProxyItemDeleteRequiresApproval(t *testing.T) {
	env := newTestEnv(t)

	approvalMgr := approval.NewManager(approval.ManagerConfig{Timeout: 30 * time.Second, HistoryMax: 100})

	mock, remoteConn := env.startProxy(approvalMgr)

	itemPath := mock.AddItem("Delete Me", map[string]string{"app": "test"}, []byte("secret"))

	// Start Item.Delete in a goroutine — it should block waiting for approval
	deleteErr := make(chan error, 1)
	go func() {
		itemObj := remoteConn.Object(dbustypes.BusName, itemPath)
		call := itemObj.Call(dbustypes.ItemInterface.Member("Delete"), 0)
		deleteErr <- call.Err
	}()

	reqID := waitPendingID(t, approvalMgr)

	// Verify request type
	req := approvalMgr.GetPending(reqID)
	if req.Type != approval.RequestTypeDelete {
		t.Errorf("expected request type 'delete', got %q", req.Type)
	}

	// Approve the request
	if err := approvalMgr.Approve(reqID); err != nil {
		t.Fatalf("approve failed: %v", err)
	}

	// Delete should succeed
	select {
	case err := <-deleteErr:
		if err != nil {
			t.Errorf("Item.Delete returned error after approval: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for Item.Delete to complete")
	}
}

func TestProxyCollectionDeleteRequiresApproval(t *testing.T) {
	env := newTestEnv(t)

	approvalMgr := approval.NewManager(approval.ManagerConfig{Timeout: 30 * time.Second, HistoryMax: 100})

	_, remoteConn := env.startProxy(approvalMgr)

	collPath := testutil.DefaultCollectionPath

	// Start Collection.Delete in a goroutine — it should block waiting for approval
	deleteErr := make(chan error, 1)
	go func() {
		collObj := remoteConn.Object(dbustypes.BusName, collPath)
		call := collObj.Call(dbustypes.CollectionInterface.Member("Delete"), 0)
		deleteErr <- call.Err
	}()

	reqID := waitPendingID(t, approvalMgr)

	// Verify request type and that it captured the collection label
	req := approvalMgr.GetPending(reqID)
	if req.Type != approval.RequestTypeDelete {
		t.Errorf("expected request type 'delete', got %q", req.Type)
	}
	if len(req.Items) != 1 {
		t.Fatalf("expected 1 item in request, got %d", len(req.Items))
	}
	if req.Items[0].Label != "Default" {
		t.Errorf("expected collection label 'Default', got %q", req.Items[0].Label)
	}

	// Approve the request
	if err := approvalMgr.Approve(reqID); err != nil {
		t.Fatalf("approve failed: %v", err)
	}

	// Delete should succeed
	select {
	case err := <-deleteErr:
		if err != nil {
			t.Errorf("Collection.Delete returned error after approval: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for Collection.Delete to complete")
	}
}

// Approving a GetSecret does not approve a later Delete of the same item from
// the approval cache.
func TestProxyItemDeleteNoAutoApprove(t *testing.T) {
	env := newTestEnv(t)

	// Use approval window so GetSecret gets cached
	approvalMgr := approval.NewManager(approval.ManagerConfig{Timeout: 30 * time.Second, HistoryMax: 100, ApprovalWindow: time.Minute})

	mock, remoteConn := env.startProxy(approvalMgr)

	itemPath := mock.AddItem("Cached Secret", map[string]string{"app": "test"}, []byte("secret"))

	// First: open session and GetSecret (approve it to populate cache)
	serviceObj := remoteConn.Object(dbustypes.BusName, dbustypes.ServicePath)
	sessionPath := openPlainSession(t, serviceObj)

	// GetSecret blocks on approval
	getSecretDone := make(chan error, 1)
	go func() {
		itemObj := remoteConn.Object(dbustypes.BusName, itemPath)
		call := itemObj.Call(dbustypes.ItemInterface.Member("GetSecret"), 0, sessionPath)
		getSecretDone <- call.Err
	}()

	// Approve GetSecret
	reqID := waitPendingID(t, approvalMgr)
	approvalMgr.Approve(reqID)
	<-getSecretDone

	// Now try Delete — it should still require approval (not use cache)
	deleteErr := make(chan error, 1)
	go func() {
		itemObj := remoteConn.Object(dbustypes.BusName, itemPath)
		call := itemObj.Call(dbustypes.ItemInterface.Member("Delete"), 0)
		deleteErr <- call.Err
	}()

	// Should see a new pending request
	deleteReqID := waitPendingID(t, approvalMgr)

	req := approvalMgr.GetPending(deleteReqID)
	if req.Type != approval.RequestTypeDelete {
		t.Errorf("expected request type 'delete', got %q", req.Type)
	}

	approvalMgr.Approve(deleteReqID)

	select {
	case err := <-deleteErr:
		if err != nil {
			t.Errorf("Delete returned error after approval: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for Delete to complete")
	}
}

func TestProxySetSecretRequiresApproval(t *testing.T) {
	env := newTestEnv(t)

	approvalMgr := approval.NewManager(approval.ManagerConfig{Timeout: 30 * time.Second, HistoryMax: 100})

	mock, remoteConn := env.startProxy(approvalMgr)

	itemPath := mock.AddItem("My Secret", map[string]string{"app": "test"}, []byte("old-value"))

	serviceObj := remoteConn.Object(dbustypes.BusName, dbustypes.ServicePath)
	sessionPath := openPlainSession(t, serviceObj)

	// Start SetSecret in a goroutine — it should block
	setErr := make(chan error, 1)
	go func() {
		itemObj := remoteConn.Object(dbustypes.BusName, itemPath)
		newSecret := dbustypes.Secret{
			Session:     sessionPath,
			Value:       []byte("new-value"),
			ContentType: "text/plain",
		}
		call := itemObj.Call(dbustypes.ItemInterface.Member("SetSecret"), 0, newSecret)
		setErr <- call.Err
	}()

	reqID := waitPendingID(t, approvalMgr)

	req := approvalMgr.GetPending(reqID)
	if req.Type != approval.RequestTypeWrite {
		t.Errorf("expected request type 'write', got %q", req.Type)
	}

	approvalMgr.Approve(reqID)

	select {
	case err := <-setErr:
		if err != nil {
			t.Errorf("SetSecret returned error after approval: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for SetSecret to complete")
	}
}

func TestProxyCreateItemRequiresApproval(t *testing.T) {
	env := newTestEnv(t)

	approvalMgr := approval.NewManager(approval.ManagerConfig{Timeout: 30 * time.Second, HistoryMax: 100})

	_, remoteConn := env.startProxy(approvalMgr)

	serviceObj := remoteConn.Object(dbustypes.BusName, dbustypes.ServicePath)
	sessionPath := openPlainSession(t, serviceObj)

	// Start CreateItem in a goroutine — it should block
	createErr := make(chan error, 1)
	go func() {
		collObj := remoteConn.Object(dbustypes.BusName, testutil.DefaultCollectionPath)
		properties := map[string]dbus.Variant{
			"org.freedesktop.Secret.Item.Label":      dbus.MakeVariant("New Item"),
			"org.freedesktop.Secret.Item.Attributes": dbus.MakeVariant(map[string]string{"svc": "test"}),
		}
		secret := dbustypes.Secret{
			Session:     sessionPath,
			Value:       []byte("secret-value"),
			ContentType: "text/plain",
		}
		call := collObj.Call(dbustypes.CollectionInterface.Member("CreateItem"), 0, properties, secret, true)
		createErr <- call.Err
	}()

	reqID := waitPendingID(t, approvalMgr)

	req := approvalMgr.GetPending(reqID)
	if req.Type != approval.RequestTypeWrite {
		t.Errorf("expected request type 'write', got %q", req.Type)
	}
	if len(req.Items) != 1 || req.Items[0].Label != "New Item" {
		t.Errorf("expected item label 'New Item', got %v", req.Items)
	}

	approvalMgr.Approve(reqID)

	select {
	case err := <-createErr:
		if err != nil {
			t.Errorf("CreateItem returned error after approval: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for CreateItem to complete")
	}
}

// CreateItem with Chrome's dummy secret schema reaches neither the backend nor
// the pending list.
func TestProxyCreateItemIgnoresChromeDummy(t *testing.T) {
	env := newTestEnv(t)

	approvalMgr := approval.NewManager(approval.ManagerConfig{
		Timeout:           30 * time.Second,
		HistoryMax:        100,
		IgnoreChromeDummy: true,
	})

	mock, remoteConn := env.startProxy(approvalMgr)

	initialItemCount := mock.ItemCount()

	serviceObj := remoteConn.Object(dbustypes.BusName, dbustypes.ServicePath)
	sessionPath := openPlainSession(t, serviceObj)

	// CreateItem with Chrome's dummy schema — should return immediately without blocking
	collObj := remoteConn.Object(dbustypes.BusName, testutil.DefaultCollectionPath)
	properties := map[string]dbus.Variant{
		"org.freedesktop.Secret.Item.Label": dbus.MakeVariant("Chrome Safe Storage"),
		"org.freedesktop.Secret.Item.Attributes": dbus.MakeVariant(map[string]string{
			"xdg:schema": "_chrome_dummy_schema_for_unlocking",
		}),
	}
	secret := dbustypes.Secret{
		Session:     sessionPath,
		Parameters:  nil,
		Value:       []byte("dummy"),
		ContentType: "text/plain",
	}

	call := collObj.Call(dbustypes.CollectionInterface.Member("CreateItem"), 0, properties, secret, true)
	if call.Err != nil {
		t.Fatalf("CreateItem returned error: %v", call.Err)
	}

	var itemPath, promptPath dbus.ObjectPath
	if err := call.Store(&itemPath, &promptPath); err != nil {
		t.Fatalf("store result: %v", err)
	}

	// Ignored CreateItem returns ("/", "/")
	if itemPath != "/" {
		t.Errorf("expected item path '/', got %q", itemPath)
	}

	// No pending requests should have been created
	if count := approvalMgr.PendingCount(); count != 0 {
		t.Errorf("expected 0 pending requests, got %d", count)
	}

	// History should have one entry with "ignored" resolution
	history := approvalMgr.History()
	if len(history) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(history))
	}
	if history[0].Resolution != approval.ResolutionIgnored {
		t.Errorf("expected resolution %q, got %q", approval.ResolutionIgnored, history[0].Resolution)
	}

	// Backend should NOT have received the CreateItem (item count unchanged)
	if count := mock.ItemCount(); count != initialItemCount {
		t.Errorf("expected backend item count %d (unchanged), got %d", initialItemCount, count)
	}
}

// SetSecret on an item with Chrome's dummy secret schema succeeds without a
// prompt, and neither reaches the backend nor the pending list.
func TestProxySetSecretIgnoresChromeDummy(t *testing.T) {
	env := newTestEnv(t)

	approvalMgr := approval.NewManager(approval.ManagerConfig{
		Timeout:           30 * time.Second,
		HistoryMax:        100,
		IgnoreChromeDummy: true,
	})

	mock, remoteConn := env.startProxy(approvalMgr)
	itemPath := mock.AddItem("Chrome Safe Storage", map[string]string{"xdg:schema": "_chrome_dummy_schema_for_unlocking"}, []byte("old"))

	serviceObj := remoteConn.Object(dbustypes.BusName, dbustypes.ServicePath)
	sessionPath := openPlainSession(t, serviceObj)

	secret := dbustypes.Secret{Session: sessionPath, Value: []byte("dummy"), ContentType: "text/plain"}
	if err := remoteConn.Object(dbustypes.BusName, itemPath).Call(dbustypes.ItemInterface.Member("SetSecret"), 0, secret).Err; err != nil {
		t.Fatalf("SetSecret returned error: %v", err)
	}

	if count := approvalMgr.PendingCount(); count != 0 {
		t.Errorf("expected 0 pending requests, got %d", count)
	}
	history := approvalMgr.History()
	if len(history) != 1 || history[0].Resolution != approval.ResolutionIgnored {
		t.Fatalf("expected one ignored history entry, got %+v", history)
	}

	// The backend still holds the old value.
	got := make(chan *dbus.Call, 1)
	go func() {
		got <- serviceObj.Call(dbustypes.ServiceInterface.Member("GetSecrets"), 0, []dbus.ObjectPath{itemPath}, sessionPath)
	}()
	approvalMgr.Approve(waitPendingID(t, approvalMgr))
	var secrets map[dbus.ObjectPath]dbustypes.Secret
	if err := (<-got).Store(&secrets); err != nil {
		t.Fatalf("GetSecrets: %v", err)
	}
	if v := string(secrets[itemPath].Value); v != "old" {
		t.Errorf("expected backend value %q, got %q", "old", v)
	}
}

// Any algorithm other than "plain" and the DH one is rejected.
func TestProxyRejectsUnsupportedAlgorithm(t *testing.T) {
	env := newTestEnv(t)

	_, remoteConn := env.startProxy(approval.NewDisabledManager())

	obj := remoteConn.Object(dbustypes.BusName, dbustypes.ServicePath)
	call := obj.Call(dbustypes.ServiceInterface.Member("OpenSession"), 0, "dh-ietf2048-sha512-aes256-cbc-pkcs7", dbus.MakeVariant(""))

	if call.Err == nil {
		t.Error("expected error for unsupported algorithm")
	} else if !strings.Contains(call.Err.Error(), "not supported") {
		t.Errorf("unexpected error: %v", call.Err)
	}
}

// Performs the client side of the dh-ietf1024 handshake against the proxy and
// returns the negotiated session and the remote session path.
func openDHSession(t *testing.T, serviceObj dbus.BusObject) (*dhcrypto.Session, dbus.ObjectPath) {
	t.Helper()

	kp, err := dhcrypto.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	call := serviceObj.Call(dbustypes.ServiceInterface.Member("OpenSession"), 0,
		"dh-ietf1024-sha256-aes128-cbc-pkcs7", dbus.MakeVariant(kp.Public))
	if call.Err != nil {
		t.Fatalf("OpenSession(dh): %v", call.Err)
	}

	var output dbus.Variant
	var sessionPath dbus.ObjectPath
	if err := call.Store(&output, &sessionPath); err != nil {
		t.Fatalf("store OpenSession result: %v", err)
	}

	serverPub, ok := output.Value().([]byte)
	if !ok {
		t.Fatalf("OpenSession output is not a byte array: %T", output.Value())
	}
	session, err := kp.Derive(serverPub)
	if err != nil {
		t.Fatalf("derive client session: %v", err)
	}
	return session, sessionPath
}

// Secrets read over a dh-ietf1024-sha256-aes128-cbc-pkcs7 session arrive
// encrypted and decrypt to the stored plaintext, and secrets written over one
// reach the plain upstream backend decrypted.
func TestProxyDHSession(t *testing.T) {
	env := newTestEnv(t)

	mock, remoteConn := env.startProxy(approval.NewDisabledManager())

	itemPath := mock.AddItem("DH Secret", map[string]string{"app": "dh-app"}, []byte("dh-secret-value"))

	serviceObj := remoteConn.Object(dbustypes.BusName, dbustypes.ServicePath)

	// Item.GetSecret over a DH session returns ciphertext that decrypts to the
	// stored plaintext.
	t.Run("ItemGetSecretEncrypted", func(t *testing.T) {
		session, sessionPath := openDHSession(t, serviceObj)

		itemObj := remoteConn.Object(dbustypes.BusName, itemPath)
		call := itemObj.Call(dbustypes.ItemInterface.Member("GetSecret"), 0, sessionPath)
		if call.Err != nil {
			t.Fatalf("Item.GetSecret: %v", call.Err)
		}
		var secret dbustypes.Secret
		if err := call.Store(&secret); err != nil {
			t.Fatalf("store secret: %v", err)
		}

		if string(secret.Value) == "dh-secret-value" {
			t.Fatal("secret value was returned in plaintext over a DH session")
		}
		if len(secret.Parameters) != 16 {
			t.Errorf("expected 16-byte IV in Parameters, got %d bytes", len(secret.Parameters))
		}
		plaintext, err := session.Decrypt(secret.Parameters, secret.Value)
		if err != nil {
			t.Fatalf("decrypt secret: %v", err)
		}
		if string(plaintext) != "dh-secret-value" {
			t.Errorf("decrypted secret = %q, want %q", plaintext, "dh-secret-value")
		}
	})

	// Service.GetSecrets over a DH session encrypts every returned secret.
	t.Run("ServiceGetSecretsEncrypted", func(t *testing.T) {
		session, sessionPath := openDHSession(t, serviceObj)

		call := serviceObj.Call(dbustypes.ServiceInterface.Member("SearchItems"), 0, map[string]string{"app": "dh-app"})
		if call.Err != nil {
			t.Fatalf("SearchItems: %v", call.Err)
		}
		var unlocked, locked []dbus.ObjectPath
		if err := call.Store(&unlocked, &locked); err != nil {
			t.Fatalf("store search result: %v", err)
		}
		if len(unlocked) == 0 {
			t.Fatal("no items found")
		}

		call = serviceObj.Call(dbustypes.ServiceInterface.Member("GetSecrets"), 0, unlocked, sessionPath)
		if call.Err != nil {
			t.Fatalf("GetSecrets: %v", call.Err)
		}
		var secrets map[dbus.ObjectPath]dbustypes.Secret
		if err := call.Store(&secrets); err != nil {
			t.Fatalf("store secrets: %v", err)
		}
		if len(secrets) == 0 {
			t.Fatal("expected at least one secret")
		}
		for path, secret := range secrets {
			if secret.Session != sessionPath {
				t.Errorf("secret %s: session = %s, want %s", path, secret.Session, sessionPath)
			}
			plaintext, err := session.Decrypt(secret.Parameters, secret.Value)
			if err != nil {
				t.Fatalf("decrypt secret %s: %v", path, err)
			}
			if string(plaintext) != "dh-secret-value" {
				t.Errorf("secret %s: decrypted = %q, want %q", path, plaintext, "dh-secret-value")
			}
		}
	})

	// CreateItem over a DH session: the proxy decrypts before storing upstream,
	// so the value can be read back as plaintext over a plain session.
	t.Run("CreateItemEncrypted", func(t *testing.T) {
		session, sessionPath := openDHSession(t, serviceObj)

		ciphertext, iv, err := session.Encrypt([]byte("written-over-dh"))
		if err != nil {
			t.Fatalf("encrypt: %v", err)
		}

		collObj := remoteConn.Object(dbustypes.BusName, testutil.DefaultCollectionPath)
		properties := map[string]dbus.Variant{
			"org.freedesktop.Secret.Item.Label":      dbus.MakeVariant("DH Written"),
			"org.freedesktop.Secret.Item.Attributes": dbus.MakeVariant(map[string]string{"dh-written": "yes"}),
		}
		secret := dbustypes.Secret{
			Session:     sessionPath,
			Parameters:  iv,
			Value:       ciphertext,
			ContentType: "text/plain",
		}
		call := collObj.Call(dbustypes.CollectionInterface.Member("CreateItem"), 0, properties, secret, true)
		if call.Err != nil {
			t.Fatalf("CreateItem: %v", call.Err)
		}
		var newItem, prompt dbus.ObjectPath
		if err := call.Store(&newItem, &prompt); err != nil {
			t.Fatalf("store CreateItem result: %v", err)
		}
		if newItem == "/" || newItem == "" {
			t.Fatal("expected a valid item path")
		}

		// Read it back over a *plain* session: the backend must hold plaintext,
		// proving the proxy decrypted the DH-encrypted write before forwarding.
		plainSession := openPlainSession(t, serviceObj)

		itemObj := remoteConn.Object(dbustypes.BusName, newItem)
		call = itemObj.Call(dbustypes.ItemInterface.Member("GetSecret"), 0, plainSession)
		if call.Err != nil {
			t.Fatalf("GetSecret(plain): %v", call.Err)
		}
		var got dbustypes.Secret
		if err := call.Store(&got); err != nil {
			t.Fatalf("store secret: %v", err)
		}
		if string(got.Value) != "written-over-dh" {
			t.Errorf("backend stored %q, want plaintext %q", got.Value, "written-over-dh")
		}
	})
}

// Killing the remote D-Bus daemon, as a dropped SSH tunnel does, makes Run
// return an error.
func TestProxyDetectsSocketDisconnect(t *testing.T) {
	env := newTestEnv(t)

	localConn := env.localConn()
	defer localConn.Close()

	mock := testutil.NewMockSecretService()
	if err := mock.Register(localConn); err != nil {
		t.Fatalf("register mock service: %v", err)
	}

	ctx := t.Context()

	p := proxy.New("test-client", approval.NewDisabledManager(), proxy.Config{})

	if err := env.connectProxy(p); err != nil {
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

	t.Log("Killing remote D-Bus daemon to simulate disconnect")
	env.killRemote()

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

// Each call that opens an approval request puts the resolved sender and the
// items it touches in the request, fails AccessDenied when the user denies it,
// and is cancelled when the client disconnects.
func TestProxyApprovalRequestCalls(t *testing.T) {
	type approvalCall struct {
		name  string
		typ   approval.RequestType
		label string
		call  func(client *dbus.Conn, item, session dbus.ObjectPath) *dbus.Call
	}
	secret := func(session dbus.ObjectPath) dbustypes.Secret {
		return dbustypes.Secret{Session: session, Value: []byte("v"), ContentType: "text/plain"}
	}
	calls := []approvalCall{
		{"CollectionDelete", approval.RequestTypeDelete, "Default", func(c *dbus.Conn, _, _ dbus.ObjectPath) *dbus.Call {
			return c.Object(dbustypes.BusName, testutil.DefaultCollectionPath).Call(dbustypes.CollectionInterface.Member("Delete"), 0)
		}},
		{"CreateItem", approval.RequestTypeWrite, "New Item", func(c *dbus.Conn, _, session dbus.ObjectPath) *dbus.Call {
			props := map[string]dbus.Variant{dbustypes.ItemLabel: dbus.MakeVariant("New Item")}
			return c.Object(dbustypes.BusName, testutil.DefaultCollectionPath).Call(dbustypes.CollectionInterface.Member("CreateItem"), 0, props, secret(session), true)
		}},
		{"ItemDelete", approval.RequestTypeDelete, "My Secret", func(c *dbus.Conn, item, _ dbus.ObjectPath) *dbus.Call {
			return c.Object(dbustypes.BusName, item).Call(dbustypes.ItemInterface.Member("Delete"), 0)
		}},
		{"GetSecret", approval.RequestTypeGetSecret, "My Secret", func(c *dbus.Conn, item, session dbus.ObjectPath) *dbus.Call {
			return c.Object(dbustypes.BusName, item).Call(dbustypes.ItemInterface.Member("GetSecret"), 0, session)
		}},
		{"SetSecret", approval.RequestTypeWrite, "My Secret", func(c *dbus.Conn, item, session dbus.ObjectPath) *dbus.Call {
			return c.Object(dbustypes.BusName, item).Call(dbustypes.ItemInterface.Member("SetSecret"), 0, secret(session))
		}},
		{"GetSecrets", approval.RequestTypeGetSecret, "My Secret", func(c *dbus.Conn, item, session dbus.ObjectPath) *dbus.Call {
			return c.Object(dbustypes.BusName, dbustypes.ServicePath).Call(dbustypes.ServiceInterface.Member("GetSecrets"), 0, []dbus.ObjectPath{item}, session)
		}},
	}

	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			env := newTestEnv(t)
			approvalMgr := approval.NewManager(approval.ManagerConfig{Timeout: 30 * time.Second, HistoryMax: 100})
			mock, client := env.startProxy(approvalMgr)
			item := mock.AddItem("My Secret", map[string]string{"app": "test"}, []byte("value"))
			session := openPlainSession(t, client.Object(dbustypes.BusName, dbustypes.ServicePath))

			done := make(chan *dbus.Call, 1)
			go func() { done <- tc.call(client, item, session) }()
			req := approvalMgr.GetPending(waitPendingID(t, approvalMgr))
			if req.Type != tc.typ {
				t.Errorf("request type %q, want %q", req.Type, tc.typ)
			}
			if len(req.Items) != 1 || req.Items[0].Label != tc.label {
				t.Errorf("request items %+v, want one labelled %q", req.Items, tc.label)
			}
			if req.SenderInfo.Sender == nil || string(*req.SenderInfo.Sender) != client.Names()[0] {
				t.Errorf("request sender %v, want %s", req.SenderInfo.Sender, client.Names()[0])
			}
			if req.SenderInfo.Client != "test-client" {
				t.Errorf("request client %q, want test-client", req.SenderInfo.Client)
			}
			if err := approvalMgr.Deny(req.ID); err != nil {
				t.Fatalf("deny: %v", err)
			}
			var dbusErr dbus.Error
			if call := <-done; !errors.As(call.Err, &dbusErr) || dbusErr.Name != string(dbustypes.AccessDenied) {
				t.Fatalf("got %v, want AccessDenied", call.Err)
			}

			// A second request ends cancelled when its client disconnects.
			go func() { done <- tc.call(client, item, session) }()
			id := waitPendingID(t, approvalMgr)
			client.Close()
			for range 100 {
				if e := approvalMgr.GetHistoryEntry(id); e != nil {
					if e.Resolution != approval.ResolutionCancelled {
						t.Fatalf("resolution %q, want cancelled", e.Resolution)
					}
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatal("request was not cancelled after the client disconnected")
		})
	}
}

// SearchItems and Unlock never prompt. A deny rule fails them with
// AccessDenied. Otherwise they go upstream and are recorded as auto-approved.
func TestPassthroughCallsAgainstTrustRules(t *testing.T) {
	const deniedSchema = "org.epiphany.FormPassword"

	env := newTestEnv(t)
	approvalMgr := approval.NewManager(approval.ManagerConfig{
		Timeout:    30 * time.Second,
		HistoryMax: 100,
		TrustRules: []approval.TrustRule{
			{
				Name:             "deny-search",
				Action:           approval.RuleDeny,
				RequestTypes:     []approval.RequestType{approval.RequestTypeSearch},
				SearchAttributes: map[string]approval.Glob{"xdg:schema": approval.MustGlob(deniedSchema)},
			},
			{
				Name:         "deny-unlock",
				Action:       approval.RuleDeny,
				RequestTypes: []approval.RequestType{approval.RequestTypeUnlock},
			},
		},
	})
	_, remoteConn := env.startProxy(approvalMgr)
	service := remoteConn.Object(dbustypes.BusName, dbustypes.ServicePath)
	coll := remoteConn.Object(dbustypes.BusName, testutil.DefaultCollectionPath)

	requireDenied := func(t *testing.T, call *dbus.Call, message string) {
		t.Helper()
		var dbusErr dbus.Error
		if !errors.As(call.Err, &dbusErr) {
			t.Fatalf("expected a D-Bus error, got %v", call.Err)
		}
		if dbusErr.Name != string(dbustypes.AccessDenied) || len(dbusErr.Body) != 1 || dbusErr.Body[0] != message {
			t.Fatalf("got %s %v, want AccessDenied %q", dbusErr.Name, dbusErr.Body, message)
		}
	}
	requireLatest := func(t *testing.T, typ approval.RequestType, res approval.Resolution) {
		t.Helper()
		history := approvalMgr.History()
		if len(history) == 0 || history[0].Request.Type != typ || history[0].Resolution != res {
			t.Fatalf("latest history entry %+v, want %s %s", history, typ, res)
		}
		if n := approvalMgr.PendingCount(); n != 0 {
			t.Fatalf("%d requests pending, want none", n)
		}
	}

	denied := map[string]string{"xdg:schema": deniedSchema}
	t.Run("ServiceSearchDenied", func(t *testing.T) {
		requireDenied(t, service.Call(dbustypes.ServiceInterface.Member("SearchItems"), 0, denied), "access denied by trust rule: deny-search")
		requireLatest(t, approval.RequestTypeSearch, approval.ResolutionDenied)
	})
	t.Run("CollectionSearchDenied", func(t *testing.T) {
		requireDenied(t, coll.Call(dbustypes.CollectionInterface.Member("SearchItems"), 0, denied), "access denied by trust rule: deny-search")
		requireLatest(t, approval.RequestTypeSearch, approval.ResolutionDenied)
	})
	t.Run("UnlockDenied", func(t *testing.T) {
		requireDenied(t, service.Call(dbustypes.ServiceInterface.Member("Unlock"), 0, []dbus.ObjectPath{testutil.DefaultCollectionPath}), "access denied by trust rule: deny-unlock")
		requireLatest(t, approval.RequestTypeUnlock, approval.ResolutionDenied)
	})
	t.Run("SearchAllowed", func(t *testing.T) {
		call := service.Call(dbustypes.ServiceInterface.Member("SearchItems"), 0, map[string]string{"xdg:schema": "org.gnome.keyring.Note"})
		if call.Err != nil {
			t.Fatalf("SearchItems: %v", call.Err)
		}
		requireLatest(t, approval.RequestTypeSearch, approval.ResolutionAutoApproved)
	})
}
