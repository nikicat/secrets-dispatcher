package unixsock

import (
	"net"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPathAddress(t *testing.T) {
	assert.Equal(t, "unix:path=/run/user/1000/bus.sock", Path("/run/user/1000/bus.sock").Address())
}

func TestDirSocket(t *testing.T) {
	socket := Dir("/run/sockets").Socket("vm")
	assert.Equal(t, Path("/run/sockets/vm.sock"), socket)
	assert.True(t, socket.IsSocketFile())
	assert.Equal(t, "vm", socket.Name(), "Name reads back the name that Socket was given")
}

func TestPathName(t *testing.T) {
	tests := []struct {
		path   Path
		name   string
		socket bool
	}{
		{"myserver.sock", "myserver", true},
		{"/path/to/server.sock", "server", true},
		{"notasocket.txt", "notasocket.txt", false},
		{"socket", "socket", false},
		{"socket.sock.bak", "socket.sock.bak", false},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.name, tc.path.Name(), tc.path)
		assert.Equal(t, tc.socket, tc.path.IsSocketFile(), tc.path)
	}
}

func TestListenPrivate(t *testing.T) {
	socket := Dir(t.TempDir()).Socket("sub/api")

	stale, err := socket.ListenPrivate()
	require.NoError(t, err)
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	require.NoError(t, stale.Close())
	require.True(t, socket.Exists(), "the stale socket file stays")

	listener, err := socket.ListenPrivate()
	require.NoError(t, err)
	info, err := os.Stat(string(socket))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())

	conn, err := socket.Dial(t.Context())
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	require.NoError(t, listener.Close())
	assert.False(t, socket.Exists(), "closing the listener removes the socket file")
}
