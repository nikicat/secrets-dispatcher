package sshagent

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"log/slog"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

const testKeyComment = "test-key@localhost"

// Returns a keyring holding one fresh key with comment testKeyComment.
func newUpstream(t *testing.T) agent.Agent {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	upstream := agent.NewKeyring()
	require.NoError(t, upstream.Add(agent.AddedKey{PrivateKey: key, Comment: testKeyComment}))
	return upstream
}

type signResult struct {
	sig *ssh.Signature
	err error
}

// A Sign call blocked on its approval request.
type pendingSign struct {
	mgr    *approval.Manager
	req    approval.Pending
	result <-chan signResult
}

// Starts a Sign through a proxy whose manager asks for approval, and waits in
// the synctest bubble until its request is pending.
func startSign(t *testing.T, destination *string) pendingSign {
	t.Helper()
	mgr := approval.NewManagerForTest(approval.ManagerConfig{})
	proxy := newProxyAgent(newUpstream(t), mgr, approval.SenderInfo{
		InvokerName: "test-proc",
	}, destination, slog.Default())

	keys, err := proxy.List()
	require.NoError(t, err)

	result := make(chan signResult, 1)
	go func() {
		sig, err := proxy.Sign(keys[0], []byte("test data to sign"))
		result <- signResult{sig, err}
	}()

	synctest.Wait()
	pending := mgr.Pending()
	require.Len(t, pending, 1, "no pending request appeared")
	return pendingSign{mgr: mgr, req: pending[0], result: result}
}

func TestProxyAgent_ListPassthrough(t *testing.T) {
	mgr := approval.NewDisabledManager()
	proxy := newProxyAgent(newUpstream(t), mgr, approval.SenderInfo{}, nil, slog.Default())

	keys, err := proxy.List()
	require.NoError(t, err)
	require.Len(t, keys, 1)
	assert.Equal(t, testKeyComment, keys[0].Comment)
}

func TestProxyAgent_SignApproved(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sign := startSign(t, new("example.com"))

		assert.Equal(t, approval.RequestTypeSSHSign, sign.req.Type)
		require.Len(t, sign.req.Items, 1)
		assert.Equal(t, testKeyComment, sign.req.Items[0].Label)
		assert.Equal(t, "example.com", sign.req.Items[0].SSHDestination())

		require.NoError(t, sign.mgr.Approve(sign.req.ID))

		result := <-sign.result
		require.NoError(t, result.err, "sign after approval failed")
		require.NotNil(t, result.sig)
	})
}

func TestProxyAgent_SignDenied(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sign := startSign(t, nil)

		require.NoError(t, sign.mgr.Deny(sign.req.ID))

		result := <-sign.result
		require.Error(t, result.err, "expected error after denial")
		assert.Nil(t, result.sig, "expected nil signature after denial")
	})
}

func TestProxyAgent_SignDisabledManager(t *testing.T) {
	// With a disabled manager, sign should pass through immediately
	mgr := approval.NewDisabledManager()
	proxy := newProxyAgent(newUpstream(t), mgr, approval.SenderInfo{}, nil, slog.Default())

	keys, _ := proxy.List()
	require.NotEmpty(t, keys, "no keys")

	sig, err := proxy.Sign(keys[0], []byte("data"))
	require.NoError(t, err, "sign with disabled manager failed")
	require.NotNil(t, sig)
}

func TestProxyAgent_ExtensionPassthrough(t *testing.T) {
	upstream := agent.NewKeyring()
	mgr := approval.NewDisabledManager()
	proxy := newProxyAgent(upstream, mgr, approval.SenderInfo{}, nil, slog.Default())

	_, err := proxy.Extension("session-bind@openssh.com", []byte{})
	assert.ErrorIs(t, err, agent.ErrExtensionUnsupported)
}
