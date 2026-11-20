package proxy

import (
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
	got, err := nameOwner(observer, dbustypes.BusName)
	require.NoError(t, err)
	require.Equal(t, uniqueName(holder), got)

	holder.Close()
	require.Eventually(t, func() bool {
		got, err := nameOwner(observer, dbustypes.BusName)
		return err == nil && got == uniqueName(p.frontConn)
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
	p := New(approval.ClientName(t.Name()), approval.NewDisabledManager(), Config{Local: &sender.ChainReader{}})
	require.ErrorIs(t, p.ConnectWith(frontConn, backendConn), errBusNameTaken)
}
