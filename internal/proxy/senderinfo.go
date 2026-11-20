package proxy

import (
	"log/slog"

	"github.com/godbus/dbus/v5"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/procutil"
	"github.com/nikicat/secrets-dispatcher/internal/sender"
	"github.com/nikicat/secrets-dispatcher/internal/systemd"
)

// Returns the unique D-Bus name of the sender of an incoming message.
func senderOf(msg dbus.Message) sender.UniqueName {
	return sender.UniqueName(msg.Headers[dbus.FieldSender].Value().(string))
}

// pathOf returns the object path an incoming message is addressed to.
func pathOf(msg dbus.Message) dbus.ObjectPath {
	return msg.Headers[dbus.FieldPath].Value().(dbus.ObjectPath)
}

// dbusClient abstracts D-Bus operations for testing.
type dbusClient interface {
	GetConnectionUnixProcessID(sender sender.UniqueName) (procutil.PID, error)
	GetConnectionUnixUser(sender sender.UniqueName) (procutil.UID, error)
	GetUnitByPID(pid procutil.PID) (systemd.Unit, error)
}

// SenderInfoResolver resolves D-Bus sender information (PID, UID, systemd unit).
type SenderInfoResolver struct {
	client     dbusClient
	clientName approval.ClientName // set as every resolved SenderInfo's Client
	// Nil when the bus is forwarded from another machine or container. Its PIDs
	// belong to another PID namespace, so /proc and systemd here would describe
	// an unrelated process.
	local *sender.ChainReader
}

// Returns a resolver that asks conn about senders and names them clientName.
func newSenderInfoResolver(conn *dbus.Conn, clientName approval.ClientName, local *sender.ChainReader) *SenderInfoResolver {
	return &SenderInfoResolver{client: &realDBusClient{conn: conn}, clientName: clientName, local: local}
}

// newSenderInfoResolverWithClient creates a resolver with a custom client (for testing).
func newSenderInfoResolverWithClient(client dbusClient) *SenderInfoResolver {
	return &SenderInfoResolver{client: client, local: &sender.ChainReader{}}
}

// Resolve retrieves sender information for a D-Bus sender.
// Returns partial information if some queries fail (never fails completely).
//
// Uses GetConnectionUnixProcessID and GetConnectionUnixUser instead of
// GetConnectionCredentials to avoid ProcessFD which cannot be forwarded
// over SSH tunneled sockets (dbus-broker v34+ includes ProcessFD).
func (r *SenderInfoResolver) Resolve(name sender.UniqueName) approval.SenderInfo {
	info := approval.SenderInfo{Client: r.clientName, Sender: &name}

	// Get PID
	pid, err := r.client.GetConnectionUnixProcessID(name)
	if err != nil {
		slog.Warn("failed to get connection PID", "sender", name, "error", err)
	} else {
		info.PID = pid
	}

	// Get UID (username resolution not possible - logind is on system bus, not session bus)
	uid, err := r.client.GetConnectionUnixUser(name)
	if err != nil {
		slog.Warn("failed to get connection UID", "sender", name, "error", err)
	} else {
		info.UID = &uid
	}

	// A forwarded caller keeps only the PID and UID its bus reports.
	if r.local == nil {
		return info
	}

	// Resolve the caller's real systemd unit (authoritative and non-spoofable for
	// systemd-managed services). Stored separately from InvokerName (the spoofable
	// comm) and consulted by the `unit` process matcher.
	if info.PID != 0 {
		if unit, err := r.client.GetUnitByPID(pid); err != nil {
			slog.Debug("failed to get unit by PID", "pid", pid, "error", err)
		} else {
			info.SystemdUnit = unit
		}
	}

	// Resolve the user-facing invoker process via /proc.
	if info.PID != 0 {
		if chain := r.local.Chain(info.PID); len(chain) > 0 {
			info.ProcessChain = chain
			if invoker, ok := info.PID.Invoker(); ok {
				info.InvokerName, info.PID = invoker.Name, invoker.PID
			}
		}
	}

	return info
}

// realDBusClient implements dbusClient using a real D-Bus connection.
type realDBusClient struct {
	conn *dbus.Conn
}

func (c *realDBusClient) GetConnectionUnixProcessID(sender sender.UniqueName) (procutil.PID, error) {
	obj := c.conn.Object("org.freedesktop.DBus", "/org/freedesktop/DBus")
	call := obj.Call("org.freedesktop.DBus.GetConnectionUnixProcessID", 0, string(sender))
	if call.Err != nil {
		return 0, call.Err
	}

	var pid procutil.PID
	if err := call.Store(&pid); err != nil {
		return 0, err
	}

	return pid, nil
}

func (c *realDBusClient) GetConnectionUnixUser(sender sender.UniqueName) (procutil.UID, error) {
	obj := c.conn.Object("org.freedesktop.DBus", "/org/freedesktop/DBus")
	call := obj.Call("org.freedesktop.DBus.GetConnectionUnixUser", 0, string(sender))
	if call.Err != nil {
		return 0, call.Err
	}

	var uid procutil.UID
	if err := call.Store(&uid); err != nil {
		return 0, err
	}

	return uid, nil
}

func (c *realDBusClient) GetUnitByPID(pid procutil.PID) (systemd.Unit, error) {
	obj := c.conn.Object("org.freedesktop.systemd1", "/org/freedesktop/systemd1")
	call := obj.Call("org.freedesktop.systemd1.Manager.GetUnitByPID", 0, pid)
	if call.Err != nil {
		return "", call.Err
	}

	var unitPath dbus.ObjectPath
	if err := call.Store(&unitPath); err != nil {
		return "", err
	}

	// Decode the unit path to get the unit name
	unitName := DecodeUnitPath(string(unitPath))
	slog.Debug("decoded unit path", "path", string(unitPath), "unit", unitName)
	return unitName, nil
}
