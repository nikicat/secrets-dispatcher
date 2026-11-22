package proxy

import (
	"errors"

	"github.com/godbus/dbus/v5"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
)

// memberOf returns the method/member name of an incoming message.
func memberOf(msg dbus.Message) string {
	if v, ok := msg.Headers[dbus.FieldMember]; ok {
		if s, ok := v.Value().(string); ok {
			return s
		}
	}
	return ""
}

// interfaceOf returns the interface name of an incoming message.
func interfaceOf(msg dbus.Message) string {
	if v, ok := msg.Headers[dbus.FieldInterface]; ok {
		if s, ok := v.Value().(string); ok {
			return s
		}
	}
	return ""
}

// Relays a method call to the same path, interface and member on another
// connection, with its body unread, so a prompter exchange passes through
// sealed (ADR 0009).
type callForwarder struct {
	dst     *dbus.Conn
	dstName string
}

// forward re-issues msg on the destination connection and returns the reply
// body, propagating a remote D-Bus error faithfully.
func (f callForwarder) forward(msg dbus.Message) ([]any, *dbus.Error) {
	obj := f.dst.Object(f.dstName, pathOf(msg))
	call := obj.Call(interfaceOf(msg)+"."+memberOf(msg), 0, msg.Body...)
	if call.Err != nil {
		if derr, ok := errors.AsType[dbus.Error](call.Err); ok {
			return nil, &derr
		}
		return nil, dbustypes.ErrFailed(call.Err)
	}
	return call.Body, nil
}

// forwardVoid forwards a call whose reply carries no body (the Prompt and
// Prompter/Callback methods all return nothing; their result arrives later as
// a signal).
func (f callForwarder) forwardVoid(msg dbus.Message) *dbus.Error {
	_, err := f.forward(msg)
	return err
}
