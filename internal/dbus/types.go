// Package dbus provides D-Bus type definitions for the Secret Service API.
package dbus

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
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

// The attributes of a Secret Service item, or of a search for items, each key
// to its value.
type Attributes map[string]string

// Returns the attributes as key=value pairs sorted by key, joined by ", ".
func (a Attributes) String() string {
	pairs := make([]string, 0, len(a))
	for _, k := range slices.Sorted(maps.Keys(a)) {
		pairs = append(pairs, k+"="+a[k])
	}
	return strings.Join(pairs, ", ")
}

// Checks whether the attributes hold every key of the given ones with the same
// value. A missing key reads as the empty string.
func (a Attributes) Contain(want Attributes) bool {
	for k, v := range want {
		if a[k] != v {
			return false
		}
	}
	return true
}

// The label and attributes of an item, as CreateItem takes them.
type ItemProperties struct {
	Label      string
	Attributes Attributes
}

// Returns the property dict that CreateItem takes.
func (p ItemProperties) Variants() map[string]dbus.Variant {
	return map[string]dbus.Variant{
		ItemLabel: dbus.MakeVariant(p.Label),
		// The plain map type, which ParseItemProperties reads back.
		ItemAttributes: dbus.MakeVariant(map[string]string(p.Attributes)),
	}
}

// Reads the label and attributes from the given property dict. Errors with
// InvalidArgs when the label is not a string or the attributes are not a
// string-to-string dict.
func ParseItemProperties(props map[string]dbus.Variant) (ItemProperties, error) {
	label, err := propertyValue[string](props, ItemLabel)
	if err != nil {
		return ItemProperties{}, err
	}
	attributes, err := propertyValue[map[string]string](props, ItemAttributes)
	if err != nil {
		return ItemProperties{}, err
	}
	return ItemProperties{Label: label, Attributes: attributes}, nil
}

// Returns the value of the named property in the given dict. Errors with
// InvalidArgs when the value is not a T.
func propertyValue[T any](props map[string]dbus.Variant, name string) (T, error) {
	var value T
	v, ok := props[name]
	if !ok {
		return value, nil
	}
	if value, ok = v.Value().(T); !ok {
		return value, InvalidArgs.WithMessage(fmt.Sprintf("property %s has type %s, not %s", name, v.Signature(), dbus.SignatureOf(value)))
	}
	return value, nil
}

// Returns the value of the given header field of an incoming message.
func Header[T any](msg dbus.Message, field dbus.HeaderField) T {
	v, _ := msg.Headers[field].Value().(T)
	return v
}

// Returns the object path an incoming message is addressed to.
func PathOf(msg dbus.Message) dbus.ObjectPath {
	return Header[dbus.ObjectPath](msg, dbus.FieldPath)
}

// Object paths, bus name and session algorithms of the Secret Service.
const (
	ServicePath = "/org/freedesktop/secrets"
	BusName     = "org.freedesktop.secrets"

	// Roots of the collection and item paths. A collection lies under
	// CollectionsPath by its name and under AliasesPath by its alias.
	CollectionsPath = ServicePath + "/collection"
	AliasesPath     = ServicePath + "/aliases"

	// Roots of the prompt and session paths.
	PromptsPath  = ServicePath + "/prompt"
	SessionsPath = ServicePath + "/session"

	// Algorithms for OpenSession.
	AlgorithmPlain = "plain"
	AlgorithmDH    = "dh-ietf1024-sha256-aes128-cbc-pkcs7"
)

// The path that the Secret Service returns for no object or for no prompt.
const NoObject dbus.ObjectPath = "/"

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
	InvalidArgs      ErrorName = "org.freedesktop.DBus.Error.InvalidArgs"
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

// Returns the D-Bus error that the given error holds, by value or by pointer,
// or else a Failed error with its message.
func AsDBusError(err error) *dbus.Error {
	if derr, ok := errors.AsType[*dbus.Error](err); ok {
		return derr
	}
	if derr, ok := errors.AsType[dbus.Error](err); ok {
		return &derr
	}
	return ErrFailed(err)
}

// A collection's name, or an alias such as "default".
type CollectionName string

// A collection as an object path names it.
type CollectionRef struct {
	Name CollectionName
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
	ref.Name = CollectionName(rest[:end])
	return ref, rest[end:], true
}

// Returns the object path of the session with the given id.
func SessionPath(id uint64) dbus.ObjectPath {
	return dbus.ObjectPath(SessionsPath + "/" + strconv.FormatUint(id, 10))
}

// Returns the object path of the prompt with the given name.
func PromptPath(name string) dbus.ObjectPath {
	return dbus.ObjectPath(PromptsPath + "/" + name)
}

// Checks whether the given path names an object under PromptsPath.
func IsPromptPath(path dbus.ObjectPath) bool {
	name, ok := strings.CutPrefix(string(path), PromptsPath+"/")
	return ok && name != ""
}

// Returns the collection that a collection path names or that an item path
// lies in, or false for any other path.
func CollectionOf(path dbus.ObjectPath) (CollectionRef, bool) {
	if ref, ok := ParseCollectionPath(path); ok {
		return ref, true
	}
	return ParseItemPath(path)
}
