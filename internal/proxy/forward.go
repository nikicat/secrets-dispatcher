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

// callForwarder relays a received method call to a peer connection verbatim:
// it re-issues the same interface.member with the same body on dst (addressed
// to dstName at the message's own object path) and returns the reply.
//
// It is the shared engine for every interface the dispatcher proxies 1:1
// between the front (session) and backend buses — the Secret Service Prompt
// interface (front→backend) and the gnome-keyring prompter bridge in both
// directions — so they all forward through one code path instead of a
// hand-written relay per method. Forwarding the body verbatim also keeps the
// dispatcher a transparent pipe: the prompter exchange (which carries the
// DH-encrypted keyring password) passes through untouched and unread.
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

// nameHasOwner reports whether name currently has an owner on conn. An
// activatable-but-not-running name reports false — which is what lets the
// prompter bridge claim org.gnome.keyring.SystemPrompter on the backend bus
// before the display-less gcr-prompter fallback would be activated.
func nameHasOwner(conn *dbus.Conn, name string) bool {
	var has bool
	if err := conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, name).Store(&has); err != nil {
		return false
	}
	return has
}
