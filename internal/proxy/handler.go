package proxy

import (
	"context"

	"github.com/godbus/dbus/v5"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/logging"
)

// The part of a handler that exists before its buses are connected.
type handlerBase struct {
	logger       *logging.Logger
	approval     *approval.Manager
	slowUpstream SlowUpstream
}

type handler struct {
	handlerBase
	// Reaches the real Secret Service, on the session bus or a private bus.
	backendConn *dbus.Conn
	sessions    *SessionManager
	tracker     *clientTracker
	resolver    *SenderInfoResolver
}

// Stops tracking clients, closes the backend sessions and closes the backend
// connection.
func (h *handler) close(ctx context.Context) {
	h.tracker.close()
	h.sessions.CloseAll(ctx)
	h.backendConn.Close()
}

// Returns the context of an upstream call made for the given message outside
// any approval request. The message's sender is resolved only if the call is
// slow.
func (h *handler) callContext(msg dbus.Message) UpstreamCallContext {
	sender := senderOf(msg)
	return SenderCall(func() approval.SenderInfo { return h.resolver.Resolve(sender) })
}

// Calls the given method, qualified by its interface, on the upstream object
// at the given path. A call that outlasts the slow-upstream threshold is
// reported with the caller and request that the call context names.
func (h *handler) callUpstream(ctx UpstreamCallContext, path dbus.ObjectPath, method string, args ...any) *dbus.Call {
	obj := h.backendConn.Object(dbustypes.BusName, path)
	return WithSlowNotify(h.slowUpstream, ctx, func() *dbus.Call { return obj.Call(method, 0, args...) })
}

// Returns the given property, qualified by its interface, of the upstream
// object at the given path, and reports a slow call as callUpstream does.
func (h *handler) getProperty(ctx UpstreamCallContext, path dbus.ObjectPath, property string) (dbus.Variant, error) {
	obj := h.backendConn.Object(dbustypes.BusName, path)
	type propResult struct {
		v   dbus.Variant
		err error
	}
	r := WithSlowNotify(h.slowUpstream, ctx, func() propResult {
		v, err := obj.GetProperty(property)
		return propResult{v, err}
	})
	return r.v, r.err
}

// The client behind one call, with its sender resolved.
type caller struct {
	h      *handler
	sender approval.SenderInfo
}

// Returns the caller of the given message.
func (h *handler) caller(msg dbus.Message) caller {
	return caller{h: h, sender: h.resolver.Resolve(senderOf(msg))}
}

// Returns the label and attributes of the upstream item at the given path. Both
// are empty when the backend gives either with the wrong type, which is logged.
func (c caller) itemInfo(path dbus.ObjectPath) approval.ItemInfo {
	props := make(map[string]dbus.Variant, 2)
	for _, property := range []string{dbustypes.ItemLabel, dbustypes.ItemAttributes} {
		if v, err := c.h.getProperty(ResolvedSenderCall(c.sender), path, property); err == nil {
			props[property] = v
		}
	}
	info, err := itemInfoFromProperties(string(path), props)
	if err != nil {
		c.h.logger.Warn("backend item has a mistyped property", "path", path, "error", err)
		return approval.ItemInfo{Path: string(path)}
	}
	return info
}

// Returns the upstream collection at the given path as an item.
func (c caller) collectionInfo(path dbus.ObjectPath) approval.ItemInfo {
	info := approval.ItemInfo{Path: string(path)}
	if v, err := c.h.getProperty(ResolvedSenderCall(c.sender), path, dbustypes.CollectionLabel); err == nil {
		info.Label, _ = v.Value().(string)
	}
	return info
}

// Returns the upstream item or collection at the given path as an item.
func (c caller) objectInfo(path dbus.ObjectPath) approval.ItemInfo {
	if _, ok := dbustypes.ParseItemPath(path); ok {
		return c.itemInfo(path)
	}
	return c.collectionInfo(path)
}

// Lets the call's sender make the given request without a prompt, as
// approval.Manager.Passthrough does. Returns the context of the upstream call
// that the request is for.
func (c caller) passthrough(info approval.RequestInfo) (UpstreamCallContext, error) {
	info.SenderInfo = c.sender
	if err := c.h.approval.Passthrough(info); err != nil {
		return UpstreamCallContext{}, err
	}
	return RequestCall(info), nil
}

// The caller of a call that opens an approval request. Its context ends when the
// client disconnects, and that cancels the request.
type approvalCaller struct {
	caller
	ctx context.Context
}

// Returns the approval caller of the given message, and the function that ends
// its context once the call is served.
func (h *handler) startApprovalCaller(msg dbus.Message) (approvalCaller, func()) {
	ctx, release := h.tracker.contextForSender(context.Background(), senderOf(msg))
	return approvalCaller{caller: h.caller(msg), ctx: ctx}, release
}

// Requires the call's sender to get the user's approval of the given request,
// as approval.Manager.RequireApproval does. Returns the context of the
// upstream call that the request is for.
func (c approvalCaller) requireApproval(info approval.RequestInfo) (UpstreamCallContext, error) {
	info.SenderInfo = c.sender
	prompted, err := c.h.approval.RequireApproval(c.ctx, info)
	if prompted {
		return PromptedCall(info), err
	}
	return RequestCall(info), err
}

// Returns the item at the given path that the given property dict describes.
// Errors when a property has the wrong type.
func itemInfoFromProperties(path string, props map[string]dbus.Variant) (approval.ItemInfo, error) {
	p, err := dbustypes.ParseItemProperties(props)
	if err != nil {
		return approval.ItemInfo{}, err
	}
	return approval.ItemInfo{Path: path, Label: p.Label, Attributes: p.Attributes}, nil
}

// Serves org.freedesktop.DBus.Properties by forwarding each call upstream, to
// the object that servedObjects picks.
type properties struct {
	*handler
	servedObjects
}

// The upstream objects that one handler type serves.
type servedObjects interface {
	// Returns the path of the upstream object that a call on the given
	// interface addresses. Errors with the D-Bus error to reply with when the
	// handler does not serve that object.
	object(msg dbus.Message, iface dbustypes.Interface) (dbus.ObjectPath, *dbus.Error)
}

// Upstream objects that their path alone picks out, whatever the interface.
type pathSet interface {
	serves(path dbus.ObjectPath) bool
}

// Returns the path of the called object. Errors with NoSuchObject when the
// given set does not hold it.
func calledPathIn(set pathSet, msg dbus.Message) (dbus.ObjectPath, *dbus.Error) {
	path := dbustypes.PathOf(msg)
	if !set.serves(path) {
		return "", dbustypes.ErrObjectNotFound(string(path))
	}
	return path, nil
}

func (p properties) Get(msg dbus.Message, iface, property string) (dbus.Variant, *dbus.Error) {
	called := dbustypes.Interface(iface)
	path, dbusErr := p.object(msg, called)
	if dbusErr != nil {
		return dbus.Variant{}, dbusErr
	}
	v, err := p.getProperty(p.callContext(msg), path, called.Member(property))
	if err != nil {
		return dbus.Variant{}, dbustypes.ErrFailed(err)
	}
	return v, nil
}

func (p properties) GetAll(msg dbus.Message, iface string) (map[string]dbus.Variant, *dbus.Error) {
	path, dbusErr := p.object(msg, dbustypes.Interface(iface))
	if dbusErr != nil {
		return nil, dbusErr
	}
	call := p.callUpstream(p.callContext(msg), path, dbustypes.PropertiesInterface.Member("GetAll"), iface)
	var props map[string]dbus.Variant
	if err := call.Store(&props); err != nil {
		return nil, dbustypes.ErrFailed(err)
	}
	return props, nil
}

func (p properties) Set(msg dbus.Message, iface, property string, value dbus.Variant) *dbus.Error {
	path, dbusErr := p.object(msg, dbustypes.Interface(iface))
	if dbusErr != nil {
		return dbusErr
	}
	call := p.callUpstream(p.callContext(msg), path, dbustypes.PropertiesInterface.Member("Set"), iface, property, value)
	if call.Err != nil {
		return dbustypes.ErrFailed(call.Err)
	}
	return nil
}
