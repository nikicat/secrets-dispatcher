package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/nikicat/secrets-dispatcher/internal/gpgsign"
)

const remoteCommit = "tree 4b825dc642cb6eb9a060e54bf8d69288fbee4904\nauthor A\ncommitter A\n\nfix: remote\n"

// Pending-request timeout long enough that no test request expires on its own.
const remoteTimeout = 5 * time.Second

// A server with remote "nb-home" open on a real socket, signing with a fake gpg.
type remoteHarness struct {
	mgr    *approval.Manager
	h      *Handlers
	server *Server
	remote *RemoteServer
}

// What the thin client's remote call returned.
type remoteSignOutcome struct {
	res gpgsign.SignOutcome
	err error
}

func newRemoteHarness(t *testing.T) *remoteHarness {
	return newRemoteHarnessExpiring(t, remoteTimeout)
}

// Like newRemoteHarness, with pending requests expiring after timeout.
func newRemoteHarnessExpiring(t *testing.T, timeout time.Duration) *remoteHarness {
	t.Helper()
	mgr := newTestManager(func(cfg *approval.ManagerConfig) { cfg.Timeout = timeout })
	h := testHandlers(t, mgr)
	h.resolver.GPGRunner = &fakeGPGRunner{res: approval.GPGResult{Signature: []byte("SIG"), Status: []byte("[GNUPG:] SIG_CREATED\n")}}
	server := &Server{handlers: h, httpServer: &http.Server{}}
	require.NoError(t, server.ServeAPISocket(config.RuntimeDir(t.TempDir())))
	rs, err := server.ServeRemote("nb-home")
	require.NoError(t, err)
	t.Cleanup(func() { server.Shutdown(context.Background()) }) //nolint:errcheck
	return &remoteHarness{mgr: mgr, h: h, server: server, remote: rs}
}

// Runs the thin client's remote call in the background.
func (rh *remoteHarness) signAsync(t *testing.T, ctx context.Context) <-chan remoteSignOutcome {
	t.Helper()
	info, err := approval.NewGPGSignInfo(approval.SignedObject(remoteCommit), "repo", "ABCD", approval.ListedFiles(nil))
	require.NoError(t, err)
	out := make(chan remoteSignOutcome, 1)
	go func() {
		res, err := gpgsign.SignRemote(ctx, rh.remote.SocketPath, info)
		out <- remoteSignOutcome{res: res, err: err}
	}()
	return out
}

func (rh *remoteHarness) waitPending(t *testing.T) *approval.Request {
	t.Helper()
	var pending []approval.Pending
	require.Eventually(t, func() bool { pending = rh.mgr.List(); return len(pending) == 1 }, 2*time.Second, 10*time.Millisecond)
	return pending[0].Request
}

func TestRemoteSign_ApprovedReturnsSignature(t *testing.T) {
	rh := newRemoteHarness(t)
	result := rh.signAsync(t, context.Background())

	req := rh.waitPending(t)
	assert.Equal(t, approval.ClientName("remote:nb-home"), req.SenderInfo.Client)
	assert.Empty(t, req.SenderInfo.ProcessChain, "a remote request has no local process chain")
	assert.Equal(t, "fix: remote", requireGPGSign(t, req).Signed.Message, "display stays bound to the signed bytes")
	require.NoError(t, rh.h.resolver.Approve(req.ID))

	out := <-result
	require.NoError(t, out.err)
	assert.Equal(t, approval.ResolutionApproved, out.res.Result)
	require.NotNil(t, out.res.GPGResult)
	assert.Equal(t, []byte("SIG"), out.res.Signature)
	assert.Equal(t, []byte("[GNUPG:] SIG_CREATED\n"), out.res.Status)
}

func TestRemoteSign_ClientReadsEveryResolution(t *testing.T) {
	cases := []struct {
		want    approval.Resolution
		timeout time.Duration
		resolve func(rh *remoteHarness, id approval.RequestID) error
	}{
		{approval.ResolutionApproved, remoteTimeout, func(rh *remoteHarness, id approval.RequestID) error { return rh.h.resolver.Approve(id) }},
		{approval.ResolutionDenied, remoteTimeout, func(rh *remoteHarness, id approval.RequestID) error { return rh.h.resolver.Deny(id) }},
		{approval.ResolutionCancelled, remoteTimeout, func(rh *remoteHarness, id approval.RequestID) error { return rh.mgr.Cancel(id) }},
		{approval.ResolutionExpired, 500 * time.Millisecond, func(*remoteHarness, approval.RequestID) error { return nil }},
	}
	for _, c := range cases {
		t.Run(string(c.want), func(t *testing.T) {
			rh := newRemoteHarnessExpiring(t, c.timeout)
			result := rh.signAsync(t, context.Background())
			require.NoError(t, c.resolve(rh, rh.waitPending(t).ID))
			out := <-result
			require.NoError(t, out.err)
			assert.Equal(t, c.want, out.res.Result)
			assert.Equal(t, c.want == approval.ResolutionApproved, out.res.GPGResult != nil, "GPGResult only on approval")
		})
	}
}

func TestRemoteSign_DroppedConnectionCancelsRequest(t *testing.T) {
	rh := newRemoteHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	result := rh.signAsync(t, ctx)

	rh.waitPending(t)
	cancel()
	assert.ErrorIs(t, (<-result).err, context.Canceled)
	require.Eventually(t, func() bool { return len(rh.mgr.List()) == 0 }, 2*time.Second, 10*time.Millisecond)
	assert.Equal(t, approval.ResolutionCancelled, rh.mgr.History()[0].Resolution)
}

func TestRemoteSign_ShutdownKeepsItsDeadline(t *testing.T) {
	rh := newRemoteHarness(t)
	result := rh.signAsync(t, context.Background())
	req := rh.waitPending(t)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	assert.ErrorIs(t, rh.server.Shutdown(ctx), context.DeadlineExceeded)
	assert.Less(t, time.Since(start), time.Second, "a pending remote request must not hold up shutdown")

	require.NoError(t, rh.h.resolver.Deny(req.ID))
	require.NoError(t, (<-result).err)
}

func TestServeRemote_NeedsAPISocket(t *testing.T) {
	_, err := (&Server{}).ServeRemote("nb-home")
	assert.Error(t, err)
}

func TestRemoteSign_ServesNothingButSigning(t *testing.T) {
	socket := newRemoteHarness(t).remote.SocketPath
	client := gpgsign.UnixSocketClient(socket)
	for _, path := range []string{"/api/v1/pending", "/api/v1/ws", "/api/v1/log"} {
		resp, err := client.Get(gpgsign.SocketURL(path))
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusNotFound, resp.StatusCode, path)
	}
}
