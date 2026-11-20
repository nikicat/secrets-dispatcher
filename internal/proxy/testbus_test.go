package proxy

import (
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/sender"
)

func uniqueName(conn *dbus.Conn) sender.UniqueName {
	return sender.UniqueName(conn.Names()[0])
}

func startTestProxy(t *testing.T, frontAddr, backendAddr string) *Proxy {
	t.Helper()
	backendConn, err := dbus.Connect(backendAddr)
	require.NoError(t, err, "connect proxy to backend bus")
	frontConn, err := dbus.Connect(frontAddr)
	require.NoError(t, err, "connect proxy to front bus")
	p := New(approval.ClientName(t.Name()), approval.NewDisabledManager(), Config{})
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
