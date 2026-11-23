package approval

import (
	"context"
	"testing"
	"testing/synctest"

	"github.com/nikicat/secrets-dispatcher/internal/sender"
	"github.com/stretchr/testify/require"
)

var ghSender = LocalSenderForTest("gh", "/usr/bin/gh")

// Returns a manager made as NewManagerForTest makes it, and a testObserver
// subscribed to it.
func newObservedManager(cfg ManagerConfig) (*Manager, *testObserver) {
	mgr := NewManagerForTest(cfg)
	obs := &testObserver{}
	mgr.Subscribe(obs)
	return mgr, obs
}

// Returns the Secret Service path of the named item in the named collection.
func itemPath(collection, name string) string {
	return "/org/freedesktop/secrets/collection/" + collection + "/" + name
}

// Returns an ItemInfo whose path places it in the named collection.
func itemInCollection(collection, name string) ItemInfo {
	return ItemInfo{Path: itemPath(collection, name)}
}

// Returns a get-secret request for one item.
func getSecretRequest() RequestInfo {
	return RequestInfo{Items: []ItemInfo{{Path: "/test/item"}}, Session: "/session/1", Type: RequestTypeGetSecret}
}

// Returns a sender known only by its unique bus name.
func busSender(name sender.UniqueName) SenderInfo {
	return SenderInfo{Sender: &name}
}

// Returns the resolutions in History, newest first.
func historyResolutions(mgr *Manager) []Resolution {
	var out []Resolution
	for _, e := range mgr.History() {
		out = append(out, e.Resolution)
	}
	return out
}

// A RequireApproval call running in a goroutine of its own.
type approvalCall struct {
	done     chan struct{}
	prompted bool
	err      error
}

// Calls RequireApproval in a new goroutine.
func startApproval(ctx context.Context, mgr *Manager, info RequestInfo) *approvalCall {
	c := &approvalCall{done: make(chan struct{})}
	go func() {
		defer close(c.done)
		c.prompted, c.err = mgr.RequireApproval(ctx, info)
	}()
	return c
}

// Waits until the call returns, and returns its result.
func (c *approvalCall) wait() (prompted bool, err error) {
	<-c.done
	return c.prompted, c.err
}

// Waits until every other goroutine of the synctest bubble is blocked, and
// returns the pending requests, failing the test unless there are n of them.
func waitPending(t *testing.T, mgr *Manager, n int) []Pending {
	t.Helper()
	synctest.Wait()
	pending := mgr.Pending()
	require.Len(t, pending, n, "pending requests")
	return pending
}

// Waits as waitPending does for the one pending request, and returns its ID.
func waitPendingID(t *testing.T, mgr *Manager) RequestID {
	t.Helper()
	return waitPending(t, mgr, 1)[0].ID
}

// Fails the test unless the request goes pending, and cancels it. The manager
// must have no other pending request.
func requirePrompts(t *testing.T, mgr *Manager, info RequestInfo) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	call := startApproval(ctx, mgr, info)
	waitPending(t, mgr, 1)
	cancel()
	_, err := call.wait()
	require.ErrorIs(t, err, context.Canceled)
}
