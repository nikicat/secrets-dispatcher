// Package dbus provides D-Bus type definitions for the Secret Service API.
package dbus

import "github.com/godbus/dbus/v5"

// D-Bus interface and path constants for Secret Service.
const (
	ServiceInterface    = "org.freedesktop.Secret.Service"
	CollectionInterface = "org.freedesktop.Secret.Collection"
	ItemInterface       = "org.freedesktop.Secret.Item"
	SessionInterface    = "org.freedesktop.Secret.Session"
	PromptInterface     = "org.freedesktop.Secret.Prompt"

	ServicePath = "/org/freedesktop/secrets"
	BusName     = "org.freedesktop.secrets"

	// Algorithms for OpenSession.
	AlgorithmPlain = "plain"
	AlgorithmDH    = "dh-ietf1024-sha256-aes128-cbc-pkcs7"
)

// Secret represents a secret value in the Secret Service protocol.
// D-Bus signature: (oayays) - object path, byte array, byte array, string
type Secret struct {
	Session     dbus.ObjectPath // Session used to encode the secret
	Parameters  []byte          // Algorithm-specific parameters (empty for plain)
	Value       []byte          // The secret value (possibly encrypted)
	ContentType string          // MIME content type (e.g., "text/plain")
}

// Error names defined by the Secret Service specification.
const (
	ErrIsLocked         = "org.freedesktop.Secret.Error.IsLocked"
	ErrNoSession        = "org.freedesktop.Secret.Error.NoSession"
	ErrNoSuchObject     = "org.freedesktop.Secret.Error.NoSuchObject"
	ErrAlreadyExists    = "org.freedesktop.Secret.Error.AlreadyExists"
	ErrNotSupported     = "org.freedesktop.DBus.Error.NotSupported"
	ErrInvalidSignature = "org.freedesktop.DBus.Error.InvalidSignature"
)

// NewDBusError creates a D-Bus error with the given name and message.
func NewDBusError(name, message string) *dbus.Error {
	return &dbus.Error{
		Name: name,
		Body: []interface{}{message},
	}
}

// ErrLocked returns an IsLocked error.
func ErrLocked(path string) *dbus.Error {
	return NewDBusError(ErrIsLocked, "Object "+path+" is locked")
}

// ErrSessionNotFound returns a NoSession error.
func ErrSessionNotFound(path string) *dbus.Error {
	return NewDBusError(ErrNoSession, "Session "+path+" does not exist")
}

// ErrObjectNotFound returns a NoSuchObject error.
func ErrObjectNotFound(path string) *dbus.Error {
	return NewDBusError(ErrNoSuchObject, "Object "+path+" does not exist")
}

// ErrUnsupportedAlgorithm returns a NotSupported error for unknown algorithms.
func ErrUnsupportedAlgorithm(algo string) *dbus.Error {
	return NewDBusError(ErrNotSupported, "Algorithm "+algo+" is not supported")
}
