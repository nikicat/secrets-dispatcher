// Package testutil provides test utilities including a mock Secret Service.
package testutil

import (
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/godbus/dbus/v5"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
)

// MockSecretService is a minimal Secret Service implementation for testing.
type MockSecretService struct {
	conn *dbus.Conn

	mu         sync.RWMutex
	items      map[dbus.ObjectPath]*MockItem
	sessions   map[dbus.ObjectPath]bool
	sessionCtr atomic.Uint64
	itemCtr    atomic.Uint64
}

// MockItem represents a stored secret item.
type MockItem struct {
	Path        dbus.ObjectPath
	Label       string
	Attributes  map[string]string
	Secret      []byte
	ContentType string
}

// NewMockSecretService creates a new mock service.
func NewMockSecretService() *MockSecretService {
	return &MockSecretService{
		items:    make(map[dbus.ObjectPath]*MockItem),
		sessions: make(map[dbus.ObjectPath]bool),
	}
}

// Register exports the mock service on the given connection.
func (m *MockSecretService) Register(conn *dbus.Conn) error {
	m.conn = conn

	// Export Service interface
	if err := conn.Export(m, dbustypes.ServicePath, dbustypes.ServiceInterface); err != nil {
		return fmt.Errorf("export Service: %w", err)
	}

	// Export Properties interface
	if err := conn.Export(m, dbustypes.ServicePath, "org.freedesktop.DBus.Properties"); err != nil {
		return fmt.Errorf("export Properties: %w", err)
	}

	// Export Introspectable
	if err := conn.Export(introspectable{m.Introspect}, dbustypes.ServicePath, "org.freedesktop.DBus.Introspectable"); err != nil {
		return fmt.Errorf("export Introspectable: %w", err)
	}

	// Export collection at /org/freedesktop/secrets/collection/default
	coll := &mockCollection{service: m, path: "/org/freedesktop/secrets/collection/default"}
	if err := conn.Export(coll, coll.path, dbustypes.CollectionInterface); err != nil {
		return fmt.Errorf("export Collection: %w", err)
	}
	if err := conn.Export(coll, coll.path, "org.freedesktop.DBus.Properties"); err != nil {
		return fmt.Errorf("export Collection Properties: %w", err)
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
	path := dbus.ObjectPath(fmt.Sprintf("/org/freedesktop/secrets/session/%d", id))

	m.mu.Lock()
	m.sessions[path] = true
	m.mu.Unlock()

	// For "plain" algorithm, output is empty string
	return dbus.MakeVariant(""), path, nil
}

// SearchItems searches for items matching attributes.
func (m *MockSecretService) SearchItems(attributes map[string]string) ([]dbus.ObjectPath, []dbus.ObjectPath, *dbus.Error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var unlocked []dbus.ObjectPath
	for path, item := range m.items {
		if matchesAttributes(item.Attributes, attributes) {
			unlocked = append(unlocked, path)
		}
	}
	return unlocked, nil, nil
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

// Unlock "unlocks" objects (mock always returns them as unlocked).
func (m *MockSecretService) Unlock(objects []dbus.ObjectPath) ([]dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	return objects, "/", nil
}

// Lock "locks" objects (mock no-op).
func (m *MockSecretService) Lock(objects []dbus.ObjectPath) ([]dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	return objects, "/", nil
}

// ReadAlias returns the collection for an alias.
func (m *MockSecretService) ReadAlias(name string) (dbus.ObjectPath, *dbus.Error) {
	if name == "default" {
		return "/org/freedesktop/secrets/collection/default", nil
	}
	return "/", nil
}

// SetAlias sets an alias (mock no-op).
func (m *MockSecretService) SetAlias(name string, collection dbus.ObjectPath) *dbus.Error {
	return nil
}

// CreateCollection creates a collection (mock returns default).
func (m *MockSecretService) CreateCollection(properties map[string]dbus.Variant, alias string) (dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	return "/org/freedesktop/secrets/collection/default", "/", nil
}

// Get implements Properties.Get.
func (m *MockSecretService) Get(iface, property string) (dbus.Variant, *dbus.Error) {
	if iface == dbustypes.ServiceInterface && property == "Collections" {
		return dbus.MakeVariant([]dbus.ObjectPath{"/org/freedesktop/secrets/collection/default"}), nil
	}
	return dbus.Variant{}, &dbus.Error{Name: "org.freedesktop.DBus.Error.UnknownProperty"}
}

// GetAll implements Properties.GetAll.
func (m *MockSecretService) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	if iface == dbustypes.ServiceInterface {
		return map[string]dbus.Variant{
			"Collections": dbus.MakeVariant([]dbus.ObjectPath{"/org/freedesktop/secrets/collection/default"}),
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
	path := dbus.ObjectPath(fmt.Sprintf("/org/freedesktop/secrets/collection/default/%d", id))

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

	// Export item interface
	itemHandler := &mockItem{service: m, path: path}
	m.conn.Export(itemHandler, path, dbustypes.ItemInterface)
	m.conn.Export(itemHandler, path, "org.freedesktop.DBus.Properties")

	return path
}

// mockCollection implements Collection interface.
type mockCollection struct {
	service *MockSecretService
	path    dbus.ObjectPath
}

func (c *mockCollection) Delete() (dbus.ObjectPath, *dbus.Error) {
	return "/", nil
}

func (c *mockCollection) SearchItems(attributes map[string]string) ([]dbus.ObjectPath, *dbus.Error) {
	unlocked, _, err := c.service.SearchItems(attributes)
	return unlocked, err
}

func (c *mockCollection) CreateItem(properties map[string]dbus.Variant, secret dbustypes.Secret, replace bool) (dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	// Extract label from properties
	label := "Untitled"
	if v, ok := properties["org.freedesktop.Secret.Item.Label"]; ok {
		if s, ok := v.Value().(string); ok {
			label = s
		}
	}

	// Extract attributes from properties
	attrs := make(map[string]string)
	if v, ok := properties["org.freedesktop.Secret.Item.Attributes"]; ok {
		if a, ok := v.Value().(map[string]string); ok {
			attrs = a
		}
	}

	// Check for existing item with same attributes (for replace)
	if replace {
		c.service.mu.Lock()
		for path, item := range c.service.items {
			if matchesAttributes(item.Attributes, attrs) && len(item.Attributes) == len(attrs) {
				// Update existing item
				item.Label = label
				item.Secret = secret.Value
				item.ContentType = secret.ContentType
				c.service.mu.Unlock()
				return path, "/", nil
			}
		}
		c.service.mu.Unlock()
	}

	// Create new item
	path := c.service.AddItem(label, attrs, secret.Value)
	return path, "/", nil
}

func (c *mockCollection) Get(iface, property string) (dbus.Variant, *dbus.Error) {
	switch property {
	case "Label":
		return dbus.MakeVariant("Default"), nil
	case "Locked":
		return dbus.MakeVariant(false), nil
	case "Items":
		c.service.mu.RLock()
		items := make([]dbus.ObjectPath, 0, len(c.service.items))
		for path := range c.service.items {
			items = append(items, path)
		}
		c.service.mu.RUnlock()
		return dbus.MakeVariant(items), nil
	}
	return dbus.Variant{}, &dbus.Error{Name: "org.freedesktop.DBus.Error.UnknownProperty"}
}

func (c *mockCollection) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	c.service.mu.RLock()
	items := make([]dbus.ObjectPath, 0, len(c.service.items))
	for path := range c.service.items {
		items = append(items, path)
	}
	c.service.mu.RUnlock()

	return map[string]dbus.Variant{
		"Label":  dbus.MakeVariant("Default"),
		"Locked": dbus.MakeVariant(false),
		"Items":  dbus.MakeVariant(items),
	}, nil
}

func (c *mockCollection) Set(iface, property string, value dbus.Variant) *dbus.Error {
	return nil
}

// mockItem implements Item interface.
type mockItem struct {
	service *MockSecretService
	path    dbus.ObjectPath
}

func (i *mockItem) Delete() (dbus.ObjectPath, *dbus.Error) {
	i.service.mu.Lock()
	delete(i.service.items, i.path)
	i.service.mu.Unlock()
	return "/", nil
}

func (i *mockItem) GetSecret(session dbus.ObjectPath) (dbustypes.Secret, *dbus.Error) {
	i.service.mu.RLock()
	defer i.service.mu.RUnlock()

	if !i.service.sessions[session] {
		return dbustypes.Secret{}, dbustypes.ErrSessionNotFound(string(session))
	}

	item, ok := i.service.items[i.path]
	if !ok {
		return dbustypes.Secret{}, dbustypes.ErrObjectNotFound(string(i.path))
	}

	return dbustypes.Secret{
		Session:     session,
		Parameters:  nil,
		Value:       item.Secret,
		ContentType: item.ContentType,
	}, nil
}

func (i *mockItem) SetSecret(secret dbustypes.Secret) *dbus.Error {
	i.service.mu.Lock()
	defer i.service.mu.Unlock()

	item, ok := i.service.items[i.path]
	if !ok {
		return dbustypes.ErrObjectNotFound(string(i.path))
	}

	item.Secret = secret.Value
	item.ContentType = secret.ContentType
	return nil
}

func (i *mockItem) Get(iface, property string) (dbus.Variant, *dbus.Error) {
	i.service.mu.RLock()
	defer i.service.mu.RUnlock()

	item, ok := i.service.items[i.path]
	if !ok {
		return dbus.Variant{}, dbustypes.ErrObjectNotFound(string(i.path))
	}

	switch property {
	case "Label":
		return dbus.MakeVariant(item.Label), nil
	case "Attributes":
		return dbus.MakeVariant(item.Attributes), nil
	case "Locked":
		return dbus.MakeVariant(false), nil
	}
	return dbus.Variant{}, &dbus.Error{Name: "org.freedesktop.DBus.Error.UnknownProperty"}
}

func (i *mockItem) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	i.service.mu.RLock()
	defer i.service.mu.RUnlock()

	item, ok := i.service.items[i.path]
	if !ok {
		return nil, dbustypes.ErrObjectNotFound(string(i.path))
	}

	return map[string]dbus.Variant{
		"Label":      dbus.MakeVariant(item.Label),
		"Attributes": dbus.MakeVariant(item.Attributes),
		"Locked":     dbus.MakeVariant(false),
	}, nil
}

func (i *mockItem) Set(iface, property string, value dbus.Variant) *dbus.Error {
	return nil
}

func matchesAttributes(itemAttrs, searchAttrs map[string]string) bool {
	for k, v := range searchAttrs {
		if itemAttrs[k] != v {
			return false
		}
	}
	return true
}

type introspectable struct {
	introspectFunc func() string
}

func (i introspectable) Introspect() (string, *dbus.Error) {
	return i.introspectFunc(), nil
}
