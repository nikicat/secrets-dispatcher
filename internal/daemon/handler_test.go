package daemon

import (
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/godbus/dbus/v5"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

// mockSender is a test messageSender that records every message sent to it.
type mockSender struct {
	mu   sync.Mutex
	msgs []tea.Msg
}

func (m *mockSender) Send(msg tea.Msg) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.msgs = append(m.msgs, msg)
}

// mockResolver implements senderResolver for tests.
type mockResolver struct {
	info approval.SenderInfo
}

func (r *mockResolver) Resolve(_ string) approval.SenderInfo {
	return r.info
}

// mockSigner implements gpgSigner for tests.
type mockSigner struct {
	sig      []byte
	status   []byte
	exitCode int
	err      error
}

func (s *mockSigner) Sign(_ []byte, _ string) ([]byte, []byte, int, error) {
	return s.sig, s.status, s.exitCode, s.err
}

// pollForPending polls mgr.List() until at least one request appears or timeout.
func pollForPending(t *testing.T, mgr *approval.Manager) *approval.Request {
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

// newTestDispatcher creates a Dispatcher wired with the given manager, a mock
// resolver, and a mock sender as the TUI program.
func newTestDispatcher(mgr *approval.Manager) (*Dispatcher, *mockSender) {
	resolver := &mockResolver{info: approval.SenderInfo{PID: 1234, UID: 1000, UserName: "test-user"}}
	d := NewDispatcher("test", mgr, resolver)
	sender := &mockSender{}
	d.program = sender
	return d, sender
}

// TestRequestSecret_Approved verifies that RequestSecret returns "approved:<path>"
// when the companion user approves.
func TestRequestSecret_Approved(t *testing.T) {
	mgr := approval.NewManager(5*time.Second, 100)
	d, _ := newTestDispatcher(mgr)

	resultCh := make(chan struct {
		val string
		err *dbus.Error
	}, 1)

	go func() {
		val, err := d.RequestSecret(dbus.Sender(":1.1"), "/github/token")
		resultCh <- struct {
			val string
			err *dbus.Error
		}{val, err}
	}()

	req := pollForPending(t, mgr)
	if err := mgr.Approve(req.ID); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	res := <-resultCh
	if res.err != nil {
		t.Fatalf("RequestSecret returned dbus error: %v", res.err)
	}
	want := "approved:/github/token"
	if res.val != want {
		t.Errorf("got %q, want %q", res.val, want)
	}
}

// TestRequestSecret_Denied verifies that RequestSecret returns net.mowaka.Error.Denied
// when the companion user denies.
func TestRequestSecret_Denied(t *testing.T) {
	mgr := approval.NewManager(5*time.Second, 100)
	d, _ := newTestDispatcher(mgr)

	resultCh := make(chan *dbus.Error, 1)
	go func() {
		_, err := d.RequestSecret(dbus.Sender(":1.1"), "/github/token")
		resultCh <- err
	}()

	req := pollForPending(t, mgr)
	if err := mgr.Deny(req.ID); err != nil {
		t.Fatalf("Deny: %v", err)
	}

	dbusErr := <-resultCh
	if dbusErr == nil {
		t.Fatal("expected dbus error on denial, got nil")
	}
	if dbusErr.Name != "net.mowaka.Error.Denied" {
		t.Errorf("got error name %q, want net.mowaka.Error.Denied", dbusErr.Name)
	}
}

// TestRequestSecret_Timeout verifies that RequestSecret returns net.mowaka.Error.Timeout
// when the approval request expires.
func TestRequestSecret_Timeout(t *testing.T) {
	// Use a short timeout so the test completes quickly.
	mgr := approval.NewManager(200*time.Millisecond, 100)
	d, _ := newTestDispatcher(mgr)

	start := time.Now()
	_, dbusErr := d.RequestSecret(dbus.Sender(":1.1"), "/github/token")
	elapsed := time.Since(start)

	if dbusErr == nil {
		t.Fatal("expected dbus error on timeout, got nil")
	}
	if dbusErr.Name != "net.mowaka.Error.Timeout" {
		t.Errorf("got error name %q, want net.mowaka.Error.Timeout", dbusErr.Name)
	}
	// Should have taken roughly the timeout duration.
	if elapsed < 150*time.Millisecond {
		t.Errorf("completed too quickly (%v), expected ~200ms", elapsed)
	}
}

// TestRequestSecret_NilProgram verifies that RequestSecret returns
// net.mowaka.Error.NotReady when the TUI has not been initialized.
func TestRequestSecret_NilProgram(t *testing.T) {
	mgr := approval.NewManager(5*time.Second, 100)
	d := NewDispatcher("test", mgr, nil)
	// program is nil — not calling SetProgram or setting d.program

	_, dbusErr := d.RequestSecret(dbus.Sender(":1.1"), "/github/token")
	if dbusErr == nil {
		t.Fatal("expected dbus error, got nil")
	}
	if dbusErr.Name != "net.mowaka.Error.NotReady" {
		t.Errorf("got error name %q, want net.mowaka.Error.NotReady", dbusErr.Name)
	}
}

// TestRequestSign_Approved verifies that RequestSign returns signature bytes
// from the injected signer when approved.
func TestRequestSign_Approved(t *testing.T) {
	mgr := approval.NewManager(5*time.Second, 100)
	d, _ := newTestDispatcher(mgr)
	d.signer = &mockSigner{
		sig:    []byte("-----BEGIN PGP SIGNATURE-----\nfake\n-----END PGP SIGNATURE-----\n"),
		status: []byte("[GNUPG:] SIG_CREATED"),
	}

	resultCh := make(chan struct {
		sig    []byte
		status []byte
		err    *dbus.Error
	}, 1)

	go func() {
		sig, status, err := d.RequestSign(
			dbus.Sender(":1.1"),
			"my-repo", "fix: typo", "Alice <alice@example.com>",
			"Alice <alice@example.com>", "ABCD1234",
			[]string{"README.md"},
			"tree abc\nauthor ...\n",
		)
		resultCh <- struct {
			sig    []byte
			status []byte
			err    *dbus.Error
		}{sig, status, err}
	}()

	req := pollForPending(t, mgr)
	if err := mgr.Approve(req.ID); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	res := <-resultCh
	if res.err != nil {
		t.Fatalf("RequestSign returned dbus error: %v", res.err)
	}
	if string(res.sig) != "-----BEGIN PGP SIGNATURE-----\nfake\n-----END PGP SIGNATURE-----\n" {
		t.Errorf("unexpected signature: %q", res.sig)
	}
}

// TestRequestSign_Denied verifies that RequestSign returns net.mowaka.Error.Denied
// when the companion user denies.
func TestRequestSign_Denied(t *testing.T) {
	mgr := approval.NewManager(5*time.Second, 100)
	d, _ := newTestDispatcher(mgr)

	resultCh := make(chan *dbus.Error, 1)
	go func() {
		_, _, err := d.RequestSign(
			dbus.Sender(":1.1"),
			"my-repo", "fix: typo", "Alice <alice@example.com>",
			"Alice <alice@example.com>", "ABCD1234",
			[]string{"README.md"},
			"tree abc\nauthor ...\n",
		)
		resultCh <- err
	}()

	req := pollForPending(t, mgr)
	if err := mgr.Deny(req.ID); err != nil {
		t.Fatalf("Deny: %v", err)
	}

	dbusErr := <-resultCh
	if dbusErr == nil {
		t.Fatal("expected dbus error on denial, got nil")
	}
	if dbusErr.Name != "net.mowaka.Error.Denied" {
		t.Errorf("got error name %q, want net.mowaka.Error.Denied", dbusErr.Name)
	}
}
