package daemon_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	. "github.com/nikicat/secrets-dispatcher/internal/daemon"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// policyConfigTemplate is the dbus-daemon config for integration tests.
// It mirrors the system bus default-deny policy and punches holes for the
// current user (identified by numeric UID) to own and call the dispatcher.
//
// The full default policy block must be present — without receive_type allows
// the daemon's method_return replies to the bus are rejected (Pitfall 1).
//
// Args: the bus address, uid (numeric string)
const policyConfigTemplate = `<?xml version="1.0"?>
<!DOCTYPE busconfig PUBLIC "-//freedesktop//DTD D-BUS Bus Configuration 1.0//EN"
 "http://www.freedesktop.org/standards/dbus/1.0/busconfig.dtd">
<busconfig>
  <type>session</type>
  <listen>%s</listen>
  <policy context="default">
    <allow user="*"/>
    <deny own="*"/>
    <deny send_type="method_call"/>
    <allow send_type="signal"/>
    <allow send_requested_reply="true" send_type="method_return"/>
    <allow send_requested_reply="true" send_type="error"/>
    <allow receive_type="method_call"/>
    <allow receive_type="method_return"/>
    <allow receive_type="error"/>
    <allow receive_type="signal"/>
    <allow send_destination="org.freedesktop.DBus"/>
  </policy>
  <policy user="%s">
    <allow own="net.mowaka.SecretsDispatcher1"/>
    <allow send_destination="net.mowaka.SecretsDispatcher1"/>
  </policy>
</busconfig>`

// Starts a private bus whose policy lets the current user own and call
// net.mowaka.SecretsDispatcher1.
func startDBusDaemonWithPolicy(t *testing.T) string {
	t.Helper()
	uid := fmt.Sprintf("%d", os.Getuid())
	return testutil.StartDBusDaemonWithConfig(t, func(addr string) string {
		return fmt.Sprintf(policyConfigTemplate, addr, uid)
	})
}

// Waits until the given name has an owner on the bus at the given address, and
// fails the test after 5s.
func waitForName(t *testing.T, addr, name string) {
	t.Helper()
	require.Eventually(t, func() bool {
		conn, err := dbus.Connect(addr)
		if err != nil {
			return false
		}
		defer conn.Close()
		var owners []string
		if err := conn.BusObject().Call(dbustypes.BusDaemonInterface.Member("ListNames"), 0).Store(&owners); err != nil {
			return false
		}
		return slices.Contains(owners, name)
	}, testutil.BusWait, 100*time.Millisecond, "bus name %q not registered in time", name)
}

// TestDaemon_RegistersAndServesStubs is the main integration test:
// starts a daemon against a private bus and verifies Ping + GetVersion work.
func TestDaemon_RegistersAndServesStubs(t *testing.T) {
	addr := startDBusDaemonWithPolicy(t)

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, Config{
			Bus:     BusAt(addr),
			Version: "1.2.3-test",
		})
	}()

	// Wait until the daemon has registered its bus name.
	waitForName(t, addr, BusName)

	// Connect a second "client" connection to the same private bus.
	client, err := dbus.Connect(addr)
	require.NoError(t, err, "connect client")
	defer client.Close()

	obj := client.Object(BusName, ObjectPath)

	// Verify Ping.
	var pong string
	require.NoError(t, obj.Call(Interface.Member("Ping"), 0).Store(&pong), "Ping")
	assert.Equal(t, "pong", pong)

	// Verify GetVersion.
	var version string
	require.NoError(t, obj.Call(Interface.Member("GetVersion"), 0).Store(&version), "GetVersion")
	assert.Equal(t, "1.2.3-test", version)

	// Shut down the daemon cleanly.
	cancel()

	assert.NoError(t, testutil.Recv(t, errCh, testutil.BusWait, "daemon did not stop after context cancel"), "Run() returned error")
}

// TestDaemon_NameAlreadyTaken verifies Run() returns an error when the bus
// name is already owned by another connection.
func TestDaemon_NameAlreadyTaken(t *testing.T) {
	addr := startDBusDaemonWithPolicy(t)

	// Claim the bus name first, simulating another instance already running.
	owner, err := dbus.Connect(addr)
	require.NoError(t, err, "connect owner")
	defer owner.Close()

	reply, err := owner.RequestName(BusName, dbus.NameFlagDoNotQueue)
	require.NoError(t, err, "pre-claim RequestName")
	require.Equal(t, dbus.RequestNameReplyPrimaryOwner, reply, "expected to become primary owner")

	// Now try to start a daemon — it should fail because the name is taken.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = Run(ctx, Config{Bus: BusAt(addr), Version: "test"})
	require.Error(t, err, "Run() succeeded but expected an error for name-already-taken")
}

// TestDaemon_Introspectable verifies that the introspection XML mentions Ping
// and GetVersion, confirming org.freedesktop.DBus.Introspectable is exported.
func TestDaemon_Introspectable(t *testing.T) {
	addr := startDBusDaemonWithPolicy(t)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, Config{Bus: BusAt(addr), Version: "test"})
	}()

	waitForName(t, addr, BusName)

	client, err := dbus.Connect(addr)
	require.NoError(t, err, "connect client")
	defer client.Close()

	obj := client.Object(BusName, ObjectPath)

	var xml string
	require.NoError(t, obj.Call(dbustypes.IntrospectableInterface.Member("Introspect"), 0).Store(&xml), "Introspect")

	assert.Contains(t, xml, "Ping")
	assert.Contains(t, xml, "GetVersion")
}

// TestSdNotify_NoSocket verifies SdNotify is a silent no-op when NOTIFY_SOCKET is unset.
func TestSdNotify_NoSocket(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "")
	// Must not panic or error.
	SdNotify("READY=1")
}

// TestSdNotify_WithSocket verifies SdNotify sends the state string to the socket.
func TestSdNotify_WithSocket(t *testing.T) {
	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "notify.sock")

	// Create a Unix datagram listener.
	ln, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Net: "unixgram", Name: sockPath})
	require.NoError(t, err, "listen unixgram")
	t.Cleanup(func() { ln.Close() })

	t.Setenv("NOTIFY_SOCKET", sockPath)
	SdNotify("READY=1")

	// Read what was sent.
	buf := make([]byte, 128)
	ln.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
	n, err := ln.Read(buf)
	require.NoError(t, err, "read from socket")
	assert.Equal(t, "READY=1", string(buf[:n]))
}
