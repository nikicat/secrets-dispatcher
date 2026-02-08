package proxy

import (
	"context"
	"strings"

	"github.com/godbus/dbus/v5"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/logging"
)

// ItemHandler handles Item interface calls for item objects.
// It is exported as a subtree handler for /org/freedesktop/secrets/collection/*/*.
type ItemHandler struct {
	localConn  *dbus.Conn
	sessions   *SessionManager
	logger     *logging.Logger
	approval   *approval.Manager
	clientName string
	tracker    *clientTracker
	resolver   *SenderInfoResolver
}

// NewItemHandler creates a new ItemHandler.
func NewItemHandler(localConn *dbus.Conn, sessions *SessionManager, logger *logging.Logger, approvalMgr *approval.Manager, clientName string, tracker *clientTracker, resolver *SenderInfoResolver) *ItemHandler {
	return &ItemHandler{
		localConn:  localConn,
		sessions:   sessions,
		logger:     logger,
		approval:   approvalMgr,
		clientName: clientName,
		tracker:    tracker,
		resolver:   resolver,
	}
}

// isItemPath checks if the path is an item (not a collection).
// Collection paths: /org/freedesktop/secrets/collection/xxx
// Item paths: /org/freedesktop/secrets/collection/xxx/yyy
func isItemPath(path dbus.ObjectPath) bool {
	p := string(path)
	prefix := "/org/freedesktop/secrets/collection/"
	if !strings.HasPrefix(p, prefix) {
		return false
	}
	remainder := p[len(prefix):]
	// Items have at least one slash (collection/item)
	return strings.Contains(remainder, "/")
}

// Delete deletes the item.
// Signature: Delete() -> (prompt ObjectPath)
func (i *ItemHandler) Delete(msg dbus.Message) (dbus.ObjectPath, *dbus.Error) {
	path := msg.Headers[dbus.FieldPath].Value().(dbus.ObjectPath)
	if !isItemPath(path) {
		return "/", dbustypes.ErrObjectNotFound(string(path))
	}

	obj := i.localConn.Object(dbustypes.BusName, path)
	call := obj.Call(dbustypes.ItemInterface+".Delete", 0)
	if call.Err != nil {
		return "/", &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []interface{}{call.Err.Error()}}
	}

	var prompt dbus.ObjectPath
	if err := call.Store(&prompt); err != nil {
		return "/", &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []interface{}{err.Error()}}
	}

	i.logger.LogMethod(context.Background(), "Item.Delete", map[string]any{
		"item": string(path),
	}, "ok", nil)

	return prompt, nil
}

// GetSecret retrieves the secret for this item.
// Signature: GetSecret(session ObjectPath) -> (secret Secret)
func (i *ItemHandler) GetSecret(msg dbus.Message, session dbus.ObjectPath) (dbustypes.Secret, *dbus.Error) {
	path := msg.Headers[dbus.FieldPath].Value().(dbus.ObjectPath)
	if !isItemPath(path) {
		return dbustypes.Secret{}, dbustypes.ErrObjectNotFound(string(path))
	}

	// Fetch item info (label + attributes)
	itemInfo := i.getItemInfo(path)

	// Get a context that will be cancelled if the client disconnects
	sender := msg.Headers[dbus.FieldSender].Value().(string)
	ctx := i.tracker.contextForSender(context.Background(), sender)
	defer i.tracker.remove(sender)

	// Resolve sender information
	senderInfo := i.resolver.Resolve(sender)

	// Require approval before accessing secret
	items := []approval.ItemInfo{itemInfo}
	if err := i.approval.RequireApproval(ctx, i.clientName, items, string(session), approval.RequestTypeGetSecret, nil, senderInfo); err != nil {
		i.logger.LogItemGetSecret(ctx, string(path), "denied", err)
		return dbustypes.Secret{}, dbustypes.ErrAccessDenied(err.Error())
	}

	// Map remote session to local session
	localSession, ok := i.sessions.GetLocalSession(session)
	if !ok {
		i.logger.LogItemGetSecret(context.Background(), string(path), "error", dbustypes.ErrSessionNotFound(string(session)))
		return dbustypes.Secret{}, dbustypes.ErrSessionNotFound(string(session))
	}

	obj := i.localConn.Object(dbustypes.BusName, path)
	call := obj.Call(dbustypes.ItemInterface+".GetSecret", 0, localSession)
	if call.Err != nil {
		i.logger.LogItemGetSecret(context.Background(), string(path), "error", call.Err)
		return dbustypes.Secret{}, &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []interface{}{call.Err.Error()}}
	}

	var secret dbustypes.Secret
	if err := call.Store(&secret); err != nil {
		i.logger.LogItemGetSecret(context.Background(), string(path), "error", err)
		return dbustypes.Secret{}, &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []interface{}{err.Error()}}
	}

	// Rewrite session path to use remote session
	secret.Session = session

	i.logger.LogItemGetSecret(context.Background(), string(path), "ok", nil)
	return secret, nil
}

// SetSecret sets the secret for this item.
// Signature: SetSecret(secret Secret)
func (i *ItemHandler) SetSecret(msg dbus.Message, secret dbustypes.Secret) *dbus.Error {
	path := msg.Headers[dbus.FieldPath].Value().(dbus.ObjectPath)
	if !isItemPath(path) {
		return dbustypes.ErrObjectNotFound(string(path))
	}

	// Map remote session to local session
	localSession, ok := i.sessions.GetLocalSession(secret.Session)
	if !ok {
		return dbustypes.ErrSessionNotFound(string(secret.Session))
	}

	// Create local secret with local session path
	localSecret := dbustypes.Secret{
		Session:     localSession,
		Parameters:  secret.Parameters,
		Value:       secret.Value,
		ContentType: secret.ContentType,
	}

	obj := i.localConn.Object(dbustypes.BusName, path)
	call := obj.Call(dbustypes.ItemInterface+".SetSecret", 0, localSecret)
	if call.Err != nil {
		return &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []interface{}{call.Err.Error()}}
	}

	i.logger.LogMethod(context.Background(), "Item.SetSecret", map[string]any{
		"item": string(path),
	}, "ok", nil)

	return nil
}

// Get implements org.freedesktop.DBus.Properties.Get for items.
func (i *ItemHandler) Get(msg dbus.Message, iface, property string) (dbus.Variant, *dbus.Error) {
	path := msg.Headers[dbus.FieldPath].Value().(dbus.ObjectPath)
	if !isItemPath(path) {
		return dbus.Variant{}, dbustypes.ErrObjectNotFound(string(path))
	}

	obj := i.localConn.Object(dbustypes.BusName, path)
	variant, err := obj.GetProperty(iface + "." + property)
	if err != nil {
		return dbus.Variant{}, &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []interface{}{err.Error()}}
	}

	return variant, nil
}

// GetAll implements org.freedesktop.DBus.Properties.GetAll for items.
func (i *ItemHandler) GetAll(msg dbus.Message, iface string) (map[string]dbus.Variant, *dbus.Error) {
	path := msg.Headers[dbus.FieldPath].Value().(dbus.ObjectPath)
	if !isItemPath(path) {
		return nil, dbustypes.ErrObjectNotFound(string(path))
	}

	obj := i.localConn.Object(dbustypes.BusName, path)
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

// Set implements org.freedesktop.DBus.Properties.Set for items.
func (i *ItemHandler) Set(msg dbus.Message, iface, property string, value dbus.Variant) *dbus.Error {
	path := msg.Headers[dbus.FieldPath].Value().(dbus.ObjectPath)
	if !isItemPath(path) {
		return dbustypes.ErrObjectNotFound(string(path))
	}

	obj := i.localConn.Object(dbustypes.BusName, path)
	call := obj.Call("org.freedesktop.DBus.Properties.Set", 0, iface, property, value)
	if call.Err != nil {
		return &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []interface{}{call.Err.Error()}}
	}

	return nil
}

// getItemInfo fetches label and attributes for a secret item from D-Bus.
func (i *ItemHandler) getItemInfo(path dbus.ObjectPath) approval.ItemInfo {
	info := approval.ItemInfo{Path: string(path)}

	obj := i.localConn.Object(dbustypes.BusName, path)

	// Get Label property
	if v, err := obj.GetProperty(dbustypes.ItemInterface + ".Label"); err == nil {
		if label, ok := v.Value().(string); ok {
			info.Label = label
		}
	}

	// Get Attributes property
	if v, err := obj.GetProperty(dbustypes.ItemInterface + ".Attributes"); err == nil {
		if attrs, ok := v.Value().(map[string]string); ok {
			info.Attributes = attrs
		}
	}

	return info
}
