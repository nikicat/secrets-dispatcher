package proxy

import (
	"context"
	"strings"

	"github.com/godbus/dbus/v5"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/logging"
)

// CollectionHandler handles Collection interface calls for collection objects.
// It is exported as a subtree handler for /org/freedesktop/secrets/collection/*.
type CollectionHandler struct {
	localConn  *dbus.Conn
	sessions   *SessionManager
	logger     *logging.Logger
	approval   *approval.Manager
	clientName string
	tracker    *clientTracker
	resolver   *SenderInfoResolver
}

// NewCollectionHandler creates a new CollectionHandler.
func NewCollectionHandler(localConn *dbus.Conn, sessions *SessionManager, logger *logging.Logger, approvalMgr *approval.Manager, clientName string, tracker *clientTracker, resolver *SenderInfoResolver) *CollectionHandler {
	return &CollectionHandler{
		localConn:  localConn,
		sessions:   sessions,
		logger:     logger,
		approval:   approvalMgr,
		clientName: clientName,
		tracker:    tracker,
		resolver:   resolver,
	}
}

// isCollectionPath checks if the path is a collection (not an item).
// Collection paths: /org/freedesktop/secrets/collection/xxx
// Alias paths: /org/freedesktop/secrets/aliases/xxx
// Item paths: /org/freedesktop/secrets/collection/xxx/yyy
func isCollectionPath(path dbus.ObjectPath) bool {
	p := string(path)
	for _, prefix := range []string{
		"/org/freedesktop/secrets/collection/",
		"/org/freedesktop/secrets/aliases/",
	} {
		if strings.HasPrefix(p, prefix) {
			remainder := p[len(prefix):]
			// Collection/alias has no additional slashes, items do
			return remainder != "" && !strings.Contains(remainder, "/")
		}
	}
	return false
}

// Delete deletes the collection.
// Signature: Delete() -> (prompt ObjectPath)
func (c *CollectionHandler) Delete(msg dbus.Message) (dbus.ObjectPath, *dbus.Error) {
	path := msg.Headers[dbus.FieldPath].Value().(dbus.ObjectPath)
	if !isCollectionPath(path) {
		return "/", dbustypes.ErrObjectNotFound(string(path))
	}

	obj := c.localConn.Object(dbustypes.BusName, path)
	call := obj.Call(dbustypes.CollectionInterface+".Delete", 0)
	if call.Err != nil {
		return "/", &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []interface{}{call.Err.Error()}}
	}

	var prompt dbus.ObjectPath
	if err := call.Store(&prompt); err != nil {
		return "/", &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []interface{}{err.Error()}}
	}

	return prompt, nil
}

// SearchItems searches for items in this collection matching the given attributes.
// Signature: SearchItems(attributes Dict<String,String>) -> (results Array<ObjectPath>)
func (c *CollectionHandler) SearchItems(msg dbus.Message, attributes map[string]string) ([]dbus.ObjectPath, *dbus.Error) {
	path := msg.Headers[dbus.FieldPath].Value().(dbus.ObjectPath)
	if !isCollectionPath(path) {
		return nil, dbustypes.ErrObjectNotFound(string(path))
	}

	// Perform search on local service first
	obj := c.localConn.Object(dbustypes.BusName, path)
	call := obj.Call(dbustypes.CollectionInterface+".SearchItems", 0, attributes)
	if call.Err != nil {
		return nil, &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []interface{}{call.Err.Error()}}
	}

	var results []dbus.ObjectPath
	if err := call.Store(&results); err != nil {
		return nil, &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []interface{}{err.Error()}}
	}

	// Fetch item info for all results
	itemInfos := make([]approval.ItemInfo, len(results))
	for i, itemPath := range results {
		itemInfos[i] = c.getItemInfo(itemPath)
	}

	// Get a context that will be cancelled if the client disconnects
	sender := msg.Headers[dbus.FieldSender].Value().(string)
	ctx := c.tracker.contextForSender(context.Background(), sender)
	defer c.tracker.remove(sender)

	// Resolve sender information
	senderInfo := c.resolver.Resolve(sender)

	// Require approval before returning search results
	if err := c.approval.RequireApproval(ctx, c.clientName, itemInfos, "", approval.RequestTypeSearch, attributes, senderInfo); err != nil {
		c.logger.LogMethod(ctx, "Collection.SearchItems", map[string]any{
			"collection": string(path),
			"attributes": attributes,
			"count":      len(results),
		}, "denied", err)
		return nil, dbustypes.ErrAccessDenied(err.Error())
	}

	c.logger.LogMethod(context.Background(), "Collection.SearchItems", map[string]any{
		"collection": string(path),
		"attributes": attributes,
		"count":      len(results),
	}, "ok", nil)

	return results, nil
}

// CreateItem creates a new item in the collection.
// Signature: CreateItem(properties Dict<String,Variant>, secret Secret, replace Boolean) -> (item ObjectPath, prompt ObjectPath)
func (c *CollectionHandler) CreateItem(msg dbus.Message, properties map[string]dbus.Variant, secret dbustypes.Secret, replace bool) (dbus.ObjectPath, dbus.ObjectPath, *dbus.Error) {
	path := msg.Headers[dbus.FieldPath].Value().(dbus.ObjectPath)
	if !isCollectionPath(path) {
		return "/", "/", dbustypes.ErrObjectNotFound(string(path))
	}

	// Map remote session to local session
	localSession, ok := c.sessions.GetLocalSession(secret.Session)
	if !ok {
		return "/", "/", dbustypes.ErrSessionNotFound(string(secret.Session))
	}

	// Create local secret with local session path
	localSecret := dbustypes.Secret{
		Session:     localSession,
		Parameters:  secret.Parameters,
		Value:       secret.Value,
		ContentType: secret.ContentType,
	}

	obj := c.localConn.Object(dbustypes.BusName, path)
	call := obj.Call(dbustypes.CollectionInterface+".CreateItem", 0, properties, localSecret, replace)
	if call.Err != nil {
		return "/", "/", &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []interface{}{call.Err.Error()}}
	}

	var item, prompt dbus.ObjectPath
	if err := call.Store(&item, &prompt); err != nil {
		return "/", "/", &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []interface{}{err.Error()}}
	}

	c.logger.LogMethod(context.Background(), "Collection.CreateItem", map[string]any{
		"collection": string(path),
		"item":       string(item),
	}, "ok", nil)

	return item, prompt, nil
}

// Get implements org.freedesktop.DBus.Properties.Get for collections.
func (c *CollectionHandler) Get(msg dbus.Message, iface, property string) (dbus.Variant, *dbus.Error) {
	path := msg.Headers[dbus.FieldPath].Value().(dbus.ObjectPath)
	if !isCollectionPath(path) {
		return dbus.Variant{}, dbustypes.ErrObjectNotFound(string(path))
	}

	obj := c.localConn.Object(dbustypes.BusName, path)
	variant, err := obj.GetProperty(iface + "." + property)
	if err != nil {
		return dbus.Variant{}, &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []interface{}{err.Error()}}
	}

	return variant, nil
}

// GetAll implements org.freedesktop.DBus.Properties.GetAll for collections.
func (c *CollectionHandler) GetAll(msg dbus.Message, iface string) (map[string]dbus.Variant, *dbus.Error) {
	path := msg.Headers[dbus.FieldPath].Value().(dbus.ObjectPath)
	if !isCollectionPath(path) {
		return nil, dbustypes.ErrObjectNotFound(string(path))
	}

	obj := c.localConn.Object(dbustypes.BusName, path)
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

// Set implements org.freedesktop.DBus.Properties.Set for collections.
func (c *CollectionHandler) Set(msg dbus.Message, iface, property string, value dbus.Variant) *dbus.Error {
	path := msg.Headers[dbus.FieldPath].Value().(dbus.ObjectPath)
	if !isCollectionPath(path) {
		return dbustypes.ErrObjectNotFound(string(path))
	}

	obj := c.localConn.Object(dbustypes.BusName, path)
	call := obj.Call("org.freedesktop.DBus.Properties.Set", 0, iface, property, value)
	if call.Err != nil {
		return &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []interface{}{call.Err.Error()}}
	}

	return nil
}

// getItemInfo fetches label and attributes for a secret item from D-Bus.
func (c *CollectionHandler) getItemInfo(path dbus.ObjectPath) approval.ItemInfo {
	info := approval.ItemInfo{Path: string(path)}

	obj := c.localConn.Object(dbustypes.BusName, path)

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
