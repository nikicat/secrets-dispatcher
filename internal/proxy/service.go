package proxy

import (
	"context"

	"github.com/godbus/dbus/v5"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/logging"
)

// Serves org.freedesktop.Secret.Service.
type Service struct {
	properties
}

// The one Service object at ServicePath.
type serviceObject struct{}

// Returns ServicePath for a call on the Service interface. Errors with
// UnknownInterface for any other interface.
func (serviceObject) object(_ dbus.Message, iface dbustypes.Interface) (dbus.ObjectPath, *dbus.Error) {
	if iface != dbustypes.ServiceInterface {
		return "", dbustypes.ErrInterfaceNotFound(iface)
	}
	return dbustypes.ServicePath, nil
}

// Opens a session for transferring secrets.
// Signature: OpenSession(algorithm String, input Variant) -> (output Variant, result ObjectPath)
func (s *Service) OpenSession(algorithm string, input dbus.Variant) (dbus.Variant, dbus.ObjectPath, *dbus.Error) {
	output, sessionPath, err := s.sessions.CreateSession(algorithm, input)
	if err != nil {
		s.logger.LogOpenSession(context.Background(), algorithm, "", logging.ResultError, err)
		return dbus.Variant{}, dbustypes.NoObject, dbustypes.AsDBusError(err)
	}

	s.logger.LogOpenSession(context.Background(), algorithm, sessionPath, logging.ResultOK, nil)
	return output, sessionPath, nil
}

// Returns the items whose attributes match the given ones.
// Signature: SearchItems(attributes Dict<String,String>) -> (unlocked Array<ObjectPath>, locked Array<ObjectPath>)
func (s *Service) SearchItems(msg dbus.Message, attributes map[string]string) ([]dbus.ObjectPath, []dbus.ObjectPath, *dbus.Error) {
	upstreamCall, err := s.caller(msg).passthrough(searchRequest(attributes))
	if err != nil {
		return nil, nil, dbustypes.ErrAccessDenied(err.Error())
	}

	call := s.callUpstream(upstreamCall, dbustypes.ServicePath, dbustypes.ServiceInterface.Member("SearchItems"), attributes)
	var unlocked, locked []dbus.ObjectPath
	if err := call.Store(&unlocked, &locked); err != nil {
		s.logger.LogSearchItems(context.Background(), attributes, 0, 0, logging.ResultError, err)
		return nil, nil, dbustypes.ErrFailed(err)
	}

	s.logger.LogSearchItems(context.Background(), attributes, len(unlocked), len(locked), logging.ResultOK, nil)
	return unlocked, locked, nil
}

// Returns the given items' secrets, encoded for the given session, when the one
// request for all of them is approved (ADR 0004). Errors with AccessDenied when
// it is denied or expires, and with NoSession when the session is unknown.
// Signature: GetSecrets(items Array<ObjectPath>, session ObjectPath) -> (secrets Dict<ObjectPath,Secret>)
func (s *Service) GetSecrets(msg dbus.Message, items []dbus.ObjectPath, session dbus.ObjectPath) (map[dbus.ObjectPath]dbustypes.Secret, *dbus.Error) {
	caller, release := s.startApprovalCaller(msg)
	defer release()

	itemInfos := make([]approval.ItemInfo, len(items))
	for i, path := range items {
		itemInfos[i] = caller.itemInfo(path)
	}

	info := approval.RequestInfo{Items: itemInfos, Session: session, Type: approval.RequestTypeGetSecret}
	upstreamCall, err := caller.requireApproval(info)
	if err != nil {
		s.logger.LogGetSecrets(caller.ctx, items, logging.ResultDenied, err)
		return nil, dbustypes.ErrAccessDenied(err.Error())
	}

	upstreamSession, ok := s.sessions.UpstreamSession(session)
	if !ok {
		s.logger.LogGetSecrets(context.Background(), items, logging.ResultError, dbustypes.ErrSessionNotFound(string(session)))
		return nil, dbustypes.ErrSessionNotFound(string(session))
	}

	call := s.callUpstream(upstreamCall, dbustypes.ServicePath, dbustypes.ServiceInterface.Member("GetSecrets"), items, upstreamSession)
	var secrets map[dbus.ObjectPath]dbustypes.Secret
	if err := call.Store(&secrets); err != nil {
		s.logger.LogGetSecrets(context.Background(), items, logging.ResultError, err)
		return nil, dbustypes.ErrFailed(err)
	}

	for path, secret := range secrets {
		encoded, err := s.sessions.ForClient(session, secret)
		if err != nil {
			s.logger.LogGetSecrets(context.Background(), items, logging.ResultError, err)
			return nil, sessionSecretError(session, err)
		}
		secrets[path] = encoded
	}

	s.logger.LogGetSecrets(context.Background(), items, logging.ResultOK, nil)
	return secrets, nil
}

// Unlocks the given items and collections.
// Signature: Unlock(objects Array<ObjectPath>) -> (unlocked Array<ObjectPath>, prompt ObjectPath)
func (s *Service) Unlock(msg dbus.Message, objects []dbus.ObjectPath) ([]dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	caller := s.caller(msg)
	infos := make([]approval.ItemInfo, len(objects))
	for i, path := range objects {
		infos[i] = caller.objectInfo(path)
	}

	upstreamCall, err := caller.passthrough(approval.RequestInfo{Items: infos, Type: approval.RequestTypeUnlock})
	if err != nil {
		return nil, dbustypes.NoObject, dbustypes.ErrAccessDenied(err.Error())
	}

	call := s.callUpstream(upstreamCall, dbustypes.ServicePath, dbustypes.ServiceInterface.Member("Unlock"), objects)
	var unlocked []dbus.ObjectPath
	var prompt dbus.ObjectPath
	if err := call.Store(&unlocked, &prompt); err != nil {
		s.logger.LogUnlock(context.Background(), objects, 0, logging.ResultError, err)
		return nil, dbustypes.NoObject, dbustypes.ErrFailed(err)
	}

	s.logger.LogUnlock(context.Background(), objects, len(unlocked), logging.ResultOK, nil)
	return unlocked, prompt, nil
}

// Locks the given items and collections.
// Signature: Lock(objects Array<ObjectPath>) -> (locked Array<ObjectPath>, prompt ObjectPath)
func (s *Service) Lock(msg dbus.Message, objects []dbus.ObjectPath) ([]dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	call := s.callUpstream(s.callContext(msg), dbustypes.ServicePath, dbustypes.ServiceInterface.Member("Lock"), objects)
	var locked []dbus.ObjectPath
	var prompt dbus.ObjectPath
	if err := call.Store(&locked, &prompt); err != nil {
		return nil, dbustypes.NoObject, dbustypes.ErrFailed(err)
	}

	return locked, prompt, nil
}

// Returns the collection with the given alias.
// Signature: ReadAlias(name String) -> (collection ObjectPath)
func (s *Service) ReadAlias(msg dbus.Message, name string) (dbus.ObjectPath, *dbus.Error) {
	call := s.callUpstream(s.callContext(msg), dbustypes.ServicePath, dbustypes.ServiceInterface.Member("ReadAlias"), name)
	var collection dbus.ObjectPath
	if err := call.Store(&collection); err != nil {
		s.logger.LogReadAlias(context.Background(), name, "", logging.ResultError, err)
		return dbustypes.NoObject, dbustypes.ErrFailed(err)
	}

	s.logger.LogReadAlias(context.Background(), name, collection, logging.ResultOK, nil)
	return collection, nil
}

// Gives the given collection the given alias.
// Signature: SetAlias(name String, collection ObjectPath)
func (s *Service) SetAlias(msg dbus.Message, name string, collection dbus.ObjectPath) *dbus.Error {
	call := s.callUpstream(s.callContext(msg), dbustypes.ServicePath, dbustypes.ServiceInterface.Member("SetAlias"), name, collection)
	if call.Err != nil {
		return dbustypes.ErrFailed(call.Err)
	}
	return nil
}

// Creates a collection.
// Signature: CreateCollection(properties Dict<String,Variant>, alias String) -> (collection ObjectPath, prompt ObjectPath)
func (s *Service) CreateCollection(msg dbus.Message, properties map[string]dbus.Variant, alias string) (dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	call := s.callUpstream(s.callContext(msg), dbustypes.ServicePath, dbustypes.ServiceInterface.Member("CreateCollection"), properties, alias)
	var collection, prompt dbus.ObjectPath
	if err := call.Store(&collection, &prompt); err != nil {
		return dbustypes.NoObject, dbustypes.NoObject, dbustypes.ErrFailed(err)
	}

	return collection, prompt, nil
}

// The introspection XML of the Service object.
const serviceIntrospection = `<!DOCTYPE node PUBLIC "-//freedesktop//DTD D-BUS Object Introspection 1.0//EN"
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
    <signal name="CollectionCreated">
      <arg name="collection" type="o"/>
    </signal>
    <signal name="CollectionDeleted">
      <arg name="collection" type="o"/>
    </signal>
    <signal name="CollectionChanged">
      <arg name="collection" type="o"/>
    </signal>
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

// Returns the request to search for items with the given attributes. The
// request has one item, labelled with the attributes.
func searchRequest(attributes dbustypes.Attributes) approval.RequestInfo {
	return approval.RequestInfo{
		Items:   []approval.ItemInfo{{Label: attributes.String()}},
		Type:    approval.RequestTypeSearch,
		Payload: approval.SearchPayload{Attributes: attributes},
	}
}
