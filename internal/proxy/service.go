package proxy

import (
	"context"
	"sort"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/logging"
)

// Service implements org.freedesktop.Secret.Service.
type Service struct {
	localConn        *dbus.Conn
	sessions         *SessionManager
	logger           *logging.Logger
	approval         *approval.Manager
	clientName       string
	tracker          *clientTracker
	resolver         *SenderInfoResolver
	upstreamNotifier UpstreamNotifier
	slowThreshold    time.Duration
}

// NewService creates a new Service handler.
func NewService(localConn *dbus.Conn, sessions *SessionManager, logger *logging.Logger, approvalMgr *approval.Manager, clientName string, tracker *clientTracker, resolver *SenderInfoResolver, upstreamNotifier UpstreamNotifier, slowThreshold time.Duration) *Service {
	return &Service{
		localConn:        localConn,
		sessions:         sessions,
		logger:           logger,
		approval:         approvalMgr,
		clientName:       clientName,
		tracker:          tracker,
		resolver:         resolver,
		upstreamNotifier: upstreamNotifier,
		slowThreshold:    slowThreshold,
	}
}

// upstream wraps a D-Bus Call through the slow-upstream notifier.
func (s *Service) upstream(fn func() *dbus.Call) *dbus.Call {
	return WithSlowNotify(s.slowThreshold, s.upstreamNotifier, UpstreamCallContext{}, fn)
}

// upstreamWithContext wraps a D-Bus Call through the slow-upstream notifier,
// passing caller and item context to the notification.
func (s *Service) upstreamWithContext(ctx UpstreamCallContext, fn func() *dbus.Call) *dbus.Call {
	return WithSlowNotify(s.slowThreshold, s.upstreamNotifier, ctx, fn)
}

// upstreamGetProperty wraps a GetProperty call through the slow-upstream notifier.
func (s *Service) upstreamGetProperty(obj dbus.BusObject, prop string) (dbus.Variant, error) {
	r := WithSlowNotify(s.slowThreshold, s.upstreamNotifier, UpstreamCallContext{}, func() propResult {
		v, err := obj.GetProperty(prop)
		return propResult{v, err}
	})
	return r.v, r.err
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
		return dbus.Variant{}, "", &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []any{err.Error()}}
	}

	s.logger.LogOpenSession(context.Background(), algorithm, string(sessionPath), "ok", nil)
	return output, sessionPath, nil
}

// SearchItems searches for items matching the given attributes.
// Signature: SearchItems(attributes Dict<String,String>) -> (unlocked Array<ObjectPath>, locked Array<ObjectPath>)
func (s *Service) SearchItems(msg dbus.Message, attributes map[string]string) ([]dbus.ObjectPath, []dbus.ObjectPath, *dbus.Error) {
	obj := s.localConn.Object(dbustypes.BusName, dbustypes.ServicePath)
	infos := searchAttributesToItemInfo(attributes)
	sender := msg.Headers[dbus.FieldSender].Value().(string)
	senderInfo := s.resolver.Resolve(sender)
	s.approval.RecordPassthrough(s.clientName, infos, "", approval.RequestTypeSearch, attributes, senderInfo)
	call := s.upstreamWithContext(UpstreamCallContext{
		RequestType:   approval.RequestTypeSearch,
		Items:         infos,
		ResolveSender: func() approval.SenderInfo { return s.resolver.Resolve(sender) },
	}, func() *dbus.Call { return obj.Call(dbustypes.ServiceInterface+".SearchItems", 0, attributes) })
	if call.Err != nil {
		s.logger.LogSearchItems(context.Background(), attributes, 0, 0, "error", call.Err)
		return nil, nil, &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []any{call.Err.Error()}}
	}

	var unlocked, locked []dbus.ObjectPath
	if err := call.Store(&unlocked, &locked); err != nil {
		s.logger.LogSearchItems(context.Background(), attributes, 0, 0, "error", err)
		return nil, nil, &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []any{err.Error()}}
	}

	s.logger.LogSearchItems(context.Background(), attributes, len(unlocked), len(locked), "ok", nil)
	return unlocked, locked, nil
}

// GetSecrets retrieves secrets for multiple items.
// Signature: GetSecrets(items Array<ObjectPath>, session ObjectPath) -> (secrets Dict<ObjectPath,Secret>)
func (s *Service) GetSecrets(msg dbus.Message, items []dbus.ObjectPath, session dbus.ObjectPath) (map[dbus.ObjectPath]dbustypes.Secret, *dbus.Error) {
	// Fetch item info (label + attributes) for each item
	itemInfos := make([]approval.ItemInfo, len(items))
	for i, path := range items {
		itemInfos[i] = s.getItemInfo(path)
	}

	// Get a context that will be cancelled if the client disconnects
	sender := msg.Headers[dbus.FieldSender].Value().(string)
	ctx := s.tracker.contextForSender(context.Background(), sender)
	defer s.tracker.remove(sender)

	// Resolve sender information
	senderInfo := s.resolver.Resolve(sender)

	// Require approval before accessing secrets
	itemStrs := objectPathsToStrings(items)
	_, err := s.approval.RequireApproval(ctx, s.clientName, itemInfos, string(session), approval.RequestTypeGetSecret, nil, senderInfo)
	if err != nil {
		s.logger.LogGetSecrets(ctx, itemStrs, "denied", err)
		return nil, dbustypes.ErrAccessDenied(err.Error())
	}

	// Map remote session to local session
	localSession, ok := s.sessions.GetLocalSession(session)
	if !ok {
		s.logger.LogGetSecrets(context.Background(), itemStrs, "error", dbustypes.ErrSessionNotFound(string(session)))
		return nil, dbustypes.ErrSessionNotFound(string(session))
	}

	obj := s.localConn.Object(dbustypes.BusName, dbustypes.ServicePath)
	call := s.upstreamWithContext(UpstreamCallContext{
		RequestType: approval.RequestTypeGetSecret,
		Items:       itemInfos,
		SenderInfo:  senderInfo,
	}, func() *dbus.Call { return obj.Call(dbustypes.ServiceInterface+".GetSecrets", 0, items, localSession) })
	if call.Err != nil {
		s.logger.LogGetSecrets(context.Background(), itemStrs, "error", call.Err)
		return nil, &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []any{call.Err.Error()}}
	}

	// The return type is Dict<ObjectPath, Secret> where Secret is (oayays)
	var secrets map[dbus.ObjectPath]dbustypes.Secret
	if err := call.Store(&secrets); err != nil {
		s.logger.LogGetSecrets(context.Background(), itemStrs, "error", err)
		return nil, &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []any{err.Error()}}
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
func (s *Service) Unlock(msg dbus.Message, objects []dbus.ObjectPath) ([]dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	obj := s.localConn.Object(dbustypes.BusName, dbustypes.ServicePath)
	infos := s.getUnlockInfo(objects)
	sender := msg.Headers[dbus.FieldSender].Value().(string)
	senderInfo := s.resolver.Resolve(sender)
	s.approval.RecordPassthrough(s.clientName, infos, "", approval.RequestTypeUnlock, nil, senderInfo)
	call := s.upstreamWithContext(UpstreamCallContext{
		Items:         infos,
		ResolveSender: func() approval.SenderInfo { return s.resolver.Resolve(sender) },
	}, func() *dbus.Call { return obj.Call(dbustypes.ServiceInterface+".Unlock", 0, objects) })
	if call.Err != nil {
		objStrs := objectPathsToStrings(objects)
		s.logger.LogUnlock(context.Background(), objStrs, 0, "error", call.Err)
		return nil, "/", &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []any{call.Err.Error()}}
	}

	var unlocked []dbus.ObjectPath
	var prompt dbus.ObjectPath
	if err := call.Store(&unlocked, &prompt); err != nil {
		objStrs := objectPathsToStrings(objects)
		s.logger.LogUnlock(context.Background(), objStrs, 0, "error", err)
		return nil, "/", &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []any{err.Error()}}
	}

	objStrs := objectPathsToStrings(objects)
	s.logger.LogUnlock(context.Background(), objStrs, len(unlocked), "ok", nil)
	return unlocked, prompt, nil
}

// Lock locks the specified objects.
// Signature: Lock(objects Array<ObjectPath>) -> (locked Array<ObjectPath>, prompt ObjectPath)
func (s *Service) Lock(objects []dbus.ObjectPath) ([]dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	obj := s.localConn.Object(dbustypes.BusName, dbustypes.ServicePath)
	call := s.upstream(func() *dbus.Call { return obj.Call(dbustypes.ServiceInterface+".Lock", 0, objects) })
	if call.Err != nil {
		return nil, "/", &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []any{call.Err.Error()}}
	}

	var locked []dbus.ObjectPath
	var prompt dbus.ObjectPath
	if err := call.Store(&locked, &prompt); err != nil {
		return nil, "/", &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []any{err.Error()}}
	}

	return locked, prompt, nil
}

// ReadAlias returns the collection with the given alias.
// Signature: ReadAlias(name String) -> (collection ObjectPath)
func (s *Service) ReadAlias(name string) (dbus.ObjectPath, *dbus.Error) {
	obj := s.localConn.Object(dbustypes.BusName, dbustypes.ServicePath)
	call := s.upstream(func() *dbus.Call { return obj.Call(dbustypes.ServiceInterface+".ReadAlias", 0, name) })
	if call.Err != nil {
		s.logger.LogReadAlias(context.Background(), name, "", "error", call.Err)
		return "/", &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []any{call.Err.Error()}}
	}

	var collection dbus.ObjectPath
	if err := call.Store(&collection); err != nil {
		s.logger.LogReadAlias(context.Background(), name, "", "error", err)
		return "/", &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []any{err.Error()}}
	}

	s.logger.LogReadAlias(context.Background(), name, string(collection), "ok", nil)
	return collection, nil
}

// SetAlias sets an alias for a collection.
// Signature: SetAlias(name String, collection ObjectPath)
func (s *Service) SetAlias(name string, collection dbus.ObjectPath) *dbus.Error {
	obj := s.localConn.Object(dbustypes.BusName, dbustypes.ServicePath)
	call := s.upstream(func() *dbus.Call { return obj.Call(dbustypes.ServiceInterface+".SetAlias", 0, name, collection) })
	if call.Err != nil {
		return &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []any{call.Err.Error()}}
	}
	return nil
}

// CreateCollection creates a new collection.
// Signature: CreateCollection(properties Dict<String,Variant>, alias String) -> (collection ObjectPath, prompt ObjectPath)
func (s *Service) CreateCollection(properties map[string]dbus.Variant, alias string) (dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	obj := s.localConn.Object(dbustypes.BusName, dbustypes.ServicePath)
	call := s.upstream(func() *dbus.Call {
		return obj.Call(dbustypes.ServiceInterface+".CreateCollection", 0, properties, alias)
	})
	if call.Err != nil {
		return "/", "/", &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []any{call.Err.Error()}}
	}

	var collection, prompt dbus.ObjectPath
	if err := call.Store(&collection, &prompt); err != nil {
		return "/", "/", &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []any{err.Error()}}
	}

	return collection, prompt, nil
}

// Get implements org.freedesktop.DBus.Properties.Get
func (s *Service) Get(iface, property string) (dbus.Variant, *dbus.Error) {
	if iface != dbustypes.ServiceInterface {
		return dbus.Variant{}, &dbus.Error{Name: "org.freedesktop.DBus.Error.UnknownInterface", Body: []any{iface}}
	}

	obj := s.localConn.Object(dbustypes.BusName, dbustypes.ServicePath)
	variant, err := s.upstreamGetProperty(obj, iface+"."+property)
	if err != nil {
		return dbus.Variant{}, &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []any{err.Error()}}
	}

	return variant, nil
}

// GetAll implements org.freedesktop.DBus.Properties.GetAll
func (s *Service) GetAll(iface string) (map[string]dbus.Variant, *dbus.Error) {
	if iface != dbustypes.ServiceInterface {
		return nil, &dbus.Error{Name: "org.freedesktop.DBus.Error.UnknownInterface", Body: []any{iface}}
	}

	obj := s.localConn.Object(dbustypes.BusName, dbustypes.ServicePath)
	call := s.upstream(func() *dbus.Call { return obj.Call("org.freedesktop.DBus.Properties.GetAll", 0, iface) })
	if call.Err != nil {
		return nil, &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []any{call.Err.Error()}}
	}

	var props map[string]dbus.Variant
	if err := call.Store(&props); err != nil {
		return nil, &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []any{err.Error()}}
	}

	return props, nil
}

// Set implements org.freedesktop.DBus.Properties.Set
func (s *Service) Set(iface, property string, value dbus.Variant) *dbus.Error {
	if iface != dbustypes.ServiceInterface {
		return &dbus.Error{Name: "org.freedesktop.DBus.Error.UnknownInterface", Body: []any{iface}}
	}

	obj := s.localConn.Object(dbustypes.BusName, dbustypes.ServicePath)
	call := s.upstream(func() *dbus.Call {
		return obj.Call("org.freedesktop.DBus.Properties.Set", 0, iface, property, value)
	})
	if call.Err != nil {
		return &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []any{call.Err.Error()}}
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
}

// getItemInfo fetches label and attributes for a secret item from D-Bus.
func (s *Service) getItemInfo(path dbus.ObjectPath) approval.ItemInfo {
	info := approval.ItemInfo{Path: string(path)}

	obj := s.localConn.Object(dbustypes.BusName, path)

	// Get Label property
	if v, err := s.upstreamGetProperty(obj, dbustypes.ItemInterface+".Label"); err == nil {
		if label, ok := v.Value().(string); ok {
			info.Label = label
		}
	}

	// Get Attributes property
	if v, err := s.upstreamGetProperty(obj, dbustypes.ItemInterface+".Attributes"); err == nil {
		if attrs, ok := v.Value().(map[string]string); ok {
			info.Attributes = attrs
		}
	}

	return info
}

// searchAttributesToItemInfo builds an ItemInfo from search attributes for notification display.
func searchAttributesToItemInfo(attributes map[string]string) []approval.ItemInfo {
	// Sort keys for deterministic output.
	keys := make([]string, 0, len(attributes))
	for k := range attributes {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	label := ""
	for _, k := range keys {
		if label != "" {
			label += ", "
		}
		label += k + "=" + attributes[k]
	}
	return []approval.ItemInfo{{Label: label}}
}

// getUnlockInfo builds ItemInfo entries for Unlock objects (typically collections).
func (s *Service) getUnlockInfo(objects []dbus.ObjectPath) []approval.ItemInfo {
	infos := make([]approval.ItemInfo, len(objects))
	for i, path := range objects {
		infos[i] = approval.ItemInfo{Path: string(path)}
		obj := s.localConn.Object(dbustypes.BusName, path)
		if v, err := s.upstreamGetProperty(obj, dbustypes.CollectionInterface+".Label"); err == nil {
			if label, ok := v.Value().(string); ok {
				infos[i].Label = label
			}
		}
	}
	return infos
}

func objectPathsToStrings(paths []dbus.ObjectPath) []string {
	strs := make([]string, len(paths))
	for i, p := range paths {
		strs[i] = string(p)
	}
	return strs
}
