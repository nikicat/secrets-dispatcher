package proxy

import (
	"log/slog"
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/stretchr/testify/require"
)

func uniqueName(conn *dbus.Conn) senderName {
	return senderName(conn.Names()[0])
}

func startTestProxy(t *testing.T, frontAddr, backendAddr string) *Proxy {
	t.Helper()
	backendConn, err := dbus.Connect(backendAddr)
	require.NoError(t, err, "connect proxy to backend bus")
	frontConn, err := dbus.Connect(frontAddr)
	require.NoError(t, err, "connect proxy to front bus")
	p := New(Config{ClientName: t.Name(), LogLevel: slog.LevelDebug})
	require.NoError(t, p.ConnectWith(frontConn, backendConn), "connect proxy")
	t.Cleanup(func() { p.Close() })
	return p
}

func claimTestName(t *testing.T, conn *dbus.Conn, name string) {
	t.Helper()
	reply, err := conn.RequestName(name, dbus.NameFlagDoNotQueue)
	require.NoError(t, err)
	require.Equal(t, dbus.RequestNameReplyPrimaryOwner, reply)
}
