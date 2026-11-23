package proxy

import (
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/sender"
	"github.com/nikicat/secrets-dispatcher/internal/testutil"
)

func uniqueName(conn *dbus.Conn) sender.UniqueName {
	return sender.UniqueName(conn.Names()[0])
}

// Returns a proxy that serves a forwarded bus.
func startTestProxy(t *testing.T, frontAddr, backendAddr string) *Proxy {
	t.Helper()
	return startTestProxyWith(t, frontAddr, backendAddr, Config{})
}

// Returns a proxy that serves the session bus.
func startSessionBusTestProxy(t *testing.T, frontAddr, backendAddr string) *Proxy {
	t.Helper()
	return startTestProxyWith(t, frontAddr, backendAddr, Config{Local: &sender.ChainReader{}})
}

func startTestProxyWith(t *testing.T, frontAddr, backendAddr string, cfg Config) *Proxy {
	t.Helper()
	backendConn, err := dbus.Connect(backendAddr)
	require.NoError(t, err, "connect proxy to backend bus")
	frontConn, err := dbus.Connect(frontAddr)
	require.NoError(t, err, "connect proxy to front bus")
	p := New(approval.ClientName(t.Name()), approval.NewDisabledManager(), cfg)
	require.NoError(t, p.ConnectWith(frontConn, backendConn), "connect proxy")
	t.Cleanup(func() { p.Close() })
	return p
}

// A mock Secret Service on a private backend bus and the proxy in front of it
// on a private front bus, as in production: client, front bus, proxy, backend
// bus, real service.
type mockServiceEnv struct {
	client *dbus.Conn
	mock   *testutil.MockSecretService
	proxy  *Proxy
}

func newMockServiceEnv(t *testing.T) *mockServiceEnv {
	t.Helper()
	frontAddr, backendAddr := testutil.StartDBusDaemon(t), testutil.StartDBusDaemon(t)

	mockConn, err := dbus.Connect(backendAddr)
	require.NoError(t, err, "connect mock to backend bus")
	t.Cleanup(func() { mockConn.Close() })

	mock := testutil.NewMockSecretService()
	require.NoError(t, mock.Register(mockConn), "register mock service")

	proxy := startTestProxy(t, frontAddr, backendAddr)

	client, err := dbus.Connect(frontAddr)
	require.NoError(t, err, "connect client to front bus")
	t.Cleanup(func() { client.Close() })

	return &mockServiceEnv{client: client, mock: mock, proxy: proxy}
}

func claimTestName(t *testing.T, conn *dbus.Conn, name string) {
	t.Helper()
	reply, err := conn.RequestName(name, dbus.NameFlagDoNotQueue)
	require.NoError(t, err)
	require.Equal(t, dbus.RequestNameReplyPrimaryOwner, reply)
}
