package daemon_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	. "github.com/nikicat/secrets-dispatcher/internal/daemon"
)

// policyConfigTemplate is the dbus-daemon config for integration tests.
// It mirrors the system bus default-deny policy and punches holes for the
// current user (identified by numeric UID) to own and call the dispatcher.
//
// The full default policy block must be present — without receive_type allows
// the daemon's method_return replies to the bus are rejected (Pitfall 1).
//
// Args: sockPath, uid (numeric string)
const policyConfigTemplate = `<?xml version="1.0"?>
<!DOCTYPE busconfig PUBLIC "-//freedesktop//DTD D-BUS Bus Configuration 1.0//EN"
 "http://www.freedesktop.org/standards/dbus/1.0/busconfig.dtd">
<busconfig>
  <type>session</type>
  <listen>unix:path=%s</listen>
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

// startDBusDaemonWithPolicy starts a private dbus-daemon with a policy config
// that allows the current user to own and call net.mowaka.SecretsDispatcher1.
// Uses filesystem sockets (NOT abstract) to avoid cross-test collisions.
func startDBusDaemonWithPolicy(t *testing.T) string {
	t.Helper()

	tmpDir := t.TempDir()
	sockPath := filepath.Join(tmpDir, "test.sock")
	confPath := filepath.Join(tmpDir, "policy.conf")

	uid := fmt.Sprintf("%d", os.Getuid())
	conf := fmt.Sprintf(policyConfigTemplate, sockPath, uid)

	if err := os.WriteFile(confPath, []byte(conf), 0600); err != nil {
		t.Fatalf("write policy config: %v", err)
	}

	cmd := exec.Command("dbus-daemon", "--config-file="+confPath, "--nofork")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("start dbus-daemon: %v", err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill() //nolint:errcheck
		cmd.Wait()         //nolint:errcheck
	})

	// Wait for socket file to appear (50 * 100ms = 5s max).
	for range 50 {
		if _, err := os.Stat(sockPath); err == nil {
			return "unix:path=" + sockPath
		}
		time.Sleep(100 * time.Millisecond)
	}

	t.Fatal("dbus-daemon socket not created in time")
	return ""
}

// waitForName polls until the bus name is registered or timeout.
func waitForName(t *testing.T, addr, name string) {
	t.Helper()
	for range 50 {
		conn, err := dbus.Connect(addr)
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		obj := conn.BusObject()
		var owners []string
		if err := obj.Call("org.freedesktop.DBus.ListNames", 0).Store(&owners); err != nil {
			conn.Close()
			time.Sleep(100 * time.Millisecond)
			continue
		}
		conn.Close()
		for _, n := range owners {
			if n == name {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("bus name %q not registered in time", name)
}

// TestDaemon_RegistersAndServesStubs is the main integration test:
// starts a daemon against a private bus and verifies Ping + GetVersion work.
func TestDaemon_RegistersAndServesStubs(t *testing.T) {
	addr := startDBusDaemonWithPolicy(t)

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, Config{
			BusAddress: addr,
			Version:    "1.2.3-test",
		})
	}()

	// Wait until the daemon has registered its bus name.
	waitForName(t, addr, BusName)

	// Connect a second "client" connection to the same private bus.
	client, err := dbus.Connect(addr)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	defer client.Close()

	obj := client.Object(BusName, ObjectPath)

	// Verify Ping.
	var pong string
	if err := obj.Call(Interface+".Ping", 0).Store(&pong); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if pong != "pong" {
		t.Errorf("Ping returned %q, want %q", pong, "pong")
	}

	// Verify GetVersion.
	var version string
	if err := obj.Call(Interface+".GetVersion", 0).Store(&version); err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if version != "1.2.3-test" {
		t.Errorf("GetVersion returned %q, want %q", version, "1.2.3-test")
	}

	// Shut down the daemon cleanly.
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("Run() returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("daemon did not stop within 5s after context cancel")
	}
}

// TestDaemon_NameAlreadyTaken verifies Run() returns an error when the bus
// name is already owned by another connection.
func TestDaemon_NameAlreadyTaken(t *testing.T) {
	addr := startDBusDaemonWithPolicy(t)

	// Claim the bus name first, simulating another instance already running.
	owner, err := dbus.Connect(addr)
	if err != nil {
		t.Fatalf("connect owner: %v", err)
	}
	defer owner.Close()

	reply, err := owner.RequestName(BusName, dbus.NameFlagDoNotQueue)
	if err != nil {
		t.Fatalf("pre-claim RequestName: %v", err)
	}
	if reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("expected to become primary owner, got reply=%d", reply)
	}

	// Now try to start a daemon — it should fail because the name is taken.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = Run(ctx, Config{BusAddress: addr, Version: "test"})
	if err == nil {
		t.Fatal("Run() succeeded but expected an error for name-already-taken")
	}
}

// TestDaemon_Introspectable verifies that the introspection XML mentions Ping
// and GetVersion, confirming org.freedesktop.DBus.Introspectable is exported.
func TestDaemon_Introspectable(t *testing.T) {
	addr := startDBusDaemonWithPolicy(t)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, Config{BusAddress: addr, Version: "test"})
	}()

	waitForName(t, addr, BusName)

	client, err := dbus.Connect(addr)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	defer client.Close()

	obj := client.Object(BusName, ObjectPath)

	var xml string
	if err := obj.Call("org.freedesktop.DBus.Introspectable.Introspect", 0).Store(&xml); err != nil {
		t.Fatalf("Introspect: %v", err)
	}

	if !strings.Contains(xml, "Ping") {
		t.Errorf("introspection XML does not mention Ping; got:\n%s", xml)
	}
	if !strings.Contains(xml, "GetVersion") {
		t.Errorf("introspection XML does not mention GetVersion; got:\n%s", xml)
	}
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
	if err != nil {
		t.Fatalf("listen unixgram: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	t.Setenv("NOTIFY_SOCKET", sockPath)
	SdNotify("READY=1")

	// Read what was sent.
	buf := make([]byte, 128)
	ln.SetReadDeadline(time.Now().Add(2 * time.Second)) //nolint:errcheck
	n, err := ln.Read(buf)
	if err != nil {
		t.Fatalf("read from socket: %v", err)
	}
	got := string(buf[:n])
	if got != "READY=1" {
		t.Errorf("SdNotify sent %q, want %q", got, "READY=1")
	}
}
