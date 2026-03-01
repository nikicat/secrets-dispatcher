package sshagent

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"log/slog"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

func newTestKey(t *testing.T) agent.AddedKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return agent.AddedKey{
		PrivateKey: key,
		Comment:    "test-key@localhost",
	}
}

func TestProxyAgent_ListPassthrough(t *testing.T) {
	upstream := agent.NewKeyring()
	testKey := newTestKey(t)
	if err := upstream.Add(testKey); err != nil {
		t.Fatal(err)
	}

	mgr := approval.NewDisabledManager()
	proxy := newProxyAgent(upstream, mgr, approval.SenderInfo{}, "", slog.Default())

	keys, err := proxy.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(keys))
	}
	if keys[0].Comment != "test-key@localhost" {
		t.Errorf("expected comment %q, got %q", "test-key@localhost", keys[0].Comment)
	}
}

func TestProxyAgent_SignApproved(t *testing.T) {
	upstream := agent.NewKeyring()
	testKey := newTestKey(t)
	if err := upstream.Add(testKey); err != nil {
		t.Fatal(err)
	}

	// Use a real manager with short timeout — we'll approve from another goroutine
	mgr := approval.NewManager(approval.ManagerConfig{
		Timeout:    5 * time.Second,
		HistoryMax: 100,
	})

	proxy := newProxyAgent(upstream, mgr, approval.SenderInfo{
		UnitName: "test-proc",
	}, "example.com", slog.Default())

	// Pre-populate key cache (normally done by List())
	keys, _ := upstream.List()
	proxy.keysMu.Lock()
	proxy.keys = keys
	proxy.keysMu.Unlock()

	pubKey := keys[0]
	data := []byte("test data to sign")

	// Start signing in a goroutine — it will block on approval
	type signResult struct {
		sig *ssh.Signature
		err error
	}
	ch := make(chan signResult, 1)
	go func() {
		sig, err := proxy.Sign(pubKey, data)
		ch <- signResult{sig, err}
	}()

	// Wait for the pending request to appear
	var pending []*approval.Request
	for i := 0; i < 50; i++ {
		time.Sleep(10 * time.Millisecond)
		pending = mgr.List()
		if len(pending) > 0 {
			break
		}
	}
	if len(pending) == 0 {
		t.Fatal("no pending request appeared")
	}

	// Verify request metadata
	req := pending[0]
	if req.Type != approval.RequestTypeSSHSign {
		t.Errorf("expected type %q, got %q", approval.RequestTypeSSHSign, req.Type)
	}
	if len(req.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(req.Items))
	}
	if req.Items[0].Label != "test-key@localhost" {
		t.Errorf("expected label %q, got %q", "test-key@localhost", req.Items[0].Label)
	}
	if req.Items[0].Attributes["destination"] != "example.com" {
		t.Errorf("expected destination %q, got %q", "example.com", req.Items[0].Attributes["destination"])
	}

	// Approve
	if err := mgr.Approve(req.ID); err != nil {
		t.Fatal(err)
	}

	// Check the sign result
	result := <-ch
	if result.err != nil {
		t.Fatalf("sign after approval failed: %v", result.err)
	}
	if result.sig == nil {
		t.Fatal("expected non-nil signature")
	}
}

func TestProxyAgent_SignDenied(t *testing.T) {
	upstream := agent.NewKeyring()
	testKey := newTestKey(t)
	if err := upstream.Add(testKey); err != nil {
		t.Fatal(err)
	}

	mgr := approval.NewManager(approval.ManagerConfig{
		Timeout:    5 * time.Second,
		HistoryMax: 100,
	})

	proxy := newProxyAgent(upstream, mgr, approval.SenderInfo{
		UnitName: "test-proc",
	}, "", slog.Default())

	keys, _ := upstream.List()
	proxy.keysMu.Lock()
	proxy.keys = keys
	proxy.keysMu.Unlock()

	pubKey := keys[0]
	data := []byte("test data to sign")

	type signResult struct {
		sig *ssh.Signature
		err error
	}
	ch := make(chan signResult, 1)
	go func() {
		sig, err := proxy.Sign(pubKey, data)
		ch <- signResult{sig, err}
	}()

	// Wait for the pending request
	var pending []*approval.Request
	for i := 0; i < 50; i++ {
		time.Sleep(10 * time.Millisecond)
		pending = mgr.List()
		if len(pending) > 0 {
			break
		}
	}
	if len(pending) == 0 {
		t.Fatal("no pending request appeared")
	}

	// Deny
	if err := mgr.Deny(pending[0].ID); err != nil {
		t.Fatal(err)
	}

	result := <-ch
	if result.err == nil {
		t.Fatal("expected error after denial, got nil")
	}
	if result.sig != nil {
		t.Error("expected nil signature after denial")
	}
}

func TestProxyAgent_SignDisabledManager(t *testing.T) {
	// With a disabled manager, sign should pass through immediately
	upstream := agent.NewKeyring()
	testKey := newTestKey(t)
	if err := upstream.Add(testKey); err != nil {
		t.Fatal(err)
	}

	mgr := approval.NewDisabledManager()
	proxy := newProxyAgent(upstream, mgr, approval.SenderInfo{}, "", slog.Default())

	keys, _ := proxy.List()
	if len(keys) == 0 {
		t.Fatal("no keys")
	}

	sig, err := proxy.Sign(keys[0], []byte("data"))
	if err != nil {
		t.Fatalf("sign with disabled manager failed: %v", err)
	}
	if sig == nil {
		t.Fatal("expected non-nil signature")
	}
}

func TestProxyAgent_ExtensionPassthrough(t *testing.T) {
	upstream := agent.NewKeyring()
	mgr := approval.NewDisabledManager()
	proxy := newProxyAgent(upstream, mgr, approval.SenderInfo{}, "", slog.Default())

	// Keyring doesn't support extensions, so we expect agent.ErrExtensionUnsupported
	_, err := proxy.Extension("session-bind@openssh.com", []byte{})
	if err == nil {
		t.Fatal("expected error from keyring extension, got nil")
	}
}
