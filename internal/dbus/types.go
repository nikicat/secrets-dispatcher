// Package dbus provides D-Bus type definitions for the Secret Service API.
package dbus

import (
	"strings"

	"github.com/godbus/dbus/v5"
)

// A D-Bus interface name, such as org.freedesktop.Secret.Item.
type Interface string

// Interfaces of the Secret Service and of D-Bus itself.
const (
	ServiceInterface        Interface = "org.freedesktop.Secret.Service"
	CollectionInterface     Interface = "org.freedesktop.Secret.Collection"
	ItemInterface           Interface = "org.freedesktop.Secret.Item"
	SessionInterface        Interface = "org.freedesktop.Secret.Session"
	PromptInterface         Interface = "org.freedesktop.Secret.Prompt"
	PropertiesInterface     Interface = "org.freedesktop.DBus.Properties"
	IntrospectableInterface Interface = "org.freedesktop.DBus.Introspectable"
	BusDaemonInterface      Interface = "org.freedesktop.DBus"
)

// The bus daemon's name and object path.
const (
	BusDaemonName                 = "org.freedesktop.DBus"
	BusDaemonPath dbus.ObjectPath = "/org/freedesktop/DBus"
)

// Qualifies a method, signal or property with the interface, as godbus Call,
// Emit and GetProperty take it.
func (i Interface) Member(name string) string {
	return string(i) + "." + name
}

// Qualified property names, as Properties.Get and CreateItem take them. They
// stay strings: godbus passes property dicts as map[string]dbus.Variant, so a
// type of their own would be converted back at every lookup.
const (
	CollectionLabel = string(CollectionInterface) + ".Label"
	ItemLabel       = string(ItemInterface) + ".Label"
	ItemAttributes  = string(ItemInterface) + ".Attributes"
)

// Object paths, bus name and session algorithms of the Secret Service.
const (
	ServicePath = "/org/freedesktop/secrets"
	BusName     = "org.freedesktop.secrets"

	// Roots of the collection and item paths. A collection lies under
	// CollectionsPath by its name and under AliasesPath by its alias.
	CollectionsPath = ServicePath + "/collection"
	AliasesPath     = ServicePath + "/aliases"

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

// The name of a D-Bus error, such as org.freedesktop.Secret.Error.IsLocked.
type ErrorName string

// Error names that the Secret Service returns, from its own specification and
// from D-Bus.
const (
	IsLocked         ErrorName = "org.freedesktop.Secret.Error.IsLocked"
	NoSession        ErrorName = "org.freedesktop.Secret.Error.NoSession"
	NoSuchObject     ErrorName = "org.freedesktop.Secret.Error.NoSuchObject"
	AlreadyExists    ErrorName = "org.freedesktop.Secret.Error.AlreadyExists"
	NotSupported     ErrorName = "org.freedesktop.DBus.Error.NotSupported"
	InvalidSignature ErrorName = "org.freedesktop.DBus.Error.InvalidSignature"
	UnknownInterface ErrorName = "org.freedesktop.DBus.Error.UnknownInterface"
	AccessDenied     ErrorName = "org.freedesktop.DBus.Error.AccessDenied"
	Failed           ErrorName = "org.freedesktop.DBus.Error.Failed"
)

// Returns the error of this name with the given message.
func (n ErrorName) WithMessage(message string) *dbus.Error {
	return &dbus.Error{Name: string(n), Body: []any{message}}
}

// ErrLocked returns an IsLocked error.
func ErrLocked(path string) *dbus.Error {
	return IsLocked.WithMessage("Object " + path + " is locked")
}

// ErrSessionNotFound returns a NoSession error.
func ErrSessionNotFound(path string) *dbus.Error {
	return NoSession.WithMessage("Session " + path + " does not exist")
}

// ErrObjectNotFound returns a NoSuchObject error.
func ErrObjectNotFound(path string) *dbus.Error {
	return NoSuchObject.WithMessage("Object " + path + " does not exist")
}

// Returns an UnknownInterface error that names the interface.
func ErrInterfaceNotFound(iface Interface) *dbus.Error {
	return UnknownInterface.WithMessage(string(iface))
}

// ErrUnsupportedAlgorithm returns a NotSupported error for unknown algorithms.
func ErrUnsupportedAlgorithm(algo string) *dbus.Error {
	return NotSupported.WithMessage("Algorithm " + algo + " is not supported")
}

// ErrAccessDenied returns an error when access to a secret is denied.
func ErrAccessDenied(message string) *dbus.Error {
	return AccessDenied.WithMessage(message)
}

// ErrFailed returns a generic Failed error wrapping the underlying error.
func ErrFailed(err error) *dbus.Error {
	return Failed.WithMessage(err.Error())
}

// A collection as an object path names it.
type CollectionRef struct {
	// The collection's name, or an alias such as "default".
	Name string
	// True if Name is an alias. False otherwise.
	Alias bool
}

// Returns the collection that a collection path names. A collection path is
// CollectionsPath/C or AliasesPath/C.
func ParseCollectionPath(path dbus.ObjectPath) (CollectionRef, bool) {
	if ref, after, ok := cutCollection(path); ok && ref.Name != "" && after == "" {
		return ref, true
	}
	return CollectionRef{}, false
}

// Returns the collection that an item path lies in. An item path is a
// collection path followed by /I.
func ParseItemPath(path dbus.ObjectPath) (CollectionRef, bool) {
	ref, after, ok := cutCollection(path)
	if !ok || ref.Name == "" {
		return CollectionRef{}, false
	}
	item, ok := strings.CutPrefix(after, "/")
	if !ok || item == "" || strings.Contains(item, "/") {
		return CollectionRef{}, false
	}
	return ref, true
}

// Returns the collection segment of a path under CollectionsPath or
// AliasesPath, and the rest of the path after that segment.
func cutCollection(path dbus.ObjectPath) (ref CollectionRef, after string, ok bool) {
	p := string(path)
	rest, ok := strings.CutPrefix(p, CollectionsPath+"/")
	if !ok {
		if rest, ok = strings.CutPrefix(p, AliasesPath+"/"); !ok {
			return CollectionRef{}, "", false
		}
		ref.Alias = true
	}
	end := strings.IndexByte(rest, '/')
	if end < 0 {
		end = len(rest)
	}
	ref.Name = rest[:end]
	return ref, rest[end:], true
}

// Returns the collection that a collection path names or that an item path
// lies in, or false for any other path.
func CollectionOf(path dbus.ObjectPath) (CollectionRef, bool) {
	if ref, ok := ParseCollectionPath(path); ok {
		return ref, true
	}
	return ParseItemPath(path)
}
