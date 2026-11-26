package proxy

import (
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/sender"
	"github.com/nikicat/secrets-dispatcher/internal/testutil"
)

func TestProxyTakesBusNameWhenHolderLeaves(t *testing.T) {
	frontAddr, backendAddr := testutil.StartDBusDaemon(t), testutil.StartDBusDaemon(t)

	holder, err := dbus.Connect(frontAddr)
	require.NoError(t, err)
	claimTestName(t, holder, dbustypes.BusName)

	p := startTestProxy(t, frontAddr, backendAddr)

	observer, err := dbus.Connect(frontAddr)
	require.NoError(t, err)
	t.Cleanup(func() { observer.Close() })
	got, err := busDaemon{observer}.nameOwner(dbustypes.BusName)
	require.NoError(t, err)
	require.Equal(t, uniqueName(holder), got)

	holder.Close()
	require.Eventually(t, func() bool {
		got, err := busDaemon{observer}.nameOwner(dbustypes.BusName)
		return err == nil && got == uniqueName(p.conn.frontConn)
	}, 5*time.Second, 50*time.Millisecond)
}

func TestProxyFailsWhenSessionBusNameIsHeld(t *testing.T) {
	frontAddr, backendAddr := testutil.StartDBusDaemon(t), testutil.StartDBusDaemon(t)

	holder, err := dbus.Connect(frontAddr)
	require.NoError(t, err)
	t.Cleanup(func() { holder.Close() })
	claimTestName(t, holder, dbustypes.BusName)

	backendConn, err := dbus.Connect(backendAddr)
	require.NoError(t, err)
	frontConn, err := dbus.Connect(frontAddr)
	require.NoError(t, err)
	p := New(approval.ClientClaim{Name: approval.ClientName(t.Name())}, approval.NewDisabledManager(), Config{Local: &sender.ChainReader{}})
	require.ErrorIs(t, p.ConnectWith(frontConn, backendConn), errBusNameTaken)
}

// A backend whose sessions' Close holds shutdown until the test lets it go.
type stalledSessions struct{ closing, release chan struct{} }

func (s stalledSessions) Close(dbus.Message) *dbus.Error {
	close(s.closing)
	<-s.release
	return nil
}

func TestProxyReleasesBusNameBeforeClosingBackend(t *testing.T) {
	stalled := stalledSessions{make(chan struct{}), make(chan struct{})}
	env := newMockServiceEnvWith(t, func(mockConn *dbus.Conn) {
		require.NoError(t, mockConn.ExportSubtree(stalled, dbustypes.SessionsPath, string(dbustypes.SessionInterface)))
	})
	openPlainSession(t, env.client)

	successor, err := dbus.Connect(env.frontAddr)
	require.NoError(t, err)
	t.Cleanup(func() { successor.Close() })
	reply, err := successor.RequestName(dbustypes.BusName, 0)
	require.NoError(t, err)
	require.Equal(t, dbus.RequestNameReplyInQueue, reply)

	// Runs before the proxy's own cleanup, so a failed wait does not leave its Close stuck.
	release := sync.OnceFunc(func() { close(stalled.release) })
	t.Cleanup(release)
	closed := make(chan struct{})
	go func() { env.proxy.Close(); close(closed) }()
	testutil.Recv(t, stalled.closing, testutil.BusWait, "backend Session.Close not reached")
	got, err := busDaemon{successor}.nameOwner(dbustypes.BusName)
	release()
	testutil.Recv(t, closed, testutil.BusWait, "proxy Close did not return")
	require.NoError(t, err)
	require.Equal(t, uniqueName(successor), got, "the name has moved on while the backend is still closing")
}
