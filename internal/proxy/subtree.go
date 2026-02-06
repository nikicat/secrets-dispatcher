package proxy

import (
	"github.com/godbus/dbus/v5"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/logging"
)

// SubtreePropertiesHandler handles Properties interface for both collections and items.
// It routes based on path type.
type SubtreePropertiesHandler struct {
	localConn *dbus.Conn
	sessions  *SessionManager
	logger    *logging.Logger
}

// NewSubtreePropertiesHandler creates a new handler.
func NewSubtreePropertiesHandler(localConn *dbus.Conn, sessions *SessionManager, logger *logging.Logger) *SubtreePropertiesHandler {
	return &SubtreePropertiesHandler{
		localConn: localConn,
		sessions:  sessions,
		logger:    logger,
	}
}

// Get implements org.freedesktop.DBus.Properties.Get for collections and items.
func (h *SubtreePropertiesHandler) Get(msg dbus.Message, iface, property string) (dbus.Variant, *dbus.Error) {
	path := msg.Headers[dbus.FieldPath].Value().(dbus.ObjectPath)

	// Route based on path type
	if !isCollectionPath(path) && !isItemPath(path) {
		return dbus.Variant{}, dbustypes.ErrObjectNotFound(string(path))
	}

	obj := h.localConn.Object(dbustypes.BusName, path)
	variant, err := obj.GetProperty(iface + "." + property)
	if err != nil {
		return dbus.Variant{}, &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []any{err.Error()}}
	}

	return variant, nil
}

// GetAll implements org.freedesktop.DBus.Properties.GetAll for collections and items.
func (h *SubtreePropertiesHandler) GetAll(msg dbus.Message, iface string) (map[string]dbus.Variant, *dbus.Error) {
	path := msg.Headers[dbus.FieldPath].Value().(dbus.ObjectPath)

	if !isCollectionPath(path) && !isItemPath(path) {
		return nil, dbustypes.ErrObjectNotFound(string(path))
	}

	obj := h.localConn.Object(dbustypes.BusName, path)
	call := obj.Call("org.freedesktop.DBus.Properties.GetAll", 0, iface)
	if call.Err != nil {
		return nil, &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []any{call.Err.Error()}}
	}

	var props map[string]dbus.Variant
	if err := call.Store(&props); err != nil {
		return nil, &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []any{err.Error()}}
	}

	return props, nil
}

// Set implements org.freedesktop.DBus.Properties.Set for collections and items.
func (h *SubtreePropertiesHandler) Set(msg dbus.Message, iface, property string, value dbus.Variant) *dbus.Error {
	path := msg.Headers[dbus.FieldPath].Value().(dbus.ObjectPath)

	if !isCollectionPath(path) && !isItemPath(path) {
		return dbustypes.ErrObjectNotFound(string(path))
	}

	obj := h.localConn.Object(dbustypes.BusName, path)
	call := obj.Call("org.freedesktop.DBus.Properties.Set", 0, iface, property, value)
	if call.Err != nil {
		return &dbus.Error{Name: "org.freedesktop.DBus.Error.Failed", Body: []any{call.Err.Error()}}
	}

	return nil
}
