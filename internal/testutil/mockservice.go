// Package testutil provides test utilities including a mock Secret Service.
package testutil

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/godbus/dbus/v5"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
)

// MockSecretService is a minimal Secret Service implementation for testing.
type MockSecretService struct {
	conn *dbus.Conn

	mu           sync.RWMutex
	items        map[dbus.ObjectPath]*MockItem
	sessions     map[dbus.ObjectPath]bool
	prompts      map[dbus.ObjectPath][]dbus.ObjectPath // the objects each prompt unlocks
	locked       bool                                  // the default collection (and all its items) is locked
	lastWindowID string
	sessionCtr   atomic.Uint64
	itemCtr      atomic.Uint64
	promptCtr    atomic.Uint64
}

// MockItem represents a stored secret item.
type MockItem struct {
	Path        dbus.ObjectPath
	Label       string
	Attributes  dbustypes.Attributes
	Secret      []byte
	ContentType string
}

// NewMockSecretService creates a new mock service.
func NewMockSecretService() *MockSecretService {
	return &MockSecretService{
		items:    make(map[dbus.ObjectPath]*MockItem),
		sessions: make(map[dbus.ObjectPath]bool),
		prompts:  make(map[dbus.ObjectPath][]dbus.ObjectPath),
	}
}

// The path of the one collection that the mock serves, which its default alias
// names too.
const DefaultCollectionPath dbus.ObjectPath = dbustypes.CollectionsPath + "/default"

// The path of the mock's default alias.
const DefaultAliasPath dbus.ObjectPath = dbustypes.AliasesPath + "/default"

// Exports the mock's service, collections, items, sessions and prompts on the
// given connection. Objects that calls create are served by subtrees, because an
// Export from inside a call races godbus's object table under go test -race (2026-11-22).
func (m *MockSecretService) Register(conn *dbus.Conn) error {
	m.conn = conn

	// Export Service interface
	if err := conn.Export(m, dbustypes.ServicePath, string(dbustypes.ServiceInterface)); err != nil {
		return fmt.Errorf("export Service: %w", err)
	}

	// Export Properties interface
	if err := conn.Export(m, dbustypes.ServicePath, string(dbustypes.PropertiesInterface)); err != nil {
		return fmt.Errorf("export Properties: %w", err)
	}

	if err := conn.ExportSubtree(mockSessions{m}, dbustypes.SessionsPath, string(dbustypes.SessionInterface)); err != nil {
		return fmt.Errorf("export sessions: %w", err)
	}
	if err := conn.ExportSubtree(mockPrompts{m}, dbustypes.PromptsPath, string(dbustypes.PromptInterface)); err != nil {
		return fmt.Errorf("export prompts: %w", err)
	}

	// Export Introspectable
	if err := conn.Export(introspectable{m.Introspect}, dbustypes.ServicePath, string(dbustypes.IntrospectableInterface)); err != nil {
		return fmt.Errorf("export Introspectable: %w", err)
	}

	// Serve the default collection at its alias path too, as gopass-secret-service
	// does. gnome-keyring answers UnknownMethod there, and libsecret then calls
	// CreateCollection.
	for _, collPath := range []dbus.ObjectPath{DefaultCollectionPath, DefaultAliasPath} {
		coll := &mockCollection{service: m, path: collPath}
		if err := conn.Export(coll, collPath, string(dbustypes.CollectionInterface)); err != nil {
			return fmt.Errorf("export Collection at %s: %w", collPath, err)
		}
		if err := conn.ExportSubtree(mockItems{m}, collPath, string(dbustypes.ItemInterface)); err != nil {
			return fmt.Errorf("export items under %s: %w", collPath, err)
		}
		if err := conn.ExportSubtree(collectionTree{coll}, collPath, string(dbustypes.PropertiesInterface)); err != nil {
			return fmt.Errorf("export Properties under %s: %w", collPath, err)
		}
	}

	// Request the bus name
	reply, err := conn.RequestName(dbustypes.BusName, dbus.NameFlagReplaceExisting)
	if err != nil {
		return fmt.Errorf("request name: %w", err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		return fmt.Errorf("not primary owner (reply=%d)", reply)
	}

	return nil
}

// OpenSession opens a secret transfer session.
func (m *MockSecretService) OpenSession(algorithm string, input dbus.Variant) (dbus.Variant, dbus.ObjectPath, *dbus.Error) {
	if algorithm != dbustypes.AlgorithmPlain {
		return dbus.Variant{}, "", dbustypes.ErrUnsupportedAlgorithm(algorithm)
	}

	id := m.sessionCtr.Add(1)
	path := dbustypes.SessionPath(id)

	m.mu.Lock()
	m.sessions[path] = true
	m.mu.Unlock()

	// For "plain" algorithm, output is empty string
	return dbus.MakeVariant(""), path, nil
}

// The sessions that OpenSession opened.
type mockSessions struct {
	service *MockSecretService
}

// Ends the session at the called path, so that secrets no longer travel over
// it. Errors with NoSession when the path names no open session.
func (s mockSessions) Close(msg dbus.Message) *dbus.Error {
	path := dbustypes.PathOf(msg)
	s.service.mu.Lock()
	defer s.service.mu.Unlock()
	if !s.service.sessions[path] {
		return dbustypes.ErrSessionNotFound(string(path))
	}
	delete(s.service.sessions, path)
	return nil
}

// Reports whether the session at the given path is open.
func (m *MockSecretService) HasSession(path dbus.ObjectPath) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.sessions[path]
}

// SearchItems searches for items matching attributes.
func (m *MockSecretService) SearchItems(attributes map[string]string) ([]dbus.ObjectPath, []dbus.ObjectPath, *dbus.Error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var matched []dbus.ObjectPath
	for path, item := range m.items {
		if item.Attributes.Contain(attributes) {
			matched = append(matched, path)
		}
	}
	if m.locked {
		return nil, matched, nil
	}
	return matched, nil, nil
}

// GetSecrets retrieves secrets for the given items.
func (m *MockSecretService) GetSecrets(items []dbus.ObjectPath, session dbus.ObjectPath) (map[dbus.ObjectPath]dbustypes.Secret, *dbus.Error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.sessions[session] {
		return nil, dbustypes.ErrSessionNotFound(string(session))
	}

	secrets := make(map[dbus.ObjectPath]dbustypes.Secret)
	for _, path := range items {
		if m.locked {
			return nil, dbustypes.ErrLocked(string(path))
		}
		item, ok := m.items[path]
		if !ok {
			continue
		}
		secrets[path] = dbustypes.Secret{
			Session:     session,
			Parameters:  nil,
			Value:       item.Secret,
			ContentType: item.ContentType,
		}
	}
	return secrets, nil
}

// Unlock unlocks objects. If the collection is locked, unlocking requires a
// prompt: the objects stay locked and the caller must invoke Prompt() on the
// returned prompt path (mirroring gnome-keyring's behavior).
func (m *MockSecretService) Unlock(objects []dbus.ObjectPath) ([]dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	m.mu.RLock()
	locked := m.locked
	m.mu.RUnlock()

	if !locked {
		return objects, dbustypes.NoObject, nil
	}
	return nil, m.newPrompt(objects), nil
}

// Lock locks the collection.
func (m *MockSecretService) Lock(objects []dbus.ObjectPath) ([]dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	m.mu.Lock()
	m.locked = true
	m.mu.Unlock()
	return objects, dbustypes.NoObject, nil
}

// ReadAlias returns the collection for an alias.
func (m *MockSecretService) ReadAlias(name string) (dbus.ObjectPath, *dbus.Error) {
	if name == "default" {
		return DefaultCollectionPath, nil
	}
	return dbustypes.NoObject, nil
}

// SetAlias sets an alias (mock no-op).
func (m *MockSecretService) SetAlias(name string, collection dbus.ObjectPath) *dbus.Error {
	return nil
}

// CreateCollection creates a collection (mock returns default).
func (m *MockSecretService) CreateCollection(properties map[string]dbus.Variant, alias string) (dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	return DefaultCollectionPath, dbustypes.NoObject, nil
}

// Get implements Properties.Get.
func (m *MockSecretService) Get(iface, property string) (dbus.Variant, *dbus.Error) {
	if dbustypes.Interface(iface) == dbustypes.ServiceInterface && property == "Collections" {
		return dbus.MakeVariant([]dbus.ObjectPath{DefaultCollectionPath}), nil
	}
	return dbus.Variant{}, &dbus.Error{Name: "org.freedesktop.DBus.Error.UnknownProperty"}
}

// GetAll implements Properties.GetAll.
func (m *MockSecretService) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	if dbustypes.Interface(iface) == dbustypes.ServiceInterface {
		return map[string]dbus.Variant{
			"Collections": dbus.MakeVariant([]dbus.ObjectPath{DefaultCollectionPath}),
		}, nil
	}
	return nil, nil
}

// Set implements Properties.Set (no-op).
func (m *MockSecretService) Set(iface, property string, value dbus.Variant) *dbus.Error {
	return nil
}

// Introspect returns introspection XML.
func (m *MockSecretService) Introspect() string {
	return `<!DOCTYPE node PUBLIC "-//freedesktop//DTD D-BUS Object Introspection 1.0//EN"
"http://www.freedesktop.org/standards/dbus/1.0/introspect.dtd">
<node>
  <interface name="org.freedesktop.Secret.Service">
    <method name="OpenSession">
      <arg name="algorithm" type="s" direction="in"/>
      <arg name="input" type="v" direction="in"/>
      <arg name="output" type="v" direction="out"/>
      <arg name="result" type="o" direction="out"/>
    </method>
    <method name="SearchItems">
      <arg name="attributes" type="a{ss}" direction="in"/>
      <arg name="unlocked" type="ao" direction="out"/>
      <arg name="locked" type="ao" direction="out"/>
    </method>
    <method name="GetSecrets">
      <arg name="items" type="ao" direction="in"/>
      <arg name="session" type="o" direction="in"/>
      <arg name="secrets" type="a{o(oayays)}" direction="out"/>
    </method>
    <method name="Unlock">
      <arg name="objects" type="ao" direction="in"/>
      <arg name="unlocked" type="ao" direction="out"/>
      <arg name="prompt" type="o" direction="out"/>
    </method>
    <method name="Lock">
      <arg name="objects" type="ao" direction="in"/>
      <arg name="locked" type="ao" direction="out"/>
      <arg name="prompt" type="o" direction="out"/>
    </method>
    <method name="ReadAlias">
      <arg name="name" type="s" direction="in"/>
      <arg name="collection" type="o" direction="out"/>
    </method>
    <method name="SetAlias">
      <arg name="name" type="s" direction="in"/>
      <arg name="collection" type="o" direction="in"/>
    </method>
    <method name="CreateCollection">
      <arg name="properties" type="a{sv}" direction="in"/>
      <arg name="alias" type="s" direction="in"/>
      <arg name="collection" type="o" direction="out"/>
      <arg name="prompt" type="o" direction="out"/>
    </method>
    <property name="Collections" type="ao" access="read"/>
  </interface>
</node>`
}

// AddItem adds a secret item to the mock store (for test setup).
func (m *MockSecretService) AddItem(label string, attributes map[string]string, secret []byte) dbus.ObjectPath {
	id := m.itemCtr.Add(1)
	path := dbus.ObjectPath(fmt.Sprintf("%s/%d", DefaultCollectionPath, id))

	item := &MockItem{
		Path:        path,
		Label:       label,
		Attributes:  attributes,
		Secret:      secret,
		ContentType: "text/plain",
	}

	m.mu.Lock()
	m.items[path] = item
	m.mu.Unlock()
	return path
}

// ItemCount returns the number of items in the mock store.
func (m *MockSecretService) ItemCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.items)
}

// SetLocked locks or unlocks the default collection (for test setup).
func (m *MockSecretService) SetLocked(locked bool) {
	m.mu.Lock()
	m.locked = locked
	m.mu.Unlock()
}

// Locked reports whether the default collection is locked.
func (m *MockSecretService) Locked() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.locked
}

// LastWindowID returns the window ID passed to the most recent Prompt() call.
func (m *MockSecretService) LastWindowID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastWindowID
}

// Returns the path of a new prompt that will unlock the given objects when
// prompted.
func (m *MockSecretService) newPrompt(objects []dbus.ObjectPath) dbus.ObjectPath {
	path := dbustypes.PromptPath(fmt.Sprintf("p%d", m.promptCtr.Add(1)))
	m.mu.Lock()
	m.prompts[path] = objects
	m.mu.Unlock()
	return path
}

// The prompts that newPrompt made. Prompting unlocks the collection and emits
// Completed with the unlocked objects; dismissing leaves it locked and emits
// Completed(dismissed=true).
type mockPrompts struct {
	service *MockSecretService
}

func (p mockPrompts) Prompt(msg dbus.Message, windowID string) *dbus.Error {
	path := dbustypes.PathOf(msg)
	objects, dbusErr := p.objects(path)
	if dbusErr != nil {
		return dbusErr
	}
	p.service.mu.Lock()
	p.service.locked = false
	p.service.lastWindowID = windowID
	p.service.mu.Unlock()
	return p.completed(path, false, dbus.MakeVariant(objects))
}

func (p mockPrompts) Dismiss(msg dbus.Message) *dbus.Error {
	path := dbustypes.PathOf(msg)
	if _, dbusErr := p.objects(path); dbusErr != nil {
		return dbusErr
	}
	return p.completed(path, true, dbus.MakeVariant(""))
}

// Returns the objects that the prompt at path unlocks.
func (p mockPrompts) objects(path dbus.ObjectPath) ([]dbus.ObjectPath, *dbus.Error) {
	p.service.mu.RLock()
	defer p.service.mu.RUnlock()
	objects, ok := p.service.prompts[path]
	if !ok {
		return nil, dbustypes.ErrObjectNotFound(string(path))
	}
	return objects, nil
}

func (p mockPrompts) completed(path dbus.ObjectPath, dismissed bool, result dbus.Variant) *dbus.Error {
	if err := p.service.conn.Emit(path, dbustypes.PromptInterface.Member("Completed"), dismissed, result); err != nil {
		return dbustypes.ErrFailed(err)
	}
	return nil
}

// mockCollection implements Collection interface.
type mockCollection struct {
	service *MockSecretService
	path    dbus.ObjectPath
}

func (c *mockCollection) Delete() (dbus.ObjectPath, *dbus.Error) {
	return dbustypes.NoObject, nil
}

func (c *mockCollection) SearchItems(attributes map[string]string) ([]dbus.ObjectPath, *dbus.Error) {
	unlocked, _, err := c.service.SearchItems(attributes)
	return unlocked, err
}

func (c *mockCollection) CreateItem(properties map[string]dbus.Variant, secret dbustypes.Secret, replace bool) (dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	props, err := dbustypes.ParseItemProperties(properties)
	if err != nil {
		return dbustypes.NoObject, dbustypes.NoObject, dbustypes.AsDBusError(err)
	}
	label := cmp.Or(props.Label, "Untitled")

	// Check for existing item with same attributes (for replace)
	if replace {
		c.service.mu.Lock()
		for path, item := range c.service.items {
			if maps.Equal(item.Attributes, props.Attributes) {
				// Update existing item
				item.Label = label
				item.Secret = secret.Value
				item.ContentType = secret.ContentType
				c.service.mu.Unlock()
				return path, dbustypes.NoObject, nil
			}
		}
		c.service.mu.Unlock()
	}

	// Create new item
	path := c.service.AddItem(label, props.Attributes, secret.Value)
	return path, dbustypes.NoObject, nil
}

func (c *mockCollection) properties() map[string]dbus.Variant {
	c.service.mu.RLock()
	defer c.service.mu.RUnlock()
	return map[string]dbus.Variant{
		"Label":  dbus.MakeVariant("Default"),
		"Locked": dbus.MakeVariant(c.service.locked),
		"Items":  dbus.MakeVariant(slices.Collect(maps.Keys(c.service.items))),
	}
}

// Serves the Properties of a collection and of the items under it. The items'
// subtree is exported at the collection's path, and godbus holds one handler
// per path and interface.
type collectionTree struct {
	coll *mockCollection
}

func (t collectionTree) Get(msg dbus.Message, iface, property string) (dbus.Variant, *dbus.Error) {
	props, dbusErr := t.GetAll(msg, iface)
	if dbusErr != nil {
		return dbus.Variant{}, dbusErr
	}
	v, ok := props[property]
	if !ok {
		return dbus.Variant{}, &dbus.Error{Name: "org.freedesktop.DBus.Error.UnknownProperty"}
	}
	return v, nil
}

func (t collectionTree) GetAll(msg dbus.Message, iface string) (map[string]dbus.Variant, *dbus.Error) {
	path := dbustypes.PathOf(msg)
	if path == t.coll.path {
		return t.coll.properties(), nil
	}
	return mockItems{t.coll.service}.properties(path)
}

func (collectionTree) Set(iface, property string, value dbus.Variant) *dbus.Error {
	return nil
}

// The items that AddItem added, each at its own path under a collection.
type mockItems struct {
	service *MockSecretService
}

func (i mockItems) Delete(msg dbus.Message) (dbus.ObjectPath, *dbus.Error) {
	i.service.mu.Lock()
	delete(i.service.items, dbustypes.PathOf(msg))
	i.service.mu.Unlock()
	return dbustypes.NoObject, nil
}

func (i mockItems) GetSecret(msg dbus.Message, session dbus.ObjectPath) (dbustypes.Secret, *dbus.Error) {
	path := dbustypes.PathOf(msg)
	i.service.mu.RLock()
	defer i.service.mu.RUnlock()

	if !i.service.sessions[session] {
		return dbustypes.Secret{}, dbustypes.ErrSessionNotFound(string(session))
	}

	if i.service.locked {
		return dbustypes.Secret{}, dbustypes.ErrLocked(string(path))
	}

	item, ok := i.service.items[path]
	if !ok {
		return dbustypes.Secret{}, dbustypes.ErrObjectNotFound(string(path))
	}

	return dbustypes.Secret{
		Session:     session,
		Parameters:  nil,
		Value:       item.Secret,
		ContentType: item.ContentType,
	}, nil
}

func (i mockItems) SetSecret(msg dbus.Message, secret dbustypes.Secret) *dbus.Error {
	path := dbustypes.PathOf(msg)
	i.service.mu.Lock()
	defer i.service.mu.Unlock()

	item, ok := i.service.items[path]
	if !ok {
		return dbustypes.ErrObjectNotFound(string(path))
	}

	item.Secret = secret.Value
	item.ContentType = secret.ContentType
	return nil
}

func (i mockItems) properties(path dbus.ObjectPath) (map[string]dbus.Variant, *dbus.Error) {
	i.service.mu.RLock()
	defer i.service.mu.RUnlock()

	item, ok := i.service.items[path]
	if !ok {
		return nil, dbustypes.ErrObjectNotFound(string(path))
	}

	return map[string]dbus.Variant{
		"Label":      dbus.MakeVariant(item.Label),
		"Attributes": dbus.MakeVariant(item.Attributes),
		"Locked":     dbus.MakeVariant(i.service.locked),
	}, nil
}

type introspectable struct {
	introspectFunc func() string
}

func (i introspectable) Introspect() (string, *dbus.Error) {
	return i.introspectFunc(), nil
}
