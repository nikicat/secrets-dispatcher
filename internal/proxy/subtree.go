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

// upstream returns the backend object at path on the upstream Secret Service bus.
func (h *SubtreePropertiesHandler) upstream(path dbus.ObjectPath) dbus.BusObject {
	return h.localConn.Object(dbustypes.BusName, path)
}

// Get implements org.freedesktop.DBus.Properties.Get for collections and items.
func (h *SubtreePropertiesHandler) Get(msg dbus.Message, iface, property string) (dbus.Variant, *dbus.Error) {
	path := pathOf(msg)

	// Route based on path type
	if !(collectionObjects{}).serves(path) && !(itemObjects{}).serves(path) {
		return dbus.Variant{}, dbustypes.ErrObjectNotFound(string(path))
	}

	obj := h.upstream(path)
	variant, err := obj.GetProperty(dbustypes.Interface(iface).Member(property))
	if err != nil {
		return dbus.Variant{}, dbustypes.ErrFailed(err)
	}

	return variant, nil
}

// GetAll implements org.freedesktop.DBus.Properties.GetAll for collections and items.
func (h *SubtreePropertiesHandler) GetAll(msg dbus.Message, iface string) (map[string]dbus.Variant, *dbus.Error) {
	path := pathOf(msg)

	if !(collectionObjects{}).serves(path) && !(itemObjects{}).serves(path) {
		return nil, dbustypes.ErrObjectNotFound(string(path))
	}

	obj := h.upstream(path)
	call := obj.Call(dbustypes.PropertiesInterface.Member("GetAll"), 0, iface)
	if call.Err != nil {
		return nil, dbustypes.ErrFailed(call.Err)
	}

	var props map[string]dbus.Variant
	if err := call.Store(&props); err != nil {
		return nil, dbustypes.ErrFailed(err)
	}

	return props, nil
}

// Set implements org.freedesktop.DBus.Properties.Set for collections and items.
func (h *SubtreePropertiesHandler) Set(msg dbus.Message, iface, property string, value dbus.Variant) *dbus.Error {
	path := pathOf(msg)

	if !(collectionObjects{}).serves(path) && !(itemObjects{}).serves(path) {
		return dbustypes.ErrObjectNotFound(string(path))
	}

	obj := h.upstream(path)
	call := obj.Call(dbustypes.PropertiesInterface.Member("Set"), 0, iface, property, value)
	if call.Err != nil {
		return dbustypes.ErrFailed(call.Err)
	}

	return nil
}
