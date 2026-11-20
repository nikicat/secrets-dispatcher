// Package unixsock names the unix sockets the daemon listens on or dials.
package unixsock

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/godbus/dbus/v5"
	"golang.org/x/sys/unix"

	"github.com/nikicat/secrets-dispatcher/internal/procutil"
)

// The file path of a unix socket.
type Path string

// Returns the D-Bus address of the socket at p.
func (p Path) Address() string { return "unix:path=" + string(p) }

// Opens a D-Bus connection to the bus that listens at p.
func (p Path) ConnectDBus() (*dbus.Conn, error) { return dbus.Connect(p.Address()) }

// The extension of a socket file in a Dir.
const extension = ".sock"

// Returns the file name of p without its ".sock" extension.
func (p Path) Name() string { return strings.TrimSuffix(filepath.Base(string(p)), extension) }

// Reports whether p has the ".sock" extension of a socket in a Dir.
func (p Path) IsSocketFile() bool { return strings.HasSuffix(string(p), extension) }

// Reports whether a file exists at p.
func (p Path) Exists() bool {
	_, err := os.Stat(string(p))
	return err == nil
}

// Listens on a new socket at p.
func (p Path) Listen() (net.Listener, error) { return net.Listen("unix", string(p)) }

// Listens on a new socket at p that only the owner can connect to, replacing a
// stale one and creating its directory. Closing the listener removes the
// socket file.
func (p Path) ListenPrivate() (net.Listener, error) {
	os.Remove(string(p)) //nolint:errcheck // stale socket from a previous run
	if err := os.MkdirAll(filepath.Dir(string(p)), 0o700); err != nil {
		return nil, err
	}
	listener, err := p.Listen()
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(string(p), 0o600); err != nil {
		listener.Close()
		return nil, err
	}
	return listener, nil
}

// Connects to the socket at p.
func (p Path) Dial(ctx context.Context) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "unix", string(p))
}

// A directory of unix sockets.
type Dir string

// Returns the path of the socket for the given name in d. Path.Name returns
// that name.
func (d Dir) Socket(name string) Path { return Path(filepath.Join(string(d), name+extension)) }

// Returns the path of the given file in d.
func (d Dir) Path(file string) Path { return Path(filepath.Join(string(d), file)) }

// Returns the directory named name in d.
func (d Dir) Sub(name string) Dir { return Dir(filepath.Join(string(d), name)) }

func (d Dir) String() string { return string(d) }

// Creates d and its missing parents with perm.
func (d Dir) MkdirAll(perm os.FileMode) error { return os.MkdirAll(string(d), perm) }

// Returns the entries of d, sorted by name.
func (d Dir) ReadDir() ([]os.DirEntry, error) { return os.ReadDir(string(d)) }

// The process on the other end of a unix socket, as the kernel reports it.
type Peer struct {
	PID procutil.PID
	UID procutil.UID
}

// Returns the peer of conn from SO_PEERCRED. Reports false when conn is not a
// unix socket or the kernel does not say.
func PeerOf(conn net.Conn) (Peer, bool) {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return Peer{}, false
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return Peer{}, false
	}
	var cred *unix.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil || credErr != nil || cred == nil {
		return Peer{}, false
	}
	return Peer{PID: procutil.PID(cred.Pid), UID: procutil.UID(cred.Uid)}, true
}
