package proxy

import (
	"context"

	"github.com/godbus/dbus/v5"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/logging"
)

// Service implements org.freedesktop.Secret.Service.
type Service struct {
	localConn  *dbus.Conn
	sessions   *SessionManager
	logger     *logging.Logger
	approval   *approval.Manager
	clientName string
}

// NewService creates a new Service handler.
func NewService(localConn *dbus.Conn, sessions *SessionManager, logger *logging.Logger, approvalMgr *approval.Manager, clientName string) *Service {
	return &Service{
		localConn:  localConn,
		sessions:   sessions,
		logger:     logger,
		approval:   approvalMgr,
		clientName: clientName,
	}
}

// OpenSession opens a session for secret transfer.
// Signature: OpenSession(algorithm String, input Variant) -> (output Variant, result ObjectPath)
func (s *Service) OpenSession(algorithm string, input dbus.Variant) (dbus.Variant, dbus.ObjectPath, *dbus.Error) {
	output, sessionPath, err := s.sessions.CreateSession(s.localConn, algorithm, input)
	if err != nil {
		if dbusErr, ok := err.(*dbus.Error); ok {
			s.logger.LogOpenSession(context.Background(), algorithm, "", "error", err)
			return dbus.Variant{}, "", dbusErr
		}
		s.logger.LogOpenSession(context.Background(), algorithm, "", "error", err)
		return dbus.Variant{}, "", &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []interface{}{err.Error()}}
	}

	s.logger.LogOpenSession(context.Background(), algorithm, string(sessionPath), "ok", nil)
	return output, sessionPath, nil
}

// SearchItems searches for items matching the given attributes.
// Signature: SearchItems(attributes Dict<String,String>) -> (unlocked Array<ObjectPath>, locked Array<ObjectPath>)
func (s *Service) SearchItems(attributes map[string]string) ([]dbus.ObjectPath, []dbus.ObjectPath, *dbus.Error) {
	obj := s.localConn.Object(dbustypes.BusName, dbustypes.ServicePath)
	call := obj.Call(dbustypes.ServiceInterface+".SearchItems", 0, attributes)
	if call.Err != nil {
		s.logger.LogSearchItems(context.Background(), attributes, 0, 0, "error", call.Err)
		return nil, nil, &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []interface{}{call.Err.Error()}}
	}

	var unlocked, locked []dbus.ObjectPath
	if err := call.Store(&unlocked, &locked); err != nil {
		s.logger.LogSearchItems(context.Background(), attributes, 0, 0, "error", err)
		return nil, nil, &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []interface{}{err.Error()}}
	}

	s.logger.LogSearchItems(context.Background(), attributes, len(unlocked), len(locked), "ok", nil)
	return unlocked, locked, nil
}

// GetSecrets retrieves secrets for multiple items.
// Signature: GetSecrets(items Array<ObjectPath>, session ObjectPath) -> (secrets Dict<ObjectPath,Secret>)
func (s *Service) GetSecrets(items []dbus.ObjectPath, session dbus.ObjectPath) (map[dbus.ObjectPath]dbustypes.Secret, *dbus.Error) {
	itemStrs := objectPathsToStrings(items)

	// Require approval before accessing secrets
	if err := s.approval.RequireApproval(context.Background(), s.clientName, itemStrs, string(session)); err != nil {
		s.logger.LogGetSecrets(context.Background(), itemStrs, "denied", err)
		return nil, dbustypes.ErrAccessDenied(err.Error())
	}

	// Map remote session to local session
	localSession, ok := s.sessions.GetLocalSession(session)
	if !ok {
		s.logger.LogGetSecrets(context.Background(), itemStrs, "error", dbustypes.ErrSessionNotFound(string(session)))
		return nil, dbustypes.ErrSessionNotFound(string(session))
	}

	obj := s.localConn.Object(dbustypes.BusName, dbustypes.ServicePath)
	call := obj.Call(dbustypes.ServiceInterface+".GetSecrets", 0, items, localSession)
	if call.Err != nil {
		s.logger.LogGetSecrets(context.Background(), itemStrs, "error", call.Err)
		return nil, &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []interface{}{call.Err.Error()}}
	}

	// The return type is Dict<ObjectPath, Secret> where Secret is (oayays)
	var secrets map[dbus.ObjectPath]dbustypes.Secret
	if err := call.Store(&secrets); err != nil {
		s.logger.LogGetSecrets(context.Background(), itemStrs, "error", err)
		return nil, &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []interface{}{err.Error()}}
	}

	// Rewrite session paths in the returned secrets to use remote session path
	for path, secret := range secrets {
		secret.Session = session // Use remote session path
		secrets[path] = secret
	}

	s.logger.LogGetSecrets(context.Background(), itemStrs, "ok", nil)
	return secrets, nil
}

// Unlock unlocks the specified objects.
// Signature: Unlock(objects Array<ObjectPath>) -> (unlocked Array<ObjectPath>, prompt ObjectPath)
func (s *Service) Unlock(objects []dbus.ObjectPath) ([]dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	obj := s.localConn.Object(dbustypes.BusName, dbustypes.ServicePath)
	call := obj.Call(dbustypes.ServiceInterface+".Unlock", 0, objects)
	if call.Err != nil {
		objStrs := objectPathsToStrings(objects)
		s.logger.LogUnlock(context.Background(), objStrs, 0, "error", call.Err)
		return nil, "/", &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []interface{}{call.Err.Error()}}
	}

	var unlocked []dbus.ObjectPath
	var prompt dbus.ObjectPath
	if err := call.Store(&unlocked, &prompt); err != nil {
		objStrs := objectPathsToStrings(objects)
		s.logger.LogUnlock(context.Background(), objStrs, 0, "error", err)
		return nil, "/", &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []interface{}{err.Error()}}
	}

	objStrs := objectPathsToStrings(objects)
	s.logger.LogUnlock(context.Background(), objStrs, len(unlocked), "ok", nil)
	return unlocked, prompt, nil
}

// Lock locks the specified objects.
// Signature: Lock(objects Array<ObjectPath>) -> (locked Array<ObjectPath>, prompt ObjectPath)
func (s *Service) Lock(objects []dbus.ObjectPath) ([]dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	obj := s.localConn.Object(dbustypes.BusName, dbustypes.ServicePath)
	call := obj.Call(dbustypes.ServiceInterface+".Lock", 0, objects)
	if call.Err != nil {
		return nil, "/", &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []interface{}{call.Err.Error()}}
	}

	var locked []dbus.ObjectPath
	var prompt dbus.ObjectPath
	if err := call.Store(&locked, &prompt); err != nil {
		return nil, "/", &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []interface{}{err.Error()}}
	}

	return locked, prompt, nil
}

// ReadAlias returns the collection with the given alias.
// Signature: ReadAlias(name String) -> (collection ObjectPath)
func (s *Service) ReadAlias(name string) (dbus.ObjectPath, *dbus.Error) {
	obj := s.localConn.Object(dbustypes.BusName, dbustypes.ServicePath)
	call := obj.Call(dbustypes.ServiceInterface+".ReadAlias", 0, name)
	if call.Err != nil {
		s.logger.LogReadAlias(context.Background(), name, "", "error", call.Err)
		return "/", &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []interface{}{call.Err.Error()}}
	}

	var collection dbus.ObjectPath
	if err := call.Store(&collection); err != nil {
		s.logger.LogReadAlias(context.Background(), name, "", "error", err)
		return "/", &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []interface{}{err.Error()}}
	}

	s.logger.LogReadAlias(context.Background(), name, string(collection), "ok", nil)
	return collection, nil
}

// SetAlias sets an alias for a collection.
// Signature: SetAlias(name String, collection ObjectPath)
func (s *Service) SetAlias(name string, collection dbus.ObjectPath) *dbus.Error {
	obj := s.localConn.Object(dbustypes.BusName, dbustypes.ServicePath)
	call := obj.Call(dbustypes.ServiceInterface+".SetAlias", 0, name, collection)
	if call.Err != nil {
		return &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []interface{}{call.Err.Error()}}
	}
	return nil
}

// CreateCollection creates a new collection.
// Signature: CreateCollection(properties Dict<String,Variant>, alias String) -> (collection ObjectPath, prompt ObjectPath)
func (s *Service) CreateCollection(properties map[string]dbus.Variant, alias string) (dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	obj := s.localConn.Object(dbustypes.BusName, dbustypes.ServicePath)
	call := obj.Call(dbustypes.ServiceInterface+".CreateCollection", 0, properties, alias)
	if call.Err != nil {
		return "/", "/", &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []interface{}{call.Err.Error()}}
	}

	var collection, prompt dbus.ObjectPath
	if err := call.Store(&collection, &prompt); err != nil {
		return "/", "/", &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []interface{}{err.Error()}}
	}

	return collection, prompt, nil
}

// Get implements org.freedesktop.DBus.Properties.Get
func (s *Service) Get(iface, property string) (dbus.Variant, *dbus.Error) {
	if iface != dbustypes.ServiceInterface {
		return dbus.Variant{}, &dbus.Error{Name: "org.freedesktop.DBus.Error.UnknownInterface", Body: []interface{}{iface}}
	}

	obj := s.localConn.Object(dbustypes.BusName, dbustypes.ServicePath)
	variant, err := obj.GetProperty(iface + "." + property)
	if err != nil {
		return dbus.Variant{}, &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []interface{}{err.Error()}}
	}

	return variant, nil
}

// GetAll implements org.freedesktop.DBus.Properties.GetAll
func (s *Service) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	if iface != dbustypes.ServiceInterface {
		return nil, &dbus.Error{Name: "org.freedesktop.DBus.Error.UnknownInterface", Body: []interface{}{iface}}
	}

	obj := s.localConn.Object(dbustypes.BusName, dbustypes.ServicePath)
	call := obj.Call("org.freedesktop.DBus.Properties.GetAll", 0, iface)
	if call.Err != nil {
		return nil, &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []interface{}{call.Err.Error()}}
	}

	var props map[string]dbus.Variant
	if err := call.Store(&props); err != nil {
		return nil, &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []interface{}{err.Error()}}
	}

	return props, nil
}

// Set implements org.freedesktop.DBus.Properties.Set
func (s *Service) Set(iface, property string, value dbus.Variant) *dbus.Error {
	if iface != dbustypes.ServiceInterface {
		return &dbus.Error{Name: "org.freedesktop.DBus.Error.UnknownInterface", Body: []interface{}{iface}}
	}

	obj := s.localConn.Object(dbustypes.BusName, dbustypes.ServicePath)
	call := obj.Call("org.freedesktop.DBus.Properties.Set", 0, iface, property, value)
	if call.Err != nil {
		return &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []interface{}{call.Err.Error()}}
	}

	return nil
}

// Introspect returns introspection XML for the Service interface.
func (s *Service) Introspect() string {
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
    <method name="CreateCollection">
      <arg name="properties" type="a{sv}" direction="in"/>
      <arg name="alias" type="s" direction="in"/>
      <arg name="collection" type="o" direction="out"/>
      <arg name="prompt" type="o" direction="out"/>
    </method>
    <method name="SearchItems">
      <arg name="attributes" type="a{ss}" direction="in"/>
      <arg name="unlocked" type="ao" direction="out"/>
      <arg name="locked" type="ao" direction="out"/>
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
    <method name="GetSecrets">
      <arg name="items" type="ao" direction="in"/>
      <arg name="session" type="o" direction="in"/>
      <arg name="secrets" type="a{o(oayays)}" direction="out"/>
    </method>
    <method name="ReadAlias">
      <arg name="name" type="s" direction="in"/>
      <arg name="collection" type="o" direction="out"/>
    </method>
    <method name="SetAlias">
      <arg name="name" type="s" direction="in"/>
      <arg name="collection" type="o" direction="in"/>
    </method>
    <property name="Collections" type="ao" access="read"/>
  </interface>
  <interface name="org.freedesktop.DBus.Properties">
    <method name="Get">
      <arg name="interface" type="s" direction="in"/>
      <arg name="property" type="s" direction="in"/>
      <arg name="value" type="v" direction="out"/>
    </method>
    <method name="GetAll">
      <arg name="interface" type="s" direction="in"/>
      <arg name="properties" type="a{sv}" direction="out"/>
    </method>
    <method name="Set">
      <arg name="interface" type="s" direction="in"/>
      <arg name="property" type="s" direction="in"/>
      <arg name="value" type="v" direction="in"/>
    </method>
  </interface>
  <interface name="org.freedesktop.DBus.Introspectable">
    <method name="Introspect">
      <arg name="xml" type="s" direction="out"/>
    </method>
  </interface>
</node>`
}

func objectPathsToStrings(paths []dbus.ObjectPath) []string {
	strs := make([]string, len(paths))
	for i, p := range paths {
		strs[i] = string(p)
	}
	return strs
}
