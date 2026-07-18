package proxy

import (
	"log/slog"
	"os"

	"github.com/godbus/dbus/v5"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/procutil"
)

// dbusClient abstracts D-Bus operations for testing.
type dbusClient interface {
	GetConnectionUnixProcessID(sender senderName) (uint32, error)
	GetConnectionUnixUser(sender senderName) (uint32, error)
	GetUnitByPID(pid uint32) (string, error)
}

// SenderInfoResolver resolves D-Bus sender information (PID, UID, systemd unit).
type SenderInfoResolver struct {
	client           dbusClient
	trimProcessChain bool
	selfExe          string // our own binary path, filtered from process chains
}

// NewSenderInfoResolver creates a new resolver using the given D-Bus connection.
func NewSenderInfoResolver(conn *dbus.Conn, trimProcessChain bool) *SenderInfoResolver {
	selfExe, _ := os.Executable()
	return &SenderInfoResolver{client: &realDBusClient{conn: conn}, trimProcessChain: trimProcessChain, selfExe: selfExe}
}

// newSenderInfoResolverWithClient creates a resolver with a custom client (for testing).
func newSenderInfoResolverWithClient(client dbusClient) *SenderInfoResolver {
	return &SenderInfoResolver{client: client}
}

// Resolve retrieves sender information for a D-Bus sender.
// Returns partial information if some queries fail (never fails completely).
//
// Uses GetConnectionUnixProcessID and GetConnectionUnixUser instead of
// GetConnectionCredentials to avoid ProcessFD which cannot be forwarded
// over SSH tunneled sockets (dbus-broker v34+ includes ProcessFD).
func (r *SenderInfoResolver) Resolve(sender senderName) approval.SenderInfo {
	info := approval.SenderInfo{Sender: string(sender)}

	// Get PID
	pid, err := r.client.GetConnectionUnixProcessID(sender)
	if err != nil {
		slog.Warn("failed to get connection PID", "sender", sender, "error", err)
	} else {
		info.PID = pid
	}

	// Get UID (username resolution not possible - logind is on system bus, not session bus)
	uid, err := r.client.GetConnectionUnixUser(sender)
	if err != nil {
		slog.Warn("failed to get connection UID", "sender", sender, "error", err)
	} else {
		info.UID = uid
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
	// Falls back to the systemd unit as the display name if /proc walking fails.
	if info.PID != 0 {
		chain := procutil.ReadProcessChain(int32(info.PID), r.trimProcessChain)
		if len(chain) > 0 {
			// Populate the full process chain, filtering out our own binary.
			info.ProcessChain = make([]approval.ProcessInfo, 0, len(chain))
			for _, entry := range chain {
				if r.selfExe != "" && entry.Exe == r.selfExe {
					continue
				}
				info.ProcessChain = append(info.ProcessChain, approval.ProcessInfo{
					Name: entry.Comm,
					PID:  uint32(entry.PID),
					Exe:  entry.Exe,
					Args: entry.Args,
					CWD:  entry.CWD,
				})
			}
			// Resolve invoker (skip shells) for the display InvokerName (comm).
			comm, invokerPID := procutil.ResolveInvoker(info.PID)
			info.InvokerName = comm
			info.PID = invokerPID
		} else {
			// No /proc chain (e.g. remote/tunneled caller): use the systemd unit
			// as the display name too.
			info.InvokerName = info.SystemdUnit
		}
	}

	return info
}

// realDBusClient implements dbusClient using a real D-Bus connection.
type realDBusClient struct {
	conn *dbus.Conn
}

func (c *realDBusClient) GetConnectionUnixProcessID(sender senderName) (uint32, error) {
	obj := c.conn.Object("org.freedesktop.DBus", "/org/freedesktop/DBus")
	call := obj.Call("org.freedesktop.DBus.GetConnectionUnixProcessID", 0, string(sender))
	if call.Err != nil {
		return 0, call.Err
	}

	var pid uint32
	if err := call.Store(&pid); err != nil {
		return 0, err
	}

	return pid, nil
}

func (c *realDBusClient) GetConnectionUnixUser(sender senderName) (uint32, error) {
	obj := c.conn.Object("org.freedesktop.DBus", "/org/freedesktop/DBus")
	call := obj.Call("org.freedesktop.DBus.GetConnectionUnixUser", 0, string(sender))
	if call.Err != nil {
		return 0, call.Err
	}

	var uid uint32
	if err := call.Store(&uid); err != nil {
		return 0, err
	}

	return uid, nil
}

func (c *realDBusClient) GetUnitByPID(pid uint32) (string, error) {
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
