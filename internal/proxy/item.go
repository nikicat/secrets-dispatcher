package proxy

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/logging"
)

// ItemHandler handles Item interface calls for item objects.
// It is exported as a subtree handler for /org/freedesktop/secrets/collection/*/*.
type ItemHandler struct {
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

// NewItemHandler creates a new ItemHandler.
func NewItemHandler(localConn *dbus.Conn, sessions *SessionManager, logger *logging.Logger, approvalMgr *approval.Manager, clientName string, tracker *clientTracker, resolver *SenderInfoResolver, upstreamNotifier UpstreamNotifier, slowThreshold time.Duration) *ItemHandler {
	return &ItemHandler{
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

// upstream returns the backend object at path on the upstream Secret Service bus.
func (i *ItemHandler) upstream(path dbus.ObjectPath) dbus.BusObject {
	return i.localConn.Object(dbustypes.BusName, path)
}

// upstreamWithContext wraps a D-Bus Call through the slow-upstream notifier,
// passing caller and item context to the notification.
func (i *ItemHandler) upstreamWithContext(ctx UpstreamCallContext, fn func() *dbus.Call) *dbus.Call {
	return WithSlowNotify(i.slowThreshold, i.upstreamNotifier, ctx, fn)
}

// upstreamGetProperty wraps a GetProperty call through the slow-upstream notifier,
// passing caller context to the notification so the user knows what triggered any
// pinentry or keyring unlock prompt.
func (i *ItemHandler) upstreamGetProperty(obj dbus.BusObject, prop string, ctx UpstreamCallContext) (dbus.Variant, error) {
	r := WithSlowNotify(i.slowThreshold, i.upstreamNotifier, ctx, func() propResult {
		v, err := obj.GetProperty(prop)
		return propResult{v, err}
	})
	return r.v, r.err
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

	// Fetch item info (label + attributes)
	sender := senderOf(msg)
	senderCtx := UpstreamCallContext{
		ResolveSender: func() approval.SenderInfo { return i.resolver.Resolve(sender) },
	}
	itemInfo := i.getItemInfo(path, senderCtx)

	// Get a context that will be cancelled if the client disconnects
	ctx, release := i.tracker.contextForSender(context.Background(), sender)
	defer release()

	// Resolve sender information
	senderInfo := i.resolver.Resolve(sender)

	// Require approval before deleting
	items := []approval.ItemInfo{itemInfo}
	if _, err := i.approval.RequireApproval(ctx, i.clientName, items, "", approval.RequestTypeDelete, nil, senderInfo); err != nil {
		i.logger.LogMethod(ctx, "Item.Delete", map[string]any{"item": string(path)}, "denied", err)
		return "/", dbustypes.ErrAccessDenied(err.Error())
	}

	obj := i.upstream(path)
	call := i.upstreamWithContext(UpstreamCallContext{
		RequestType: approval.RequestTypeDelete,
		Items:       items,
		SenderInfo:  senderInfo,
	}, func() *dbus.Call { return obj.Call(dbustypes.ItemInterface+".Delete", 0) })
	if call.Err != nil {
		return "/", dbustypes.ErrFailed(call.Err)
	}

	var prompt dbus.ObjectPath
	if err := call.Store(&prompt); err != nil {
		return "/", dbustypes.ErrFailed(err)
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
	sender := senderOf(msg)
	senderCtx := UpstreamCallContext{
		ResolveSender: func() approval.SenderInfo { return i.resolver.Resolve(sender) },
	}
	itemInfo := i.getItemInfo(path, senderCtx)

	// Get a context that will be cancelled if the client disconnects
	ctx, release := i.tracker.contextForSender(context.Background(), sender)
	defer release()

	// Resolve sender information
	senderInfo := i.resolver.Resolve(sender)

	// Require approval before accessing secret
	items := []approval.ItemInfo{itemInfo}
	_, err := i.approval.RequireApproval(ctx, i.clientName, items, string(session), approval.RequestTypeGetSecret, nil, senderInfo)
	if err != nil {
		i.logger.LogItemGetSecret(ctx, string(path), "denied", err)
		return dbustypes.Secret{}, dbustypes.ErrAccessDenied(err.Error())
	}

	// Map remote session to local session
	localSession, ok := i.sessions.GetLocalSession(session)
	if !ok {
		i.logger.LogItemGetSecret(context.Background(), string(path), "error", dbustypes.ErrSessionNotFound(string(session)))
		return dbustypes.Secret{}, dbustypes.ErrSessionNotFound(string(session))
	}

	obj := i.upstream(path)
	call := i.upstreamWithContext(UpstreamCallContext{
		RequestType: approval.RequestTypeGetSecret,
		Items:       items,
		SenderInfo:  senderInfo,
	}, func() *dbus.Call { return obj.Call(dbustypes.ItemInterface+".GetSecret", 0, localSession) })
	if call.Err != nil {
		i.logger.LogItemGetSecret(context.Background(), string(path), "error", call.Err)
		return dbustypes.Secret{}, dbustypes.ErrFailed(call.Err)
	}

	var secret dbustypes.Secret
	if err := call.Store(&secret); err != nil {
		i.logger.LogItemGetSecret(context.Background(), string(path), "error", err)
		return dbustypes.Secret{}, dbustypes.ErrFailed(err)
	}

	// Rewrite session path and, for DH sessions, encrypt the value for the client.
	encoded, err := i.sessions.ForClient(session, secret)
	if err != nil {
		i.logger.LogItemGetSecret(context.Background(), string(path), "error", err)
		return dbustypes.Secret{}, dbustypes.ErrFailed(err)
	}

	i.logger.LogItemGetSecret(context.Background(), string(path), "ok", nil)
	return encoded, nil
}

// SetSecret sets the secret for this item.
// Signature: SetSecret(secret Secret)
func (i *ItemHandler) SetSecret(msg dbus.Message, secret dbustypes.Secret) *dbus.Error {
	path := msg.Headers[dbus.FieldPath].Value().(dbus.ObjectPath)
	if !isItemPath(path) {
		return dbustypes.ErrObjectNotFound(string(path))
	}

	// Fetch item info (label + attributes)
	sender := senderOf(msg)
	senderCtx := UpstreamCallContext{
		ResolveSender: func() approval.SenderInfo { return i.resolver.Resolve(sender) },
	}
	itemInfo := i.getItemInfo(path, senderCtx)

	// Get a context that will be cancelled if the client disconnects
	ctx, release := i.tracker.contextForSender(context.Background(), sender)
	defer release()

	// Resolve sender information
	senderInfo := i.resolver.Resolve(sender)

	// Require approval before writing secret
	items := []approval.ItemInfo{itemInfo}
	if _, err := i.approval.RequireApproval(ctx, i.clientName, items, string(secret.Session), approval.RequestTypeWrite, nil, senderInfo); err != nil {
		if errors.Is(err, approval.ErrIgnored) {
			i.logger.LogMethod(ctx, "Item.SetSecret", map[string]any{
				"item": string(path), "ignored": true,
			}, "ignored", nil)
			return nil
		}
		i.logger.LogMethod(ctx, "Item.SetSecret", map[string]any{"item": string(path)}, "denied", err)
		return dbustypes.ErrAccessDenied(err.Error())
	}

	// Map the remote session to the upstream session, decrypting the value for
	// DH sessions before forwarding it to the (plain) upstream service.
	localSecret, ok, err := i.sessions.ForUpstream(secret)
	if !ok {
		return dbustypes.ErrSessionNotFound(string(secret.Session))
	}
	if err != nil {
		i.logger.LogMethod(ctx, "Item.SetSecret", map[string]any{"item": string(path)}, "error", err)
		return dbustypes.ErrFailed(err)
	}

	obj := i.upstream(path)
	call := i.upstreamWithContext(UpstreamCallContext{
		RequestType: approval.RequestTypeWrite,
		Items:       items,
		SenderInfo:  senderInfo,
	}, func() *dbus.Call { return obj.Call(dbustypes.ItemInterface+".SetSecret", 0, localSecret) })
	if call.Err != nil {
		return dbustypes.ErrFailed(call.Err)
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

	obj := i.upstream(path)
	sender := senderOf(msg)
	r := WithSlowNotify(i.slowThreshold, i.upstreamNotifier, UpstreamCallContext{
		ResolveSender: func() approval.SenderInfo { return i.resolver.Resolve(sender) },
	}, func() propResult {
		v, err := obj.GetProperty(iface + "." + property)
		return propResult{v, err}
	})
	if r.err != nil {
		return dbus.Variant{}, dbustypes.ErrFailed(r.err)
	}

	return r.v, nil
}

// GetAll implements org.freedesktop.DBus.Properties.GetAll for items.
func (i *ItemHandler) GetAll(msg dbus.Message, iface string) (map[string]dbus.Variant, *dbus.Error) {
	path := msg.Headers[dbus.FieldPath].Value().(dbus.ObjectPath)
	if !isItemPath(path) {
		return nil, dbustypes.ErrObjectNotFound(string(path))
	}

	obj := i.upstream(path)
	sender := senderOf(msg)
	call := i.upstreamWithContext(UpstreamCallContext{
		ResolveSender: func() approval.SenderInfo { return i.resolver.Resolve(sender) },
	}, func() *dbus.Call { return obj.Call("org.freedesktop.DBus.Properties.GetAll", 0, iface) })
	if call.Err != nil {
		return nil, dbustypes.ErrFailed(call.Err)
	}

	var props map[string]dbus.Variant
	if err := call.Store(&props); err != nil {
		return nil, dbustypes.ErrFailed(err)
	}

	return props, nil
}

// Set implements org.freedesktop.DBus.Properties.Set for items.
func (i *ItemHandler) Set(msg dbus.Message, iface, property string, value dbus.Variant) *dbus.Error {
	path := msg.Headers[dbus.FieldPath].Value().(dbus.ObjectPath)
	if !isItemPath(path) {
		return dbustypes.ErrObjectNotFound(string(path))
	}

	obj := i.upstream(path)
	sender := senderOf(msg)
	call := i.upstreamWithContext(UpstreamCallContext{
		ResolveSender: func() approval.SenderInfo { return i.resolver.Resolve(sender) },
	}, func() *dbus.Call {
		return obj.Call("org.freedesktop.DBus.Properties.Set", 0, iface, property, value)
	})
	if call.Err != nil {
		return dbustypes.ErrFailed(call.Err)
	}

	return nil
}

// getItemInfo fetches label and attributes for a secret item from D-Bus.
func (i *ItemHandler) getItemInfo(path dbus.ObjectPath, ctx UpstreamCallContext) approval.ItemInfo {
	info := approval.ItemInfo{Path: string(path)}

	obj := i.upstream(path)

	// Get Label property
	if v, err := i.upstreamGetProperty(obj, dbustypes.ItemInterface+".Label", ctx); err == nil {
		if label, ok := v.Value().(string); ok {
			info.Label = label
		}
	}

	// Get Attributes property
	if v, err := i.upstreamGetProperty(obj, dbustypes.ItemInterface+".Attributes", ctx); err == nil {
		if attrs, ok := v.Value().(map[string]string); ok {
			info.Attributes = attrs
		}
	}

	return info
}
