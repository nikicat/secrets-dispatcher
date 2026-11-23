package main

import (
	"fmt"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/dhcrypto"
	"github.com/nikicat/secrets-dispatcher/internal/proxy"
	"github.com/nikicat/secrets-dispatcher/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	require.NoError(e.t, err, "connect to local dbus")
	return conn
}

func (e *testEnv) remoteConn() *dbus.Conn {
	conn, err := dbus.Connect(e.remoteAddr)
	require.NoError(e.t, err, "connect to remote dbus")
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

const testClient approval.ClientName = "test-client"

// Registers a mock backend on the local bus until the test ends.
func (e *testEnv) registerMock() *testutil.MockSecretService {
	e.t.Helper()

	localConn := e.localConn()
	e.t.Cleanup(func() { localConn.Close() })

	mock := testutil.NewMockSecretService()
	require.NoError(e.t, mock.Register(localConn), "register mock service")
	return mock
}

// Puts a proxy deciding through the given manager between the two buses until
// the test ends.
func (e *testEnv) newProxy(mgr *approval.Manager) *proxy.Proxy {
	e.t.Helper()

	p := proxy.New(testClient, mgr, proxy.Config{})
	require.NoError(e.t, e.connectProxy(p), "connect proxy")
	e.t.Cleanup(func() { p.Close() })
	return p
}

// Registers a mock backend, puts a proxy deciding through the given manager in
// front of it, and returns the mock and a client connection on the remote bus.
// All three are closed when the test ends.
func (e *testEnv) startProxy(mgr *approval.Manager) (mock *testutil.MockSecretService, client *dbus.Conn) {
	e.t.Helper()

	mock = e.registerMock()
	e.newProxy(mgr)

	client = e.remoteConn()
	e.t.Cleanup(func() { client.Close() })
	return mock, client
}

// A proxy started by startApprovalProxy, with its manager's requestEvents.
type approvalProxy struct {
	mgr    *approval.Manager
	events *requestEvents
	mock   *testutil.MockSecretService
	client *dbus.Conn
}

// Starts a proxy as startProxy does, deciding through
// approval.NewManagerForTest(cfg).
func (e *testEnv) startApprovalProxy(cfg approval.ManagerConfig) approvalProxy {
	e.t.Helper()
	mgr := approval.NewManagerForTest(cfg)
	events := watchRequests(e.t, mgr)
	mock, client := e.startProxy(mgr)
	return approvalProxy{mgr: mgr, events: events, mock: mock, client: client}
}

// Opens a plain (unencrypted) session through serviceObj and returns its path.
func openPlainSession(t *testing.T, serviceObj dbus.BusObject) dbus.ObjectPath {
	t.Helper()

	call := serviceObj.Call(dbustypes.ServiceInterface.Member("OpenSession"), 0, "plain", dbus.MakeVariant(""))
	require.NoError(t, call.Err, "OpenSession")

	var output dbus.Variant
	var sessionPath dbus.ObjectPath
	require.NoError(t, call.Store(&output, &sessionPath), "store result")
	return sessionPath
}

// Fails the test unless the CreateItem call created an item, and returns the
// item's path.
func requireCreatedItem(t *testing.T, call *dbus.Call) dbus.ObjectPath {
	t.Helper()
	require.NoError(t, call.Err, "CreateItem")

	var item, prompt dbus.ObjectPath
	require.NoError(t, call.Store(&item, &prompt), "store CreateItem result")
	require.NotContains(t, []dbus.ObjectPath{dbustypes.NoObject, ""}, item, "expected a valid item path")
	return item
}

// Fails the test unless the error is a D-Bus error with the given name, and
// returns it.
func requireDBusError(t *testing.T, err error, name dbustypes.ErrorName) dbus.Error {
	t.Helper()
	var dbusErr dbus.Error
	require.ErrorAs(t, err, &dbusErr)
	require.Equal(t, string(name), dbusErr.Name)
	return dbusErr
}

// An approval.Observer that passes on the requests an approval.Manager opens
// and ends.
type requestEvents struct {
	approval.IgnoreEvents
	created chan approval.Pending
	ended   chan approval.HistoryEntry
}

// Subscribes a requestEvents to the given manager until the test ends.
func watchRequests(t *testing.T, mgr *approval.Manager) *requestEvents {
	ev := &requestEvents{created: make(chan approval.Pending, 16), ended: make(chan approval.HistoryEntry, 16)}
	mgr.Subscribe(ev)
	t.Cleanup(func() { mgr.Unsubscribe(ev) })
	return ev
}

func (e *requestEvents) RequestCreated(p approval.Pending)        { e.created <- p }
func (e *requestEvents) RequestEnded(entry approval.HistoryEntry) { e.ended <- entry }

// Returns the next request that waits for the user.
func (e *requestEvents) pending(t *testing.T) approval.Pending {
	t.Helper()
	return testutil.Recv(t, e.created, testutil.BusWait, "approval request did not appear")
}

// Returns the approval.Manager.History entry of the request with the given ID
// once it ends.
func (e *requestEvents) end(t *testing.T, id approval.RequestID) approval.HistoryEntry {
	t.Helper()
	return testutil.RecvMatching(t, e.ended, testutil.BusWait, func(entry approval.HistoryEntry) bool {
		return entry.Request.ID == id
	}, "approval request did not end")
}

// Makes the call in the background and returns the channel its result arrives on.
func startCall(call func() *dbus.Call) <-chan *dbus.Call {
	done := make(chan *dbus.Call, 1)
	go func() { done <- call() }()
	return done
}

// Returns the result of a call that startCall made, and fails the test when it
// does not return within testutil.BusWait.
func awaitCall(t *testing.T, done <-chan *dbus.Call) *dbus.Call {
	t.Helper()
	return testutil.Recv(t, done, testutil.BusWait, "D-Bus call did not return")
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
		require.NoError(t, err, "get Collections")

		require.IsType(t, []dbus.ObjectPath{}, variant.Value())
		collections := variant.Value().([]dbus.ObjectPath)
		assert.NotEmpty(t, collections, "expected at least one collection")
		t.Logf("Collections: %v", collections)
	})

	// Test: ReadAlias
	t.Run("ReadAlias", func(t *testing.T) {
		obj := remoteConn.Object(dbustypes.BusName, dbustypes.ServicePath)
		call := obj.Call(dbustypes.ServiceInterface.Member("ReadAlias"), 0, "default")
		require.NoError(t, call.Err, "ReadAlias")

		var collection dbus.ObjectPath
		require.NoError(t, call.Store(&collection), "store result")
		assert.NotContains(t, []dbus.ObjectPath{dbustypes.NoObject, ""}, collection, "expected non-empty collection path")
		t.Logf("Default collection: %s", collection)
	})

	// Test: SearchItems
	t.Run("SearchItems", func(t *testing.T) {
		obj := remoteConn.Object(dbustypes.BusName, dbustypes.ServicePath)
		call := obj.Call(dbustypes.ServiceInterface.Member("SearchItems"), 0, map[string]string{"test-attr": "test-value"})
		require.NoError(t, call.Err, "SearchItems")

		var unlocked, locked []dbus.ObjectPath
		require.NoError(t, call.Store(&unlocked, &locked), "store result")
		assert.Len(t, unlocked, 1)
		t.Logf("Found items: unlocked=%v, locked=%v", unlocked, locked)
	})

	// Test: OpenSession + GetSecrets
	t.Run("OpenSessionAndGetSecrets", func(t *testing.T) {
		obj := remoteConn.Object(dbustypes.BusName, dbustypes.ServicePath)

		sessionPath := openPlainSession(t, obj)
		t.Logf("Session: %s", sessionPath)

		// Search for items
		call := obj.Call(dbustypes.ServiceInterface.Member("SearchItems"), 0, map[string]string{"test-attr": "test-value"})
		require.NoError(t, call.Err, "SearchItems")

		var unlocked, locked []dbus.ObjectPath
		require.NoError(t, call.Store(&unlocked, &locked), "store result")

		require.NotEmpty(t, unlocked, "no items found")

		// Get secrets
		call = obj.Call(dbustypes.ServiceInterface.Member("GetSecrets"), 0, unlocked, sessionPath)
		require.NoError(t, call.Err, "GetSecrets")

		var secrets map[dbus.ObjectPath]dbustypes.Secret
		require.NoError(t, call.Store(&secrets), "store result")

		assert.Len(t, secrets, 1)

		for path, secret := range secrets {
			assert.Equal(t, "test-secret", string(secret.Value))
			t.Logf("Secret for %s: %q", path, secret.Value)
		}
	})

	// Test: Unlock (should passthrough)
	t.Run("Unlock", func(t *testing.T) {
		obj := remoteConn.Object(dbustypes.BusName, dbustypes.ServicePath)
		call := obj.Call(dbustypes.ServiceInterface.Member("Unlock"), 0, []dbus.ObjectPath{testutil.DefaultCollectionPath})
		require.NoError(t, call.Err, "Unlock")

		var unlocked []dbus.ObjectPath
		var prompt dbus.ObjectPath
		require.NoError(t, call.Store(&unlocked, &prompt), "store result")
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
		require.NoError(t, call.Err, "Item.GetSecret")

		var secret dbustypes.Secret
		require.NoError(t, call.Store(&secret), "store result")

		assert.Equal(t, "secret-value", string(secret.Value))
		t.Logf("Got secret: %q", secret.Value)
	})

	// Test: Item properties
	t.Run("ItemProperties", func(t *testing.T) {
		itemObj := remoteConn.Object(dbustypes.BusName, itemPath)

		label, err := itemObj.GetProperty(dbustypes.ItemLabel)
		require.NoError(t, err, "get Label")
		assert.Equal(t, "My Secret", label.Value())

		attrs, err := itemObj.GetProperty(dbustypes.ItemAttributes)
		require.NoError(t, err, "get Attributes")
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
		properties := dbustypes.ItemProperties{
			Label:      "New Item",
			Attributes: map[string]string{"new-attr": "new-value"},
		}.Variants()
		secret := dbustypes.Secret{
			Session:     sessionPath,
			Parameters:  nil,
			Value:       []byte("new-secret"),
			ContentType: "text/plain",
		}

		itemPath := requireCreatedItem(t, collObj.Call(dbustypes.CollectionInterface.Member("CreateItem"), 0, properties, secret, true))
		t.Logf("Created item: %s", itemPath)

		// Verify we can get the secret back
		itemObj := remoteConn.Object(dbustypes.BusName, itemPath)
		call := itemObj.Call(dbustypes.ItemInterface.Member("GetSecret"), 0, sessionPath)
		require.NoError(t, call.Err, "GetSecret")

		var retrievedSecret dbustypes.Secret
		require.NoError(t, call.Store(&retrievedSecret), "store result")

		assert.Equal(t, "new-secret", string(retrievedSecret.Value))
	})

	// Test: Collection.SearchItems
	t.Run("CollectionSearchItems", func(t *testing.T) {
		collObj := remoteConn.Object(dbustypes.BusName, testutil.DefaultCollectionPath)
		call := collObj.Call(dbustypes.CollectionInterface.Member("SearchItems"), 0, map[string]string{"new-attr": "new-value"})
		require.NoError(t, call.Err, "SearchItems")

		var results []dbus.ObjectPath
		require.NoError(t, call.Store(&results), "store result")

		assert.Len(t, results, 1)
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
		properties := dbustypes.ItemProperties{
			Label:      "Alias Item",
			Attributes: map[string]string{"alias-attr": "alias-value"},
		}.Variants()
		secret := dbustypes.Secret{
			Session:     sessionPath,
			Parameters:  nil,
			Value:       []byte("alias-secret"),
			ContentType: "text/plain",
		}

		itemPath := requireCreatedItem(t, collObj.Call(dbustypes.CollectionInterface.Member("CreateItem"), 0, properties, secret, true))
		t.Logf("Created item via alias: %s", itemPath)
	})

	// Test: SearchItems via alias path
	t.Run("SearchItemsViaAlias", func(t *testing.T) {
		collObj := remoteConn.Object(dbustypes.BusName, testutil.DefaultAliasPath)
		call := collObj.Call(dbustypes.CollectionInterface.Member("SearchItems"), 0, map[string]string{"app": "test"})
		require.NoError(t, call.Err, "SearchItems via alias")

		var results []dbus.ObjectPath
		require.NoError(t, call.Store(&results), "store result")

		assert.Len(t, results, 1)
		t.Logf("Search results via alias: %v", results)
	})

	// Test: Properties via alias path
	t.Run("GetPropertiesViaAlias", func(t *testing.T) {
		collObj := remoteConn.Object(dbustypes.BusName, testutil.DefaultAliasPath)

		label, err := collObj.GetProperty(dbustypes.CollectionLabel)
		require.NoError(t, err, "get Label via alias")
		assert.Equal(t, "Default", label.Value())

		locked, err := collObj.GetProperty(dbustypes.CollectionInterface.Member("Locked"))
		require.NoError(t, err, "get Locked via alias")
		assert.Equal(t, false, locked.Value(), "expected collection to be unlocked")
	})
}

// Approving a GetSecret does not approve a later Delete of the same item from
// the approval cache.
func TestProxyItemDeleteNoAutoApprove(t *testing.T) {
	env := newTestEnv(t)

	// Use approval window so GetSecret gets cached
	ap := env.startApprovalProxy(approval.ManagerConfig{ApprovalWindow: time.Minute})

	itemPath := ap.mock.AddItem("Cached Secret", map[string]string{"app": "test"}, []byte("secret"))

	// First: open session and GetSecret (approve it to populate cache)
	serviceObj := ap.client.Object(dbustypes.BusName, dbustypes.ServicePath)
	sessionPath := openPlainSession(t, serviceObj)

	// GetSecret blocks on approval
	gotSecret := startCall(func() *dbus.Call {
		return ap.client.Object(dbustypes.BusName, itemPath).Call(dbustypes.ItemInterface.Member("GetSecret"), 0, sessionPath)
	})

	require.NoError(t, ap.mgr.Approve(ap.events.pending(t).ID), "approve GetSecret")
	require.NoError(t, awaitCall(t, gotSecret).Err, "GetSecret")

	// Now try Delete — it should still require approval (not use cache)
	deleted := startCall(func() *dbus.Call {
		return ap.client.Object(dbustypes.BusName, itemPath).Call(dbustypes.ItemInterface.Member("Delete"), 0)
	})

	// Should see a new pending request
	req := ap.events.pending(t)
	assert.Equal(t, approval.RequestTypeDelete, req.Type)

	require.NoError(t, ap.mgr.Approve(req.ID), "approve Delete")
	assert.NoError(t, awaitCall(t, deleted).Err, "Delete returned error after approval")
}

// CreateItem with a mistyped Label fails with InvalidArgs before it asks for
// approval, and reaches neither the backend nor the pending list.
func TestProxyCreateItemRejectsMistypedLabel(t *testing.T) {
	env := newTestEnv(t)
	ap := env.startApprovalProxy(approval.ManagerConfig{})
	initialItemCount := ap.mock.ItemCount()

	sessionPath := openPlainSession(t, ap.client.Object(dbustypes.BusName, dbustypes.ServicePath))
	properties := map[string]dbus.Variant{dbustypes.ItemLabel: dbus.MakeVariant(42)}
	secret := dbustypes.Secret{Session: sessionPath, Value: []byte("secret-value"), ContentType: "text/plain"}
	call := ap.client.Object(dbustypes.BusName, testutil.DefaultCollectionPath).
		Call(dbustypes.CollectionInterface.Member("CreateItem"), 0, properties, secret, true)

	requireDBusError(t, call.Err, dbustypes.InvalidArgs)
	assert.Zero(t, ap.mgr.PendingCount())
	assert.Empty(t, ap.mgr.History())
	assert.Equal(t, initialItemCount, ap.mock.ItemCount())
}

// CreateItem with Chrome's dummy secret schema reaches neither the backend nor
// the pending list.
func TestProxyCreateItemIgnoresChromeDummy(t *testing.T) {
	env := newTestEnv(t)

	ap := env.startApprovalProxy(approval.ManagerConfig{IgnoreChromeDummy: true})

	initialItemCount := ap.mock.ItemCount()

	serviceObj := ap.client.Object(dbustypes.BusName, dbustypes.ServicePath)
	sessionPath := openPlainSession(t, serviceObj)

	// CreateItem with Chrome's dummy schema — should return immediately without blocking
	collObj := ap.client.Object(dbustypes.BusName, testutil.DefaultCollectionPath)
	properties := dbustypes.ItemProperties{
		Label: "Chrome Safe Storage",
		Attributes: map[string]string{
			"xdg:schema": "_chrome_dummy_schema_for_unlocking",
		},
	}.Variants()
	secret := dbustypes.Secret{
		Session:     sessionPath,
		Parameters:  nil,
		Value:       []byte("dummy"),
		ContentType: "text/plain",
	}

	call := collObj.Call(dbustypes.CollectionInterface.Member("CreateItem"), 0, properties, secret, true)
	require.NoError(t, call.Err, "CreateItem returned error")

	var itemPath, promptPath dbus.ObjectPath
	require.NoError(t, call.Store(&itemPath, &promptPath), "store result")

	// Ignored CreateItem returns ("/", "/")
	assert.Equal(t, dbustypes.NoObject, itemPath)

	// No pending requests should have been created
	assert.Zero(t, ap.mgr.PendingCount())

	// History should have one entry with "ignored" resolution
	history := ap.mgr.History()
	require.Len(t, history, 1)
	assert.Equal(t, approval.ResolutionIgnored, history[0].Resolution)

	// Backend should NOT have received the CreateItem (item count unchanged)
	assert.Equal(t, initialItemCount, ap.mock.ItemCount(), "backend item count changed")
}

// SetSecret on an item with Chrome's dummy secret schema succeeds without a
// prompt, and neither reaches the backend nor the pending list.
func TestProxySetSecretIgnoresChromeDummy(t *testing.T) {
	env := newTestEnv(t)

	ap := env.startApprovalProxy(approval.ManagerConfig{IgnoreChromeDummy: true})
	itemPath := ap.mock.AddItem("Chrome Safe Storage", map[string]string{"xdg:schema": "_chrome_dummy_schema_for_unlocking"}, []byte("old"))

	serviceObj := ap.client.Object(dbustypes.BusName, dbustypes.ServicePath)
	sessionPath := openPlainSession(t, serviceObj)

	secret := dbustypes.Secret{Session: sessionPath, Value: []byte("dummy"), ContentType: "text/plain"}
	require.NoError(t, ap.client.Object(dbustypes.BusName, itemPath).Call(dbustypes.ItemInterface.Member("SetSecret"), 0, secret).Err, "SetSecret returned error")

	assert.Zero(t, ap.mgr.PendingCount())
	history := ap.mgr.History()
	require.Len(t, history, 1)
	require.Equal(t, approval.ResolutionIgnored, history[0].Resolution)

	// The backend still holds the old value.
	got := startCall(func() *dbus.Call {
		return serviceObj.Call(dbustypes.ServiceInterface.Member("GetSecrets"), 0, []dbus.ObjectPath{itemPath}, sessionPath)
	})
	require.NoError(t, ap.mgr.Approve(ap.events.pending(t).ID), "approve")
	var secrets map[dbus.ObjectPath]dbustypes.Secret
	require.NoError(t, awaitCall(t, got).Store(&secrets), "GetSecrets")
	assert.Equal(t, "old", string(secrets[itemPath].Value), "backend value")
}

// Any algorithm other than "plain" and the DH one is rejected.
func TestProxyRejectsUnsupportedAlgorithm(t *testing.T) {
	env := newTestEnv(t)

	_, remoteConn := env.startProxy(approval.NewDisabledManager())

	obj := remoteConn.Object(dbustypes.BusName, dbustypes.ServicePath)
	call := obj.Call(dbustypes.ServiceInterface.Member("OpenSession"), 0, "dh-ietf2048-sha512-aes256-cbc-pkcs7", dbus.MakeVariant(""))

	assert.ErrorContains(t, call.Err, "not supported")
}

// Performs the client side of the dh-ietf1024 handshake against the proxy and
// returns the negotiated session and the remote session path.
func openDHSession(t *testing.T, serviceObj dbus.BusObject) (*dhcrypto.Session, dbus.ObjectPath) {
	t.Helper()

	kp, err := dhcrypto.GenerateKeyPair()
	require.NoError(t, err, "GenerateKeyPair")

	call := serviceObj.Call(dbustypes.ServiceInterface.Member("OpenSession"), 0,
		"dh-ietf1024-sha256-aes128-cbc-pkcs7", dbus.MakeVariant(kp.Public))
	require.NoError(t, call.Err, "OpenSession(dh)")

	var output dbus.Variant
	var sessionPath dbus.ObjectPath
	require.NoError(t, call.Store(&output, &sessionPath), "store OpenSession result")

	require.IsType(t, []byte{}, output.Value(), "OpenSession output")
	serverPub := output.Value().([]byte)
	session, err := kp.Derive(serverPub)
	require.NoError(t, err, "derive client session")
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
		require.NoError(t, call.Err, "Item.GetSecret")
		var secret dbustypes.Secret
		require.NoError(t, call.Store(&secret), "store secret")

		require.NotEqual(t, "dh-secret-value", string(secret.Value), "secret value was returned in plaintext over a DH session")
		assert.Len(t, secret.Parameters, 16, "IV in Parameters")
		plaintext, err := session.Decrypt(secret.Parameters, secret.Value)
		require.NoError(t, err, "decrypt secret")
		assert.Equal(t, "dh-secret-value", string(plaintext))
	})

	// Service.GetSecrets over a DH session encrypts every returned secret.
	t.Run("ServiceGetSecretsEncrypted", func(t *testing.T) {
		session, sessionPath := openDHSession(t, serviceObj)

		call := serviceObj.Call(dbustypes.ServiceInterface.Member("SearchItems"), 0, map[string]string{"app": "dh-app"})
		require.NoError(t, call.Err, "SearchItems")
		var unlocked, locked []dbus.ObjectPath
		require.NoError(t, call.Store(&unlocked, &locked), "store search result")
		require.NotEmpty(t, unlocked, "no items found")

		call = serviceObj.Call(dbustypes.ServiceInterface.Member("GetSecrets"), 0, unlocked, sessionPath)
		require.NoError(t, call.Err, "GetSecrets")
		var secrets map[dbus.ObjectPath]dbustypes.Secret
		require.NoError(t, call.Store(&secrets), "store secrets")
		require.NotEmpty(t, secrets, "expected at least one secret")
		for path, secret := range secrets {
			assert.Equal(t, sessionPath, secret.Session, "secret %s", path)
			plaintext, err := session.Decrypt(secret.Parameters, secret.Value)
			require.NoError(t, err, "decrypt secret %s", path)
			assert.Equal(t, "dh-secret-value", string(plaintext), "secret %s", path)
		}
	})

	// CreateItem over a DH session: the proxy decrypts before storing upstream,
	// so the value can be read back as plaintext over a plain session.
	t.Run("CreateItemEncrypted", func(t *testing.T) {
		session, sessionPath := openDHSession(t, serviceObj)

		ciphertext, iv, err := session.Encrypt([]byte("written-over-dh"))
		require.NoError(t, err, "encrypt")

		collObj := remoteConn.Object(dbustypes.BusName, testutil.DefaultCollectionPath)
		properties := dbustypes.ItemProperties{
			Label:      "DH Written",
			Attributes: map[string]string{"dh-written": "yes"},
		}.Variants()
		secret := dbustypes.Secret{
			Session:     sessionPath,
			Parameters:  iv,
			Value:       ciphertext,
			ContentType: "text/plain",
		}
		newItem := requireCreatedItem(t, collObj.Call(dbustypes.CollectionInterface.Member("CreateItem"), 0, properties, secret, true))

		// Read it back over a *plain* session: the backend must hold plaintext,
		// proving the proxy decrypted the DH-encrypted write before forwarding.
		plainSession := openPlainSession(t, serviceObj)

		itemObj := remoteConn.Object(dbustypes.BusName, newItem)
		call := itemObj.Call(dbustypes.ItemInterface.Member("GetSecret"), 0, plainSession)
		require.NoError(t, call.Err, "GetSecret(plain)")
		var got dbustypes.Secret
		require.NoError(t, call.Store(&got), "store secret")
		assert.Equal(t, "written-over-dh", string(got.Value))
	})
}

// Killing the remote D-Bus daemon, as a dropped SSH tunnel does, makes Run
// return an error.
func TestProxyDetectsSocketDisconnect(t *testing.T) {
	env := newTestEnv(t)

	env.registerMock()
	p := env.newProxy(approval.NewDisabledManager())
	ctx := t.Context()

	// Run the proxy in a goroutine
	runErr := make(chan error, 1)
	go func() {
		runErr <- p.Run(ctx)
	}()

	t.Log("Killing remote D-Bus daemon to simulate disconnect")
	env.killRemote()

	err := testutil.Recv(t, runErr, testutil.BusWait, "timeout waiting for proxy to detect disconnect")
	assert.Error(t, err, "Run() after disconnect")
}

// Each call that opens an approval request puts the resolved sender and the
// items it touches in the request, fails AccessDenied when the user denies it,
// is cancelled when the client disconnects, and succeeds when the user
// approves it.
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
			props := dbustypes.ItemProperties{Label: "New Item"}.Variants()
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
			ap := env.startApprovalProxy(approval.ManagerConfig{})
			item := ap.mock.AddItem("My Secret", map[string]string{"app": "test"}, []byte("value"))
			session := openPlainSession(t, ap.client.Object(dbustypes.BusName, dbustypes.ServicePath))

			call := func() *dbus.Call { return tc.call(ap.client, item, session) }
			done := startCall(call)
			req := ap.events.pending(t)
			assert.Equal(t, tc.typ, req.Type)
			if assert.Len(t, req.Items, 1) {
				assert.Equal(t, tc.label, req.Items[0].Label)
			}
			if assert.NotNil(t, req.SenderInfo.Sender) {
				assert.Equal(t, ap.client.Names()[0], string(*req.SenderInfo.Sender))
			}
			assert.Equal(t, testClient, req.SenderInfo.Client)
			require.NoError(t, ap.mgr.Deny(req.ID), "deny")
			requireDBusError(t, awaitCall(t, done).Err, dbustypes.AccessDenied)

			// A second request ends cancelled when its client disconnects.
			cancelled := startCall(call)
			id := ap.events.pending(t).ID
			ap.client.Close()
			assert.Equal(t, approval.ResolutionCancelled, ap.events.end(t, id).Resolution)
			assert.Error(t, awaitCall(t, cancelled).Err)

			// A third request, from a new client, succeeds once approved.
			client := env.remoteConn()
			t.Cleanup(func() { client.Close() })
			session = openPlainSession(t, client.Object(dbustypes.BusName, dbustypes.ServicePath))
			approved := startCall(func() *dbus.Call { return tc.call(client, item, session) })
			require.NoError(t, ap.mgr.Approve(ap.events.pending(t).ID), "approve")
			assert.NoError(t, awaitCall(t, approved).Err)
		})
	}
}

// SearchItems and Unlock never prompt. A deny rule fails them with
// AccessDenied. Otherwise they go upstream and are recorded as auto-approved.
func TestPassthroughCallsAgainstTrustRules(t *testing.T) {
	const deniedSchema = "org.epiphany.FormPassword"

	env := newTestEnv(t)
	ap := env.startApprovalProxy(approval.ManagerConfig{
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
	service := ap.client.Object(dbustypes.BusName, dbustypes.ServicePath)
	coll := ap.client.Object(dbustypes.BusName, testutil.DefaultCollectionPath)

	requireDenied := func(t *testing.T, call *dbus.Call, message string) {
		t.Helper()
		require.Equal(t, []any{message}, requireDBusError(t, call.Err, dbustypes.AccessDenied).Body)
	}
	requireLatest := func(t *testing.T, typ approval.RequestType, res approval.Resolution) {
		t.Helper()
		history := ap.mgr.History()
		require.NotEmpty(t, history)
		require.Equal(t, typ, history[0].Request.Type)
		require.Equal(t, res, history[0].Resolution)
		require.Zero(t, ap.mgr.PendingCount(), "requests pending")
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
		require.NoError(t, call.Err, "SearchItems")
		requireLatest(t, approval.RequestTypeSearch, approval.ResolutionAutoApproved)
	})
}
