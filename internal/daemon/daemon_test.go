package daemon_test

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/godbus/dbus/v5"
	. "github.com/nikicat/secrets-dispatcher/internal/daemon"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

// noopSender is a MessageSender that discards all messages.
// Used by integration tests as the program seam so RequestSecret/RequestSign
// don't return NotReady without needing a real bubbletea program.
type noopSender struct {
	mu   sync.Mutex
	msgs []tea.Msg
}

func (s *noopSender) Send(msg tea.Msg) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msgs = append(s.msgs, msg)
}

// startDaemonForTest starts a headless daemon on a private bus with a given
// approval.Manager and returns the bus address and a cleanup cancel function.
// The test can call mgr.Approve/Deny to control request resolution.
// The noopSender is wired as MessageSender so RequestSecret/RequestSign don't
// return NotReady.
func startDaemonForTest(t *testing.T, cfg Config) (addr string) {
	t.Helper()
	addr = startDBusDaemonWithPolicy(t)
	cfg.BusAddress = addr
	if cfg.MessageSender == nil {
		cfg.MessageSender = &noopSender{}
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- Run(ctx, cfg)
	}()

	t.Cleanup(func() {
		cancel()
		select {
		case <-errCh:
		case <-time.After(5 * time.Second):
			t.Error("daemon did not stop within 5s after context cancel")
		}
	})

	waitForName(t, addr, BusName)
	return addr
}

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

// ---------------------------------------------------------------------------
// Integration tests for RequestSecret and RequestSign over real D-Bus wire.
// ---------------------------------------------------------------------------

// pollPendingFromManager polls mgr.List() until at least one request appears.
func pollPendingFromManager(t *testing.T, mgr *approval.Manager) *approval.Request {
	t.Helper()
	for i := 0; i < 200; i++ {
		reqs := mgr.List()
		if len(reqs) > 0 {
			return reqs[0]
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no pending request appeared within 1s")
	return nil
}

// TestDaemon_RequestSecret_Approve verifies that a D-Bus caller receives
// "approved:<path>" when the approval manager approves the request.
func TestDaemon_RequestSecret_Approve(t *testing.T) {
	mgr := approval.NewManager(5*time.Second, 100)
	addr := startDaemonForTest(t, Config{
		Version:         "test",
		Timeout:         5 * time.Second,
		ApprovalManager: mgr,
	})

	client, err := dbus.Connect(addr)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	defer client.Close()

	obj := client.Object(BusName, ObjectPath)

	resultCh := make(chan struct {
		val string
		err error
	}, 1)
	go func() {
		var secret string
		callErr := obj.Call(Interface+".RequestSecret", 0, "/github/token").Store(&secret)
		resultCh <- struct {
			val string
			err error
		}{secret, callErr}
	}()

	// Approve via manager.
	req := pollPendingFromManager(t, mgr)
	if err := mgr.Approve(req.ID); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	res := <-resultCh
	if res.err != nil {
		t.Fatalf("RequestSecret D-Bus call failed: %v", res.err)
	}
	if res.val != "approved:/github/token" {
		t.Errorf("got %q, want %q", res.val, "approved:/github/token")
	}
}

// TestDaemon_RequestSecret_Deny verifies that a D-Bus caller receives
// net.mowaka.Error.Denied when the manager denies the request.
func TestDaemon_RequestSecret_Deny(t *testing.T) {
	mgr := approval.NewManager(5*time.Second, 100)
	addr := startDaemonForTest(t, Config{
		Version:         "test",
		Timeout:         5 * time.Second,
		ApprovalManager: mgr,
	})

	client, err := dbus.Connect(addr)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	defer client.Close()

	obj := client.Object(BusName, ObjectPath)

	resultCh := make(chan error, 1)
	go func() {
		var secret string
		resultCh <- obj.Call(Interface+".RequestSecret", 0, "/github/token").Store(&secret)
	}()

	req := pollPendingFromManager(t, mgr)
	if err := mgr.Deny(req.ID); err != nil {
		t.Fatalf("Deny: %v", err)
	}

	callErr := <-resultCh
	if callErr == nil {
		t.Fatal("expected D-Bus error on denial, got nil")
	}
	dbusErr, ok := callErr.(dbus.Error)
	if !ok {
		t.Fatalf("expected dbus.Error, got %T: %v", callErr, callErr)
	}
	if dbusErr.Name != "net.mowaka.Error.Denied" {
		t.Errorf("got error name %q, want net.mowaka.Error.Denied", dbusErr.Name)
	}
}

// TestDaemon_RequestSecret_Timeout verifies that a D-Bus caller receives
// net.mowaka.Error.Timeout when the request expires without decision.
func TestDaemon_RequestSecret_Timeout(t *testing.T) {
	mgr := approval.NewManager(500*time.Millisecond, 100)
	addr := startDaemonForTest(t, Config{
		Version:         "test",
		Timeout:         500 * time.Millisecond,
		ApprovalManager: mgr,
	})

	client, err := dbus.Connect(addr)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	defer client.Close()

	obj := client.Object(BusName, ObjectPath)

	start := time.Now()
	var secret string
	callErr := obj.Call(Interface+".RequestSecret", 0, "/github/token").Store(&secret)
	elapsed := time.Since(start)

	if callErr == nil {
		t.Fatal("expected D-Bus error on timeout, got nil")
	}
	dbusErr, ok := callErr.(dbus.Error)
	if !ok {
		t.Fatalf("expected dbus.Error, got %T: %v", callErr, callErr)
	}
	if dbusErr.Name != "net.mowaka.Error.Timeout" {
		t.Errorf("got error name %q, want net.mowaka.Error.Timeout", dbusErr.Name)
	}
	if elapsed < 400*time.Millisecond {
		t.Errorf("completed too quickly (%v), expected ~500ms", elapsed)
	}
}

// TestDaemon_ConcurrentRequests verifies that two concurrent D-Bus callers
// block independently and resolve independently without interfering.
func TestDaemon_ConcurrentRequests(t *testing.T) {
	mgr := approval.NewManager(10*time.Second, 100)
	addr := startDaemonForTest(t, Config{
		Version:         "test",
		Timeout:         10 * time.Second,
		ApprovalManager: mgr,
	})

	client, err := dbus.Connect(addr)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	defer client.Close()

	obj := client.Object(BusName, ObjectPath)

	type result struct {
		val string
		err error
	}
	ch1 := make(chan result, 1)
	ch2 := make(chan result, 1)

	go func() {
		var s string
		e := obj.Call(Interface+".RequestSecret", 0, "/path/one").Store(&s)
		ch1 <- result{s, e}
	}()
	go func() {
		var s string
		e := obj.Call(Interface+".RequestSecret", 0, "/path/two").Store(&s)
		ch2 <- result{s, e}
	}()

	// Wait for both requests to appear.
	var req1, req2 *approval.Request
	for i := 0; i < 200; i++ {
		reqs := mgr.List()
		if len(reqs) >= 2 {
			req1 = reqs[0]
			req2 = reqs[1]
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if req1 == nil || req2 == nil {
		t.Fatal("expected 2 pending requests within 2s")
	}

	// Approve one, deny the other.
	if err := mgr.Approve(req1.ID); err != nil {
		t.Fatalf("Approve req1: %v", err)
	}
	if err := mgr.Deny(req2.ID); err != nil {
		t.Fatalf("Deny req2: %v", err)
	}

	// Collect results — either caller could be in ch1 or ch2, so we wait for both.
	r1 := <-ch1
	r2 := <-ch2

	// One should succeed, one should fail.
	successes := 0
	denials := 0
	for _, r := range []result{r1, r2} {
		if r.err == nil {
			successes++
		} else if e, ok := r.err.(dbus.Error); ok && e.Name == "net.mowaka.Error.Denied" {
			denials++
		} else {
			t.Errorf("unexpected error: %v", r.err)
		}
	}
	if successes != 1 || denials != 1 {
		t.Errorf("expected 1 success + 1 denial, got %d successes + %d denials", successes, denials)
	}
}

// testSignerForDaemon is an unexported-interface-compatible GPG signer for integration tests.
// It satisfies the gpgSigner interface used by daemon.Config.Signer.
type testSignerForDaemon struct {
	sig    []byte
	status []byte
}

func (s *testSignerForDaemon) Sign(_ []byte, _ string) ([]byte, []byte, int, error) {
	return s.sig, s.status, 0, nil
}

// TestDaemon_RequestSign_Approve verifies that a D-Bus caller receives GPG
// signature bytes when RequestSign is approved.
func TestDaemon_RequestSign_Approve(t *testing.T) {
	mgr := approval.NewManager(5*time.Second, 100)

	fakeSig := []byte("-----BEGIN PGP SIGNATURE-----\nfake\n-----END PGP SIGNATURE-----\n")
	fakeSigner := &testSignerForDaemon{
		sig:    fakeSig,
		status: []byte("[GNUPG:] SIG_CREATED"),
	}

	addr := startDaemonForTest(t, Config{
		Version:         "test",
		Timeout:         5 * time.Second,
		ApprovalManager: mgr,
		Signer:          fakeSigner,
	})

	client, err := dbus.Connect(addr)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	defer client.Close()

	obj := client.Object(BusName, ObjectPath)

	type signResult struct {
		sig    []byte
		status []byte
		err    error
	}
	resultCh := make(chan signResult, 1)
	go func() {
		var sig, status []byte
		callErr := obj.Call(Interface+".RequestSign", 0,
			"my-repo",
			"fix: typo",
			"Alice <alice@example.com>",
			"Alice <alice@example.com>",
			"ABCD1234",
			[]string{"README.md"},
			"tree abc\nauthor ...\n",
		).Store(&sig, &status)
		resultCh <- signResult{sig, status, callErr}
	}()

	req := pollPendingFromManager(t, mgr)
	if err := mgr.Approve(req.ID); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	res := <-resultCh
	if res.err != nil {
		t.Fatalf("RequestSign D-Bus call failed: %v", res.err)
	}
	if string(res.sig) != string(fakeSig) {
		t.Errorf("signature mismatch: got %q, want %q", res.sig, fakeSig)
	}
}
