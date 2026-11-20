package config

import (
	"errors"
	"fmt"

	"github.com/godbus/dbus/v5"
	"gopkg.in/yaml.v3"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/unixsock"
)

// A D-Bus endpoint the daemon reaches: SessionBus, Socket or Sockets.
type Bus interface {
	// Returns the type key that names the variant in YAML.
	Type() BusType
	// Calls the method of v that handles this variant.
	Visit(v BusVisitor) error
	// Errors when the variant lacks the path it needs.
	validate() error
	// Returns the path key in YAML, empty for a variant without one.
	path() string
}

// Handles each variant of Bus. A new variant adds a method here, so every
// visitor fails to compile until it handles that variant.
type BusVisitor interface {
	SessionBus() error
	Socket(Socket) error
	Sockets(Sockets) error
}

// The bus the daemon forwards requests to: SessionBus or Socket.
type Upstream interface {
	Bus
	// Opens a connection to the bus.
	Connect() (*dbus.Conn, error)
	// Why a session_bus downstream cannot go with this upstream, or nil.
	sessionBusDownstream() error
}

// The user's session bus.
type SessionBus struct{}

// One D-Bus socket file.
type Socket struct {
	Path unixsock.Path
}

// Returns the client name of the bus forwarded through the socket. Errors as
// approval.ClientFromSocket does.
func (s Socket) Client() (approval.ClientName, error) { return approval.ClientFromSocket(s.Path) }

// A directory where each client gets its own socket. Only a downstream can be
// one.
type Sockets struct {
	Dir unixsock.Dir
}

func (SessionBus) Type() BusType { return BusSessionBus }
func (Socket) Type() BusType     { return BusSocket }
func (Sockets) Type() BusType    { return BusSockets }

func (SessionBus) Visit(v BusVisitor) error { return v.SessionBus() }
func (s Socket) Visit(v BusVisitor) error   { return v.Socket(s) }
func (s Sockets) Visit(v BusVisitor) error  { return v.Sockets(s) }

func (SessionBus) validate() error { return nil }

func (s Socket) validate() error {
	if s.Path == "" {
		return errNoPath(s.Type())
	}
	return nil
}

func (s Sockets) validate() error {
	if s.Dir == "" {
		return errNoPath(s.Type())
	}
	return nil
}

func errNoPath(t BusType) error { return fmt.Errorf("type %q requires a non-empty path", t) }

func (SessionBus) path() string { return "" }
func (s Socket) path() string   { return string(s.Path) }
func (s Sockets) path() string  { return string(s.Dir) }

func (SessionBus) Connect() (*dbus.Conn, error) { return dbus.ConnectSessionBus() }
func (s Socket) Connect() (*dbus.Conn, error)   { return s.Path.ConnectDBus() }

func (SessionBus) sessionBusDownstream() error {
	return errors.New("upstream and downstream cannot both be session_bus (same bus)")
}
func (Socket) sessionBusDownstream() error { return nil }

// Checks each downstream against the upstream and the downstreams before it.
type downstreamCheck struct {
	// Why the next session_bus downstream is rejected, or nil.
	sessionBus error
	clients    map[approval.ClientName]bool
}

func newDownstreamCheck(upstream Upstream) *downstreamCheck {
	return &downstreamCheck{sessionBus: upstream.sessionBusDownstream(), clients: map[approval.ClientName]bool{}}
}

func (c *downstreamCheck) SessionBus() error {
	if c.sessionBus != nil {
		return c.sessionBus
	}
	c.sessionBus = errors.New("at most one session_bus downstream is allowed")
	return nil
}

func (c *downstreamCheck) Socket(s Socket) error {
	client, err := s.Client()
	if err != nil {
		return err
	}
	if c.clients[client] {
		return fmt.Errorf("another socket downstream already has the client name %q", client)
	}
	c.clients[client] = true
	return nil
}

func (c *downstreamCheck) Sockets(Sockets) error { return nil }

// Keeps the variants an upstream can be. Errors on Sockets.
type upstreamOf struct{ up Upstream }

func (u *upstreamOf) SessionBus() error     { u.up = SessionBus{}; return nil }
func (u *upstreamOf) Socket(s Socket) error { u.up = s; return nil }

func (u *upstreamOf) Sockets(Sockets) error { return upstreamTypes.Unknown(BusSockets) }

// A Bus as the config file holds it: a type key, and a path key for the socket
// or the directory. A nil Bus is a type left unset.
type BusConfig struct{ Bus Bus }

// An Upstream as the config file holds it, in BusConfig's form.
type UpstreamConfig struct{ Bus Upstream }

type busConfigYAML struct {
	Type BusType `yaml:"type"`
	Path string  `yaml:"path,omitempty"`
}

// Returns the variant that raw's type names, or nil for an unset type.
func (raw busConfigYAML) bus() Bus {
	switch raw.Type {
	case BusSessionBus:
		return SessionBus{}
	case BusSocket:
		return Socket{Path: unixsock.Path(raw.Path)}
	case BusSockets:
		return Sockets{Dir: unixsock.Dir(raw.Path)}
	}
	return nil
}

func marshalBus(b Bus) busConfigYAML {
	if b == nil {
		return busConfigYAML{}
	}
	return busConfigYAML{Type: b.Type(), Path: b.path()}
}

// Decodes the YAML form into the variant its type names. Errors when the type
// is unknown.
func (b *BusConfig) UnmarshalYAML(value *yaml.Node) error {
	var raw busConfigYAML
	if err := value.Decode(&raw); err != nil {
		return err
	}
	b.Bus = raw.bus()
	return nil
}

func (b BusConfig) MarshalYAML() (any, error) { return marshalBus(b.Bus), nil }

// Decodes as BusConfig does. Errors also when the type is sockets.
func (u *UpstreamConfig) UnmarshalYAML(value *yaml.Node) error {
	var b BusConfig
	if err := value.Decode(&b); err != nil {
		return err
	}
	u.Bus = nil
	if b.Bus == nil {
		return nil
	}
	var up upstreamOf
	if err := b.Bus.Visit(&up); err != nil {
		return err
	}
	u.Bus = up.up
	return nil
}

func (u UpstreamConfig) MarshalYAML() (any, error) { return marshalBus(u.Bus), nil }

// The type key of a Bus in YAML.
type BusType string

const (
	BusSessionBus BusType = "session_bus"
	BusSocket     BusType = "socket"
	BusSockets    BusType = "sockets"
)

// The valid bus types.
var BusTypes = NewEnum("bus type", BusSessionBus, BusSocket, BusSockets)

// The bus types an upstream may have.
var upstreamTypes = NewEnum("upstream type", BusSessionBus, BusSocket)

// Parses a bus type as Enum.Parse does, so that a config file naming an
// unknown type fails to load.
func (t *BusType) UnmarshalText(text []byte) (err error) {
	*t, err = BusTypes.Parse(text)
	return err
}
