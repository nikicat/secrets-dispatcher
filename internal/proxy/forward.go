package proxy

import (
	"github.com/godbus/dbus/v5"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/sender"
)

// Returns the unique D-Bus name of the sender of an incoming message.
func senderOf(msg dbus.Message) sender.UniqueName {
	return sender.UniqueName(dbustypes.Header[string](msg, dbus.FieldSender))
}

// Returns the member name of an incoming message.
func memberOf(msg dbus.Message) string {
	return dbustypes.Header[string](msg, dbus.FieldMember)
}

// Returns the interface of an incoming message.
func interfaceOf(msg dbus.Message) dbustypes.Interface {
	return dbustypes.Interface(dbustypes.Header[string](msg, dbus.FieldInterface))
}

// Relays a method call to the same path, interface and member on another
// connection, with its body unread, so a prompter exchange passes through
// sealed (ADR 0009).
type callForwarder struct {
	dst     *dbus.Conn
	dstName string
}

// Re-issues the given call on the destination connection and returns the
// reply body, or the remote D-Bus error unchanged.
func (f callForwarder) forward(msg dbus.Message) ([]any, *dbus.Error) {
	obj := f.dst.Object(f.dstName, dbustypes.PathOf(msg))
	call := obj.Call(interfaceOf(msg).Member(memberOf(msg)), 0, msg.Body...)
	if call.Err != nil {
		return nil, dbustypes.AsDBusError(call.Err)
	}
	return call.Body, nil
}

// Forwards the given call and drops the reply body.
func (f callForwarder) forwardVoid(msg dbus.Message) *dbus.Error {
	_, err := f.forward(msg)
	return err
}
