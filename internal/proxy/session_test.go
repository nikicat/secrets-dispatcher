package proxy

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/testutil"
)

// A session that closes while its call waits on the backend must not get its
// secret back in plain text.
func TestSessionManagerRefusesUnknownSession(t *testing.T) {
	m := NewSessionManager(nil)
	closed := dbustypes.SessionPath(1)

	_, err := m.ForClient(closed, dbustypes.Secret{Value: []byte("hunter2")})
	require.ErrorIs(t, err, errUnknownSession)

	_, err = m.ForUpstream(dbustypes.Secret{Session: closed, Value: []byte("hunter2")})
	assert.ErrorIs(t, err, errUnknownSession)
}

// A backend session whose Close answers only once the test has ended.
type stalledSession struct{ release chan struct{} }

func (s stalledSession) Close() *dbus.Error {
	<-s.release
	return nil
}

// A stalled backend must not hang closing the sessions: the calls give up when
// the close deadline passes.
func TestSessionManagerCloseAllGivesUpOnStalledBackend(t *testing.T) {
	backendAddr := testutil.StartDBusDaemon(t)
	backend, err := dbus.Connect(backendAddr)
	require.NoError(t, err)
	t.Cleanup(func() { backend.Close() })
	stalled := stalledSession{release: make(chan struct{})}
	t.Cleanup(func() { close(stalled.release) })
	const upstream = dbus.ObjectPath("/org/freedesktop/secrets/session/stalled")
	require.NoError(t, backend.Export(stalled, upstream, string(dbustypes.SessionInterface)))
	claimTestName(t, backend, dbustypes.BusName)

	proxyConn, err := dbus.Connect(backendAddr)
	require.NoError(t, err)
	t.Cleanup(func() { proxyConn.Close() })

	synctest.Test(t, func(t *testing.T) {
		m := NewSessionManager(proxyConn)
		m.sessions[dbustypes.SessionPath(1)] = sessionEntry{upstream: upstream, codec: plainSession{}}
		ctx, cancel := context.WithTimeout(t.Context(), closeTimeout)
		defer cancel()

		start := time.Now()
		m.CloseAll(ctx)
		assert.Equal(t, closeTimeout, time.Since(start))
	})
}

// A client's Session.Close closes the upstream session behind it, rather than
// leaving it open until the proxy disconnects.
func TestSessionCloseClosesUpstream(t *testing.T) {
	env := newMockServiceEnv(t)

	var output dbus.Variant
	var clientPath dbus.ObjectPath
	require.NoError(t, env.client.Object(dbustypes.BusName, dbustypes.ServicePath).
		Call(dbustypes.ServiceInterface.Member("OpenSession"), 0, dbustypes.AlgorithmPlain, dbus.MakeVariant("")).
		Store(&output, &clientPath))
	upstream, ok := env.proxy.conn.h.sessions.UpstreamSession(clientPath)
	require.True(t, ok)
	require.True(t, env.mock.HasSession(upstream))

	session := env.client.Object(dbustypes.BusName, clientPath)
	require.NoError(t, session.Call(dbustypes.SessionInterface.Member("Close"), 0).Err)
	assert.False(t, env.mock.HasSession(upstream), "upstream session still open")

	err := session.Call(dbustypes.SessionInterface.Member("Close"), 0).Err
	var dbusErr dbus.Error
	require.ErrorAs(t, err, &dbusErr)
	assert.Equal(t, string(dbustypes.NoSession), dbusErr.Name)
}
