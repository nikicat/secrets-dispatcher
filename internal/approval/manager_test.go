package approval

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/nikicat/secrets-dispatcher/internal/procutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManager_RequireApproval_Approved(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManagerForTest(ManagerConfig{})

		call := startApproval(context.Background(), mgr, getSecretRequest())
		require.NoError(t, mgr.Approve(waitPendingID(t, mgr)))

		_, err := call.wait()
		assert.NoError(t, err, "RequireApproval")
	})
}

func TestManager_RequireApproval_Denied(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManagerForTest(ManagerConfig{})

		call := startApproval(context.Background(), mgr, getSecretRequest())
		require.NoError(t, mgr.Deny(waitPendingID(t, mgr)))

		_, err := call.wait()
		assert.Equal(t, ErrDenied, err)
	})
}

func TestManager_RequireApproval_ContextCanceled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManagerForTest(ManagerConfig{})

		ctx, cancel := context.WithCancel(context.Background())
		call := startApproval(ctx, mgr, getSecretRequest())
		waitPending(t, mgr, 1)
		cancel()

		_, err := call.wait()
		assert.Equal(t, context.Canceled, err)
	})
}

func TestManager_Pending(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManagerForTest(ManagerConfig{})

		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		var calls []*approvalCall
		for range 3 {
			calls = append(calls, startApproval(ctx, mgr, getSecretRequest()))
		}

		waitPending(t, mgr, 3)

		for _, call := range calls {
			call.wait()
		}
	})
}

func TestManager_Cancel_Success(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManagerForTest(ManagerConfig{})

		call := startApproval(context.Background(), mgr, getSecretRequest())
		require.NoError(t, mgr.Cancel(waitPendingID(t, mgr)))

		_, err := call.wait()
		// Cancel resolves the request as cancelled, which RequireApproval reports as ErrDenied.
		assert.Equal(t, ErrDenied, err, "after cancel")

		assert.Zero(t, mgr.PendingCount(), "request should be cleaned up after cancel")
	})
}

func TestManager_Cancel_NotFound(t *testing.T) {
	mgr := NewManagerForTest(ManagerConfig{})

	assert.Equal(t, ErrNotFound, mgr.Cancel("nonexistent-id"))
}

func TestManager_Approve_NotFound(t *testing.T) {
	mgr := NewManagerForTest(ManagerConfig{})

	assert.Equal(t, ErrNotFound, mgr.Approve("nonexistent-id"))
}

func TestManager_Deny_NotFound(t *testing.T) {
	mgr := NewManagerForTest(ManagerConfig{})

	assert.Equal(t, ErrNotFound, mgr.Deny("nonexistent-id"))
}

func TestManager_Concurrent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManagerForTest(ManagerConfig{})

		const numRequests = 10
		var calls []*approvalCall
		for range numRequests {
			calls = append(calls, startApproval(context.Background(), mgr, getSecretRequest()))
		}

		for i, req := range waitPending(t, mgr, numRequests) {
			if i%2 == 0 {
				require.NoError(t, mgr.Approve(req.ID))
			} else {
				require.NoError(t, mgr.Deny(req.ID))
			}
		}

		var approved, denied int
		for _, call := range calls {
			switch _, err := call.wait(); err {
			case nil:
				approved++
			case ErrDenied:
				denied++
			default:
				assert.Failf(t, "unexpected error", "%v", err)
			}
		}

		assert.Equal(t, numRequests, approved+denied, "%d approved + %d denied", approved, denied)
	})
}

func TestManager_Disabled(t *testing.T) {
	mgr := NewDisabledManager()

	// Should auto-approve immediately
	_, err := mgr.RequireApproval(context.Background(), getSecretRequest())
	assert.NoError(t, err, "disabled manager should auto-approve")

	assert.Zero(t, mgr.PendingCount(), "disabled manager should have no pending requests")
}

func TestManager_RequestFields(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManagerForTest(ManagerConfig{})

		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		call := startApproval(ctx, mgr, RequestInfo{Items: []ItemInfo{{Path: "/test/item1"}, {Path: "/test/item2"}}, Session: "/session/42", Type: RequestTypeGetSecret, SenderInfo: SenderInfo{Client: "test-client"}})

		req := waitPending(t, mgr, 1)[0]
		assert.Equal(t, ClientName("test-client"), req.SenderInfo.Client)
		assert.Len(t, req.Items, 2)
		assert.Equal(t, "/session/42", string(req.Session))
		assert.NotEmpty(t, req.ID)
		assert.False(t, req.CreatedAt.IsZero(), "expected non-zero CreatedAt")
		assert.False(t, req.ExpiresAt.IsZero(), "expected non-zero ExpiresAt")
		assert.False(t, req.ExpiresAt.Before(req.CreatedAt), "ExpiresAt should be after CreatedAt")

		call.wait()
	})
}

func TestManager_CleanupAfterApproval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManagerForTest(ManagerConfig{})

		call := startApproval(context.Background(), mgr, getSecretRequest())
		reqID := waitPendingID(t, mgr)
		require.NoError(t, mgr.Approve(reqID))
		call.wait()

		assert.Zero(t, mgr.PendingCount(), "request should be cleaned up after approval")

		assert.Equal(t, ErrNotFound, mgr.Approve(reqID), "second approval")
	})
}

// The kind of request event that testObserver received: created, or ended
// with the resolution of the same name.
type eventKind string

const (
	evCreated      eventKind = "created"
	evApproved               = eventKind(ResolutionApproved)
	evDenied                 = eventKind(ResolutionDenied)
	evExpired                = eventKind(ResolutionExpired)
	evCancelled              = eventKind(ResolutionCancelled)
	evAutoApproved           = eventKind(ResolutionAutoApproved)
	evIgnored                = eventKind(ResolutionIgnored)
)

type testEvent struct {
	Type    eventKind
	Request *Request
}

// Keeps the request events it receives, in order.
type testObserver struct {
	IgnoreEvents
	mu     sync.Mutex
	events []testEvent
}

func (o *testObserver) RequestCreated(p Pending) {
	o.add(testEvent{evCreated, p.Request})
}

func (o *testObserver) RequestEnded(e HistoryEntry) {
	o.add(testEvent{eventKind(e.Resolution), e.Request})
}

func (o *testObserver) add(e testEvent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, e)
}

func (o *testObserver) Events() []testEvent {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]testEvent{}, o.events...)
}

// Returns the kinds of the events received so far, in order.
func (o *testObserver) Kinds() []eventKind {
	var kinds []eventKind
	for _, e := range o.Events() {
		kinds = append(kinds, e.Type)
	}
	return kinds
}

func TestManager_Observer_Created(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr, obs := newObservedManager(ManagerConfig{})

		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		call := startApproval(ctx, mgr, getSecretRequest())

		synctest.Wait()
		events := obs.Events()
		require.NotEmpty(t, events)
		assert.Equal(t, evCreated, events[0].Type)
		assert.NotNil(t, events[0].Request, "expected request in event")

		call.wait()
	})
}

func TestManager_Observer_Approved(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr, obs := newObservedManager(ManagerConfig{})

		call := startApproval(context.Background(), mgr, getSecretRequest())
		require.NoError(t, mgr.Approve(waitPendingID(t, mgr)))
		call.wait()

		assert.Equal(t, []eventKind{evCreated, evApproved}, obs.Kinds())
	})
}

func TestManager_Observer_Denied(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr, obs := newObservedManager(ManagerConfig{})

		call := startApproval(context.Background(), mgr, getSecretRequest())
		require.NoError(t, mgr.Deny(waitPendingID(t, mgr)))
		call.wait()

		assert.Equal(t, []eventKind{evCreated, evDenied}, obs.Kinds())
	})
}

func TestManager_Observer_Expired(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr, obs := newObservedManager(ManagerConfig{Timeout: 100 * time.Millisecond})

		_, _ = mgr.RequireApproval(context.Background(), getSecretRequest())

		synctest.Wait()
		assert.Equal(t, []eventKind{evCreated, evExpired}, obs.Kinds())
	})
}

func TestManager_Observer_CancelMethod(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr, obs := newObservedManager(ManagerConfig{})

		call := startApproval(context.Background(), mgr, getSecretRequest())
		require.NoError(t, mgr.Cancel(waitPendingID(t, mgr)))
		call.wait()

		assert.Equal(t, []eventKind{evCreated, evCancelled}, obs.Kinds())
	})
}

func TestManager_Observer_Cancelled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr, obs := newObservedManager(ManagerConfig{})

		ctx, cancel := context.WithCancel(context.Background())
		call := startApproval(ctx, mgr, getSecretRequest())

		synctest.Wait()
		require.NotEmpty(t, obs.Events(), "expected the created event")

		cancel()
		_, err := call.wait()
		assert.Equal(t, context.Canceled, err)

		assert.Equal(t, []eventKind{evCreated, evCancelled}, obs.Kinds())
	})
}

func TestManager_Observer_Unsubscribe(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr, obs := newObservedManager(ManagerConfig{})
		mgr.Unsubscribe(obs)

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		startApproval(ctx, mgr, getSecretRequest()).wait()

		assert.Empty(t, obs.Events(), "events after unsubscribe")
	})
}

func TestManager_Observer_MultipleObservers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr, obs1 := newObservedManager(ManagerConfig{})
		obs2 := &testObserver{}
		mgr.Subscribe(obs2)

		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		call := startApproval(ctx, mgr, getSecretRequest())

		synctest.Wait()
		assert.NotEmpty(t, obs1.Events(), "observer 1 should receive events")
		assert.NotEmpty(t, obs2.Events(), "observer 2 should receive events")

		call.wait()
	})
}

func TestManager_Observer_ConcurrentSubscribe(t *testing.T) {
	mgr := NewManagerForTest(ManagerConfig{})

	observers := make([]*testObserver, 10)
	var wg sync.WaitGroup
	for i := range observers {
		observers[i] = &testObserver{}
		wg.Go(func() { mgr.Subscribe(observers[i]) })
	}
	wg.Wait()

	require.NoError(t, mgr.Passthrough(getSecretRequest()))
	for i, obs := range observers {
		assert.Equal(t, []eventKind{evAutoApproved}, obs.Kinds(), "observer %d", i)
	}
}

func TestHistory_RecordsApproved(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManagerForTest(ManagerConfig{})

		call := startApproval(context.Background(), mgr, getSecretRequest())
		reqID := waitPendingID(t, mgr)
		require.NoError(t, mgr.Approve(reqID))
		call.wait()

		history := mgr.History()
		require.Len(t, history, 1)
		assert.Equal(t, ResolutionApproved, history[0].Resolution)
		assert.Equal(t, reqID, history[0].Request.ID)
	})
}

func TestHistory_RecordsDenied(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManagerForTest(ManagerConfig{})

		call := startApproval(context.Background(), mgr, getSecretRequest())
		require.NoError(t, mgr.Deny(waitPendingID(t, mgr)))
		call.wait()

		assert.Equal(t, []Resolution{ResolutionDenied}, historyResolutions(mgr))
	})
}

func TestHistory_RecordsCancelled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManagerForTest(ManagerConfig{})

		ctx, cancel := context.WithCancel(context.Background())
		call := startApproval(ctx, mgr, getSecretRequest())
		waitPending(t, mgr, 1)
		cancel()
		call.wait()

		assert.Equal(t, []Resolution{ResolutionCancelled}, historyResolutions(mgr))
	})
}

func TestHistory_LimitEnforced(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		historyMax := 5
		mgr := NewManagerForTest(ManagerConfig{Timeout: 100 * time.Millisecond, HistoryMax: historyMax})

		for i := 0; i < historyMax+3; i++ {
			_, _ = mgr.RequireApproval(context.Background(), getSecretRequest())
		}
		synctest.Wait()

		assert.Len(t, mgr.History(), historyMax)
	})
}

func TestHistory_NewestFirst(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManagerForTest(ManagerConfig{})

		for range 3 {
			call := startApproval(context.Background(), mgr, getSecretRequest())
			require.NoError(t, mgr.Approve(waitPendingID(t, mgr)))
			call.wait()

			// Small delay to ensure different timestamps
			time.Sleep(10 * time.Millisecond)
		}

		history := mgr.History()
		require.Len(t, history, 3)

		for i := 0; i < len(history)-1; i++ {
			assert.False(t, history[i].ResolvedAt.Before(history[i+1].ResolvedAt), "history entry %d resolved before entry %d, but should be after (newest first)", i, i+1)
		}
	})
}

func TestApprovalCache_SameSenderItemAutoApproved(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManagerForTest(ManagerConfig{ApprovalWindow: time.Second})

		info := getSecretRequest()
		info.SenderInfo = busSender(":1.100")

		call := startApproval(context.Background(), mgr, info)
		require.NoError(t, mgr.Approve(waitPendingID(t, mgr)))
		call.wait()

		// A second request from the same sender for the same item, within the window,
		// is approved without going pending.
		_, err := mgr.RequireApproval(context.Background(), info)
		require.NoError(t, err, "expected auto-approval from cache")
		assert.Zero(t, mgr.PendingCount(), "cached approval should not create a pending request")
	})
}

func TestApprovalCache_Expired(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManagerForTest(ManagerConfig{ApprovalWindow: 50 * time.Millisecond})

		info := getSecretRequest()
		info.SenderInfo = busSender(":1.100")

		call := startApproval(context.Background(), mgr, info)
		require.NoError(t, mgr.Approve(waitPendingID(t, mgr)))
		call.wait()

		time.Sleep(60 * time.Millisecond)

		requirePrompts(t, mgr, info)
	})
}

func TestApprovalCache_DifferentSender(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManagerForTest(ManagerConfig{ApprovalWindow: time.Second})

		info := getSecretRequest()
		info.SenderInfo = busSender(":1.100")

		call := startApproval(context.Background(), mgr, info)
		require.NoError(t, mgr.Approve(waitPendingID(t, mgr)))
		call.wait()

		info.SenderInfo = busSender(":1.999")
		requirePrompts(t, mgr, info)
	})
}

func TestApprovalCache_DeleteBypassesCache(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManagerForTest(ManagerConfig{ApprovalWindow: time.Second})

		info := getSecretRequest()
		info.SenderInfo = busSender(":1.100")

		// Approving a GetSecret request first fills the cache.
		call := startApproval(context.Background(), mgr, info)
		require.NoError(t, mgr.Approve(waitPendingID(t, mgr)))
		call.wait()

		_, err := mgr.RequireApproval(context.Background(), info)
		require.NoError(t, err, "GetSecret should be auto-approved from cache")

		// A delete request does not use the cache, so it goes pending.
		info.Type = RequestTypeDelete
		requirePrompts(t, mgr, info)
	})
}

func TestApprovalCache_WriteBypassesCache(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManagerForTest(ManagerConfig{ApprovalWindow: time.Second})

		info := getSecretRequest()
		info.SenderInfo = busSender(":1.100")

		// Approving a GetSecret request first fills the cache.
		call := startApproval(context.Background(), mgr, info)
		require.NoError(t, mgr.Approve(waitPendingID(t, mgr)))
		call.wait()

		info.Type = RequestTypeWrite
		requirePrompts(t, mgr, info)
	})
}

func TestCacheItemForSender_AutoApprovesRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManagerForTest(ManagerConfig{ApprovalWindow: time.Second})

		caller := busSender(":1.200")
		item := itemInCollection("default", "i123")

		mgr.CacheItemForSender(caller, item.Path)

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_, err := mgr.RequireApproval(ctx, RequestInfo{Items: []ItemInfo{item}, Session: "/s/1", Type: RequestTypeGetSecret, SenderInfo: caller})
		assert.NoError(t, err, "GetSecret should be auto-approved after CacheItemForSender")
	})
}

func TestCacheItemForSender_DifferentSenderNotCached(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManagerForTest(ManagerConfig{ApprovalWindow: time.Second})

		item := itemInCollection("default", "i123")
		mgr.CacheItemForSender(busSender(":1.200"), item.Path)

		requirePrompts(t, mgr, RequestInfo{Items: []ItemInfo{item}, Session: "/s/1", Type: RequestTypeGetSecret, SenderInfo: busSender(":1.300")})
	})
}

// An approval cached on one client does not cover the same unique name on
// another.
func TestCacheItemForSender_DifferentClientNotCached(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManagerForTest(ManagerConfig{ApprovalWindow: time.Second})

		item := itemInCollection("default", "i123")
		mgr.CacheItemForSender(ClientClaim{Name: "a"}.Sender(":1.200"), item.Path)

		caller := ClientClaim{Name: "b"}.Sender(":1.200")
		requirePrompts(t, mgr, RequestInfo{Items: []ItemInfo{item}, Session: "/s/1", Type: RequestTypeGetSecret, SenderInfo: caller})
	})
}

// A request that did not come over D-Bus has no sender to tie an approval to,
// so another process could reuse it.
func TestCacheItemForSender_NoSenderNotCached(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManagerForTest(ManagerConfig{ApprovalWindow: time.Second})

		item := itemInCollection("default", "i123")
		mgr.CacheItemForSender(SenderInfo{}, item.Path)

		requirePrompts(t, mgr, RequestInfo{Items: []ItemInfo{item}, Session: "/s/1", Type: RequestTypeGetSecret})
	})
}

// Returns a claim of the given client name, which no other bus holds.
func claim(t *testing.T, mgr *Manager, name ClientName) ClientClaim {
	t.Helper()
	c, ok := mgr.ClaimClient(name)
	require.True(t, ok, "%s is held by another bus", name)
	return c
}

func TestClaimClient_RefusesAHeldName(t *testing.T) {
	mgr := NewManagerForTest(ManagerConfig{})
	held := claim(t, mgr, "vm")
	_, ok := mgr.ClaimClient("vm")
	assert.False(t, ok)

	mgr.ReleaseClient(held)
	claim(t, mgr, "vm")
}

func TestReleaseClient_ApprovalsStayWithTheirClaim(t *testing.T) {
	item := itemInCollection("default", "i123")
	read := func(s SenderInfo) RequestInfo {
		return RequestInfo{Items: []ItemInfo{item}, Session: "/s/1", Type: RequestTypeGetSecret, SenderInfo: s}
	}
	for _, tc := range []struct {
		name string
		// Caches an approval for a sender of the first claim, at some point
		// around its release, and returns the second claim.
		approve func(t *testing.T, mgr *Manager, first ClientClaim) ClientClaim
	}{
		{"cached before the release", func(t *testing.T, mgr *Manager, first ClientClaim) ClientClaim {
			mgr.CacheItemForSender(first.Sender(":1.5"), item.Path)
			_, err := mgr.RequireApproval(t.Context(), read(first.Sender(":1.5")))
			require.NoError(t, err, "the approval does not cover its own bus's sender")
			mgr.ReleaseClient(first)
			return claim(t, mgr, "vm")
		}},
		{"cached after the release", func(t *testing.T, mgr *Manager, first ClientClaim) ClientClaim {
			mgr.ReleaseClient(first)
			mgr.CacheItemForSender(first.Sender(":1.5"), item.Path)
			return claim(t, mgr, "vm")
		}},
		{"cached after the next claim", func(t *testing.T, mgr *Manager, first ClientClaim) ClientClaim {
			mgr.ReleaseClient(first)
			next := claim(t, mgr, "vm")
			mgr.CacheItemForSender(first.Sender(":1.5"), item.Path)
			return next
		}},
		{"pending request approved after the next claim", func(t *testing.T, mgr *Manager, first ClientClaim) ClientClaim {
			id := mgr.Create(read(first.Sender(":1.5")))
			mgr.ReleaseClient(first)
			next := claim(t, mgr, "vm")
			require.NoError(t, mgr.Approve(id))
			return next
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				mgr := NewManagerForTest(ManagerConfig{ApprovalWindow: time.Minute})
				next := tc.approve(t, mgr, claim(t, mgr, "vm"))
				requirePrompts(t, mgr, read(next.Sender(":1.5")))
			})
		})
	}
}

func TestApprovalCache_DifferentItem(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManagerForTest(ManagerConfig{ApprovalWindow: time.Second})

		info := getSecretRequest()
		info.SenderInfo = busSender(":1.100")

		call := startApproval(context.Background(), mgr, info)
		require.NoError(t, mgr.Approve(waitPendingID(t, mgr)))
		call.wait()

		info.Items = []ItemInfo{{Path: "/test/item2"}}
		requirePrompts(t, mgr, info)
	})
}

func TestManager_EventOrder_CancelledContext(t *testing.T) {
	// The created event must reach observers before the cancelled one, even
	// when the context is already cancelled.
	for i := range 100 {
		mgr, obs := newObservedManager(ManagerConfig{})

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // pre-cancel so select fires ctx.Done() immediately

		_, _ = mgr.RequireApproval(ctx, getSecretRequest())

		require.Equal(t, []eventKind{evCreated, evCancelled}, obs.Kinds(), "iteration %d", i)
	}
}

func TestDropsChromeDummyWrite(t *testing.T) {
	chromeItems := []ItemInfo{{
		Path:       "/org/freedesktop/secrets/collection/default/123",
		Label:      "Chrome Safe Storage",
		Attributes: map[string]string{"xdg:schema": chromeDummySchema},
	}}
	normalItems := []ItemInfo{{
		Path:       "/org/freedesktop/secrets/collection/default/456",
		Label:      "My Secret",
		Attributes: map[string]string{"xdg:schema": "org.gnome.keyring.Note"},
	}}

	t.Run("enabled + write + matching schema", func(t *testing.T) {
		mgr := NewManagerForTest(ManagerConfig{IgnoreChromeDummy: true})
		assert.True(t, mgr.dropsChromeDummyWrite(RequestInfo{Items: chromeItems, Type: RequestTypeWrite}))
	})
	t.Run("enabled + read + matching schema", func(t *testing.T) {
		mgr := NewManagerForTest(ManagerConfig{IgnoreChromeDummy: true})
		assert.False(t, mgr.dropsChromeDummyWrite(RequestInfo{Items: chromeItems, Type: RequestTypeGetSecret}), "expected false for non-write")
	})
	t.Run("enabled + write + different schema", func(t *testing.T) {
		mgr := NewManagerForTest(ManagerConfig{IgnoreChromeDummy: true})
		assert.False(t, mgr.dropsChromeDummyWrite(RequestInfo{Items: normalItems, Type: RequestTypeWrite}), "expected false for non-chrome schema")
	})
	t.Run("disabled + write + matching schema", func(t *testing.T) {
		mgr := NewManagerForTest(ManagerConfig{IgnoreChromeDummy: false})
		assert.False(t, mgr.dropsChromeDummyWrite(RequestInfo{Items: chromeItems, Type: RequestTypeWrite}), "expected false when disabled")
	})
}

func TestRequireApproval_ChromeDummyWriteIgnored(t *testing.T) {
	mgr, obs := newObservedManager(ManagerConfig{IgnoreChromeDummy: true})

	items := []ItemInfo{{
		Path:       "/org/freedesktop/secrets/collection/default/123",
		Label:      "Chrome Safe Storage",
		Attributes: map[string]string{"xdg:schema": chromeDummySchema},
	}}

	prompted, err := mgr.RequireApproval(t.Context(), RequestInfo{Items: items, Session: "/session/1", Type: RequestTypeWrite, SenderInfo: SenderInfo{Client: "test-client"}})
	require.ErrorIs(t, err, ErrIgnored)
	assert.False(t, prompted)

	assert.Equal(t, []eventKind{evIgnored}, obs.Kinds())

	history := mgr.History()
	require.Len(t, history, 1)
	assert.Equal(t, ResolutionIgnored, history[0].Resolution)
	assert.Equal(t, ClientName("test-client"), history[0].Request.SenderInfo.Client)

	assert.Zero(t, mgr.PendingCount(), "ignored request should not be pending")
}

// Non-commit objects that git signs.
var (
	testTagObject  = SignedObject("object def\ntype commit\ntag v1\ntagger A <a@x> 1 +0000\n\nv1\n")
	testPushObject = SignedObject("certificate version 0.1\npusher A <a@x> 1 +0000\npushee https://x/r.git\n\n0 1 refs/heads/main\n")
)

func TestCheckTrustedSigner(t *testing.T) {
	const nvimExe = "/usr/bin/nvim"
	mgr := NewManagerForTest(ManagerConfig{
		TrustedSigners: []TrustedSigner{
			{ExePath: nvimExe, RepoName: "my-repo"},
		},
	})
	// A legit commit initiated from nvim, arriving via our thin client. The chain
	// as the daemon stores it has our own binary filtered out, so it starts at git
	// (the direct invoker); the trusted app, nvim, is an ancestor. PeerTrusted is
	// set because the connection was opened by our thin client.
	viaHelper := SenderInfo{
		PeerTrusted: true,
		ProcessChain: []procutil.Process{
			{Name: "git", Exe: new(procutil.Path("/usr/bin/git"))},
			{Name: "nvim", Exe: new(procutil.Path(nvimExe))},
			{Name: "kgx", Exe: new(procutil.Path("/usr/bin/kgx"))},
		},
	}

	t.Run("trusted ancestor via helper", func(t *testing.T) {
		assert.True(t, mgr.CheckTrustedSigner(CommitSignRequestForTest(viaHelper, "my-repo")))
	})

	t.Run("wrong repo", func(t *testing.T) {
		assert.False(t, mgr.CheckTrustedSigner(CommitSignRequestForTest(viaHelper, "other-repo")))
	})

	t.Run("empty chain", func(t *testing.T) {
		assert.False(t, mgr.CheckTrustedSigner(CommitSignRequestForTest(SenderInfo{PeerTrusted: true}, "my-repo")))
	})

	t.Run("empty repo matches any", func(t *testing.T) {
		m := NewManagerForTest(ManagerConfig{
			TrustedSigners: []TrustedSigner{{ExePath: nvimExe}},
		})
		assert.True(t, m.CheckTrustedSigner(CommitSignRequestForTest(viaHelper, "any-repo")))
	})

	t.Run("file prefix match and reject", func(t *testing.T) {
		m := NewManagerForTest(ManagerConfig{
			TrustedSigners: []TrustedSigner{{ExePath: nvimExe, FilePrefix: "secret-service/"}},
		})
		assert.True(t, m.CheckTrustedSigner(CommitSignRequestForTest(viaHelper, "any", "secret-service/default/i1.age", "secret-service/login/i2.age")))
		assert.False(t, m.CheckTrustedSigner(CommitSignRequestForTest(viaHelper, "any", "secret-service/default/i1.age", "other/sneaky.gpg")))
	})

	t.Run("file prefix matches only commits", func(t *testing.T) {
		m := NewManagerForTest(ManagerConfig{
			TrustedSigners: []TrustedSigner{{ExePath: nvimExe, FilePrefix: "secret-service/"}},
		})
		assert.False(t, m.CheckTrustedSigner(SignInfoForTest("any", testTagObject, "secret-service/x").RequestInfo(viaHelper)))
		assert.False(t, m.CheckTrustedSigner(SignInfoForTest("any", testPushObject, "secret-service/x").RequestInfo(viaHelper)))
	})

	t.Run("no file prefix matches a tag", func(t *testing.T) {
		assert.True(t, mgr.CheckTrustedSigner(SignInfoForTest("my-repo", testTagObject).RequestInfo(viaHelper)))
	})
}

// A process that sets its comm to a unit name must not match a `unit` rule,
// which compares the caller's real systemd unit (ADR 0005).
func TestProcessUnitMatchesSystemdUnitNotComm(t *testing.T) {
	mgr := NewManagerForTest(ManagerConfig{TrustRules: []TrustRule{{
		Name:    "approve-firefox-unit",
		Action:  RuleApprove,
		Process: &ProcessMatcher{Unit: MustGlob("firefox.service")},
	}}})
	items := []ItemInfo{{Path: "/org/freedesktop/secrets/collection/default/1"}}

	// Genuine caller: comm is "firefox", real systemd unit is "firefox.service".
	genuine := SenderInfo{Client: ClientLocal, InvokerName: "firefox", SystemdUnit: "firefox.service"}
	_, ok := mgr.CheckTrustRules(RequestInfo{Items: items, Type: RequestTypeGetSecret, SenderInfo: genuine})
	assert.True(t, ok, "unit rule should match the real systemd unit")

	// Spoofer: sets its comm to "firefox.service" but has no such systemd unit.
	spoofed := SenderInfo{Client: ClientLocal, InvokerName: "firefox.service", SystemdUnit: ""}
	_, ok = mgr.CheckTrustRules(RequestInfo{Items: items, Type: RequestTypeGetSecret, SenderInfo: spoofed})
	assert.False(t, ok, "unit rule must not match a comm spoofed to look like a unit name")
}

// Malware that speaks the socket protocol directly, with a trusted signer among
// its ancestors, must still get a prompt (ADR 0008).
func TestCheckTrustedSigner_RejectsUntrustedPeer(t *testing.T) {
	const nvimExe = "/usr/bin/nvim"
	mgr := NewManagerForTest(ManagerConfig{
		TrustedSigners: []TrustedSigner{{ExePath: nvimExe}},
	})

	// Same ancestry a genuine nvim commit would have, but the peer is malware
	// speaking the protocol directly, not our thin client.
	forged := SenderInfo{
		PeerTrusted: false,
		ProcessChain: []procutil.Process{
			{Name: "malware", Exe: new(procutil.Path("/tmp/malware"))},
			{Name: "git", Exe: new(procutil.Path("/usr/bin/git"))},
			{Name: "nvim", Exe: new(procutil.Path(nvimExe))},
		},
	}
	assert.False(t, mgr.CheckTrustedSigner(CommitSignRequestForTest(forged, "any")),
		"a direct protocol speaker must not silently sign even with a trusted ancestor")

	// Setting only PeerTrusted lets the trusted ancestor match again. So the
	// gate, not the ancestry, blocked the forged request.
	trusted := forged
	trusted.PeerTrusted = true
	assert.True(t, mgr.CheckTrustedSigner(CommitSignRequestForTest(trusted, "any")))
}

func TestCheckTrustRules(t *testing.T) {
	rules := []TrustRule{
		{
			Name:         "approve-gh-search",
			Action:       "approve",
			RequestTypes: []RequestType{"search"},
			Process:      &ProcessMatcher{Name: MustGlob("gh")},
		},
		{
			Name:         "ignore-chrome-write",
			Action:       "ignore",
			RequestTypes: []RequestType{"write"},
			Process:      &ProcessMatcher{Exe: MustGlob("/opt/google/chrome/chrome")},
			Secret:       &SecretMatcher{Collection: MustGlob("default")},
		},
		{
			Name:    "approve-all-from-unit",
			Action:  "approve",
			Process: &ProcessMatcher{Unit: MustGlob("gopass-*")},
		},
		{
			Name:   "approve-by-label",
			Action: "approve",
			Secret: &SecretMatcher{Label: MustGlob("GitHub*")},
		},
		{
			Name:             "approve-search-attrs",
			Action:           "approve",
			RequestTypes:     []RequestType{"search"},
			SearchAttributes: map[string]Glob{"xdg:schema": MustGlob("org.gnome.keyring.Note")},
		},
		{
			Name:   "approve-by-attributes",
			Action: "approve",
			Secret: &SecretMatcher{
				Attributes: map[string]Glob{"xdg:schema": MustGlob("org.gnome.keyring.NetworkPassword")},
			},
		},
	}

	mgr := NewManagerForTest(ManagerConfig{
		TrustRules: rules,
	})

	ghChain := []procutil.Process{{Name: "gh", PID: 100, Exe: new(procutil.Path("/usr/bin/gh"))}}
	chromeChain := []procutil.Process{{Name: "chrome", PID: 200, Exe: new(procutil.Path("/opt/google/chrome/chrome"))}}
	gopassSender := SenderInfo{Client: ClientLocal, InvokerName: "gopass", SystemdUnit: "gopass-secret-service.service", ProcessChain: []procutil.Process{{Name: "gopass", PID: 300}}}

	t.Run("match process name + request type", func(t *testing.T) {
		rule, ok := mgr.CheckTrustRules(RequestInfo{
			Type:       RequestTypeSearch,
			SenderInfo: SenderInfo{Client: ClientLocal, ProcessChain: ghChain},
		})
		require.True(t, ok, "no rule matched")
		assert.Equal(t, "approve-gh-search", rule.Name)
	})

	t.Run("no match wrong request type", func(t *testing.T) {
		rule, ok := mgr.CheckTrustRules(RequestInfo{
			Type:       RequestTypeGetSecret,
			SenderInfo: SenderInfo{Client: ClientLocal, ProcessChain: ghChain},
		})
		// No rule matches. Rule 0 covers only search, and with no items neither
		// the label rule nor the attribute rule can match.
		assert.False(t, ok, "expected no match, got %v", rule.Name)
	})

	t.Run("match exe + collection for ignore", func(t *testing.T) {
		items := []ItemInfo{{
			Path:  "/org/freedesktop/secrets/collection/default/123",
			Label: "Chrome Safe Storage",
		}}
		rule, ok := mgr.CheckTrustRules(RequestInfo{
			Items:      items,
			Type:       RequestTypeWrite,
			SenderInfo: SenderInfo{Client: ClientLocal, ProcessChain: chromeChain},
		})
		require.True(t, ok, "no rule matched")
		assert.Equal(t, "ignore-chrome-write", rule.Name)
	})

	t.Run("no match wrong collection", func(t *testing.T) {
		items := []ItemInfo{{
			Path:  "/org/freedesktop/secrets/collection/login/123",
			Label: "Chrome Safe Storage",
		}}
		rule, ok := mgr.CheckTrustRules(RequestInfo{
			Items:      items,
			Type:       RequestTypeWrite,
			SenderInfo: SenderInfo{Client: ClientLocal, ProcessChain: chromeChain},
		})
		// Rule 1 requires the default collection, so a write to the login
		// collection matches no rule.
		assert.False(t, ok, "expected no match, got %v", rule.Name)
	})

	t.Run("match unit glob", func(t *testing.T) {
		rule, ok := mgr.CheckTrustRules(RequestInfo{
			Items:      []ItemInfo{{Path: "/org/freedesktop/secrets/collection/default/1"}},
			Type:       RequestTypeGetSecret,
			SenderInfo: gopassSender,
		})
		require.True(t, ok, "no rule matched")
		assert.Equal(t, "approve-all-from-unit", rule.Name)
	})

	t.Run("match label glob", func(t *testing.T) {
		items := []ItemInfo{{
			Path:  "/org/freedesktop/secrets/collection/default/1",
			Label: "GitHub Token",
		}}
		rule, ok := mgr.CheckTrustRules(RequestInfo{
			Items:      items,
			Type:       RequestTypeGetSecret,
			SenderInfo: SenderInfo{Client: ClientLocal, ProcessChain: []procutil.Process{{Name: "random-app", PID: 999}}},
		})
		require.True(t, ok, "no rule matched")
		assert.Equal(t, "approve-by-label", rule.Name)
	})

	t.Run("match search attributes", func(t *testing.T) {
		rule, ok := mgr.CheckTrustRules(RequestInfo{
			Type:       RequestTypeSearch,
			Payload:    SearchPayload{Attributes: map[string]string{"xdg:schema": "org.gnome.keyring.Note"}},
			SenderInfo: SenderInfo{Client: ClientLocal, ProcessChain: []procutil.Process{{Name: "app", PID: 1}}},
		})
		require.True(t, ok, "no rule matched")
		assert.Equal(t, "approve-search-attrs", rule.Name)
	})

	t.Run("match item attributes", func(t *testing.T) {
		items := []ItemInfo{{
			Path:       "/org/freedesktop/secrets/collection/default/1",
			Label:      "WiFi Password",
			Attributes: map[string]string{"xdg:schema": "org.gnome.keyring.NetworkPassword", "ssid": "home"},
		}}
		rule, ok := mgr.CheckTrustRules(RequestInfo{
			Items:      items,
			Type:       RequestTypeGetSecret,
			SenderInfo: SenderInfo{Client: ClientLocal, ProcessChain: []procutil.Process{{Name: "nm-applet", PID: 500}}},
		})
		require.True(t, ok, "no rule matched")
		assert.Equal(t, "approve-by-attributes", rule.Name)
	})

	t.Run("match item attributes with glob", func(t *testing.T) {
		m := NewManagerForTest(ManagerConfig{
			TrustRules: []TrustRule{{
				Name:   "approve-glob-attrs",
				Action: "approve",
				Secret: &SecretMatcher{
					Attributes: map[string]Glob{"username": MustGlob("kubelogin/tokencache/*")},
				},
			}},
		})
		items := []ItemInfo{{
			Path:       "/org/freedesktop/secrets/collection/default/1",
			Label:      "token",
			Attributes: map[string]string{"username": "kubelogin/tokencache/e91d908"},
		}}
		rule, ok := m.CheckTrustRules(RequestInfo{
			Items:      items,
			Type:       RequestTypeGetSecret,
			SenderInfo: SenderInfo{Client: ClientLocal, ProcessChain: []procutil.Process{{Name: "kubectl", PID: 1}}},
		})
		require.True(t, ok, "no rule matched")
		assert.Equal(t, "approve-glob-attrs", rule.Name)
		// Non-matching value
		items[0].Attributes["username"] = "other/path"
		rule, ok = m.CheckTrustRules(RequestInfo{
			Items:      items,
			Type:       RequestTypeGetSecret,
			SenderInfo: SenderInfo{Client: ClientLocal, ProcessChain: []procutil.Process{{Name: "kubectl", PID: 1}}},
		})
		assert.False(t, ok, "expected no match for non-matching glob, got %v", rule)
	})

	t.Run("first match wins", func(t *testing.T) {
		// gh doing search matches rule 0 (approve-gh-search), not rule 4 (approve-search-attrs)
		rule, ok := mgr.CheckTrustRules(RequestInfo{
			Type:       RequestTypeSearch,
			Payload:    SearchPayload{Attributes: map[string]string{"xdg:schema": "org.gnome.keyring.Note"}},
			SenderInfo: SenderInfo{Client: ClientLocal, ProcessChain: ghChain},
		})
		require.True(t, ok, "no rule matched")
		assert.Equal(t, "approve-gh-search", rule.Name)
	})

	t.Run("empty rules", func(t *testing.T) {
		m := NewManagerForTest(ManagerConfig{})
		rule, ok := m.CheckTrustRules(RequestInfo{Type: RequestTypeGetSecret})
		assert.False(t, ok, "expected no match from empty rules, got %v", rule)
	})
}

func TestMatchProcessArgs(t *testing.T) {
	// An interpreter runs the script, so exe is the interpreter and only argv
	// shows the script path. The args matcher exists for this case.
	sender := SenderInfo{
		ProcessChain: []procutil.Process{
			{Name: "secret-tool", PID: 100, Exe: new(procutil.Path("/usr/bin/secret-tool")), Args: []string{"secret-tool", "lookup", "service", "http-basic-auth"}},
			{Name: "bash", PID: 99, Exe: new(procutil.Path("/usr/bin/bash")), Args: []string{"bash", "/home/me/.local/bin/logcli", "query"}},
			{Name: "fish", PID: 98, Exe: new(procutil.Path("/usr/bin/fish")), Args: []string{"fish"}},
		},
	}

	t.Run("matches script path in interpreter argv", func(t *testing.T) {
		pm := &ProcessMatcher{Exe: MustGlob("/usr/bin/bash"), Args: MustGlob("/home/me/.local/bin/logcli")}
		assert.True(t, pm.matches(sender))
	})

	t.Run("glob within final path segment", func(t *testing.T) {
		pm := &ProcessMatcher{Args: MustGlob("/home/me/.local/bin/log*")}
		assert.True(t, pm.matches(sender))
	})

	t.Run("glob does not cross path separators", func(t *testing.T) {
		pm := &ProcessMatcher{Args: MustGlob("*/logcli")}
		assert.False(t, pm.matches(sender))
	})

	t.Run("no match on absent arg", func(t *testing.T) {
		pm := &ProcessMatcher{Args: MustGlob("/home/me/.local/bin/other")}
		assert.False(t, pm.matches(sender))
	})

	t.Run("matches only whole args, not substrings", func(t *testing.T) {
		pm := &ProcessMatcher{Args: MustGlob("logcli")}
		assert.False(t, pm.matches(sender))
	})

	t.Run("all fields must match, each anywhere in chain", func(t *testing.T) {
		pm := &ProcessMatcher{Exe: MustGlob("/usr/bin/zsh"), Args: MustGlob("/home/me/.local/bin/logcli")}
		assert.False(t, pm.matches(sender), "exe not in chain")

		pm = &ProcessMatcher{Exe: MustGlob("/usr/bin/secret-tool"), Args: MustGlob("/home/me/.local/bin/logcli")}
		assert.True(t, pm.matches(sender), "exe and args may match different chain entries")
	})

	t.Run("no match on empty chain", func(t *testing.T) {
		pm := &ProcessMatcher{Args: MustGlob("*")}
		assert.False(t, pm.matches(SenderInfo{}))
	})
}

func TestSecretMatcherMissingAttributeReadsAsEmpty(t *testing.T) {
	sm := &SecretMatcher{Attributes: map[string]Glob{"service": MustGlob("*")}}
	assert.True(t, sm.matchesItem(ItemInfo{Attributes: map[string]string{"user": "me"}}))
	assert.True(t, sm.matchesItem(ItemInfo{}))

	sm = &SecretMatcher{Attributes: map[string]Glob{"service": MustGlob("?*")}}
	assert.False(t, sm.matchesItem(ItemInfo{Attributes: map[string]string{"user": "me"}}))
}

func TestTrustRules_RequireApproval_Approve(t *testing.T) {
	mgr, obs := newObservedManager(ManagerConfig{
		TrustRules: []TrustRule{
			{
				Name:         "approve-gh",
				Action:       "approve",
				RequestTypes: []RequestType{"search"},
				Process:      &ProcessMatcher{Name: MustGlob("gh")},
			},
		},
	})

	_, err := mgr.RequireApproval(context.Background(), RequestInfo{
		Session:    "/s/1",
		Type:       RequestTypeSearch,
		SenderInfo: SenderInfo{Client: ClientLocal, ProcessChain: []procutil.Process{{Name: "gh", PID: 1}}},
	})
	require.NoError(t, err, "expected auto-approved")
	assert.Zero(t, mgr.PendingCount(), "trust rule approve should not create pending request")

	assert.Equal(t, []eventKind{evAutoApproved}, obs.Kinds())
	assert.Equal(t, []Resolution{ResolutionAutoApproved}, historyResolutions(mgr))
}

func TestTrustRules_RequireApproval_Ignore(t *testing.T) {
	mgr, obs := newObservedManager(ManagerConfig{
		TrustRules: []TrustRule{
			{
				Name:         "ignore-chrome",
				Action:       "ignore",
				RequestTypes: []RequestType{"write"},
				Secret:       &SecretMatcher{Attributes: map[string]Glob{"xdg:schema": MustGlob(chromeDummySchema)}},
			},
		},
	})

	items := []ItemInfo{{
		Path:       "/org/freedesktop/secrets/collection/default/1",
		Attributes: map[string]string{"xdg:schema": chromeDummySchema},
	}}
	_, err := mgr.RequireApproval(context.Background(), RequestInfo{
		Items:      items,
		Session:    "/s/1",
		Type:       RequestTypeWrite,
		SenderInfo: SenderInfo{Client: ClientLocal},
	})
	require.ErrorIs(t, err, ErrIgnored)
	assert.Zero(t, mgr.PendingCount(), "trust rule ignore should not create pending request")

	assert.Equal(t, []eventKind{evIgnored}, obs.Kinds())
	assert.Equal(t, []Resolution{ResolutionIgnored}, historyResolutions(mgr))
}

func TestTrustRules_RequireApproval_Deny(t *testing.T) {
	mgr, obs := newObservedManager(ManagerConfig{
		TrustRules: []TrustRule{
			{
				Name:    "deny-epiphany",
				Action:  "deny",
				Process: &ProcessMatcher{Name: MustGlob("epiphany-search")},
			},
		},
	})

	_, err := mgr.RequireApproval(context.Background(), RequestInfo{
		Items:      []ItemInfo{{Path: "/org/freedesktop/secrets/collection/default/1"}},
		Session:    "/s/1",
		Type:       RequestTypeGetSecret,
		SenderInfo: SenderInfo{Client: ClientLocal, ProcessChain: []procutil.Process{{Name: "epiphany-search", PID: 1}}},
	})
	require.ErrorIs(t, err, ErrDeniedByRule)
	assert.Zero(t, mgr.PendingCount(), "trust rule deny should not create pending request")

	assert.Equal(t, []eventKind{evDenied}, obs.Kinds())
	assert.Equal(t, []Resolution{ResolutionDenied}, historyResolutions(mgr))
}

func TestTrustRules_RequireApproval_NoMatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManagerForTest(ManagerConfig{
			Timeout: 100 * time.Millisecond,
			TrustRules: []TrustRule{
				{
					Name:         "approve-gh",
					RequestTypes: []RequestType{"search"},
					Process:      &ProcessMatcher{Name: MustGlob("gh")},
				},
			},
		})

		// A request that no rule matches goes pending and times out.
		_, err := mgr.RequireApproval(context.Background(), RequestInfo{
			Items:      []ItemInfo{{Path: "/test/item"}},
			Session:    "/s/1",
			Type:       RequestTypeGetSecret,
			SenderInfo: SenderInfo{ProcessChain: []procutil.Process{{Name: "curl", PID: 1}}},
		})
		assert.Equal(t, ErrTimeout, err, "no trust rule should match")
	})
}

func TestTrustRules_DefaultActionIsApprove(t *testing.T) {
	mgr := NewManagerForTest(ManagerConfig{
		TrustRules: []TrustRule{
			{
				Name:    "default-action-rule",
				Process: &ProcessMatcher{Name: MustGlob("test")},
				// An empty action means approve.
			},
		},
	})

	_, err := mgr.RequireApproval(context.Background(), RequestInfo{
		Session:    "/s/1",
		Type:       RequestTypeGetSecret,
		SenderInfo: SenderInfo{Client: ClientLocal, ProcessChain: []procutil.Process{{Name: "test", PID: 1}}},
	})
	assert.NoError(t, err, "expected default action approve")
}

func TestTrustRules_CWDMatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManagerForTest(ManagerConfig{
			Timeout: 100 * time.Millisecond,
			TrustRules: []TrustRule{
				{
					Name:         "vscode-cwd",
					RequestTypes: []RequestType{"get_secret"},
					Process: &ProcessMatcher{
						CWD: MustGlob("/usr/lib/visual-studio-code-electron"),
					},
				},
			},
		})

		// A matching working directory in the process chain approves the request.
		_, err := mgr.RequireApproval(context.Background(), RequestInfo{
			Items:   []ItemInfo{itemInCollection("default", "x")},
			Session: "/s/1",
			Type:    RequestTypeGetSecret,
			SenderInfo: SenderInfo{
				Client: ClientLocal,
				ProcessChain: []procutil.Process{
					{Name: "electron", PID: 100, Exe: new(procutil.Path("/usr/lib/electron39/electron")), CWD: new(procutil.Path("/usr/lib/visual-studio-code-electron"))},
					{Name: "systemd", PID: 1},
				},
			},
		})
		require.NoError(t, err, "expected CWD match")

		// A working directory that does not match leaves the request to time out.
		_, err = mgr.RequireApproval(context.Background(), RequestInfo{
			Items:   []ItemInfo{itemInCollection("default", "x")},
			Session: "/s/1",
			Type:    RequestTypeGetSecret,
			SenderInfo: SenderInfo{
				Client: ClientLocal,
				ProcessChain: []procutil.Process{
					{Name: "electron", PID: 100, Exe: new(procutil.Path("/usr/lib/electron39/electron")), CWD: new(procutil.Path("/opt/something-else"))},
				},
			},
		})
		assert.Equal(t, ErrTimeout, err, "expected CWD mismatch")
	})
}

func TestRequireApproval_AutoApproved_Disabled(t *testing.T) {
	mgr := NewDisabledManager()
	prompted, err := mgr.RequireApproval(context.Background(), RequestInfo{Type: RequestTypeGetSecret})
	require.NoError(t, err)
	assert.False(t, prompted, "disabled manager should return prompted=false")
}

func TestRequireApproval_AutoApproved_Cache(t *testing.T) {
	mgr := NewManagerForTest(ManagerConfig{ApprovalWindow: time.Second})
	caller := busSender(":1.1")
	items := []ItemInfo{{Path: "/test/item"}}

	// Manually cache
	mgr.CacheItemForSender(caller, items[0].Path)

	prompted, err := mgr.RequireApproval(context.Background(), RequestInfo{Items: items, Type: RequestTypeGetSecret, SenderInfo: caller})
	require.NoError(t, err)
	assert.False(t, prompted, "cache hit should return prompted=false")
}

func TestRequireApproval_AutoApproved_TrustRule(t *testing.T) {
	mgr := NewManagerForTest(ManagerConfig{
		TrustRules: []TrustRule{{
			Name:         "test",
			RequestTypes: []RequestType{"get_secret"},
			Process:      &ProcessMatcher{Name: MustGlob("gh")},
		}},
	})

	prompted, err := mgr.RequireApproval(context.Background(), RequestInfo{
		Items:      []ItemInfo{{Path: "/test/item"}},
		Type:       RequestTypeGetSecret,
		SenderInfo: SenderInfo{Client: ClientLocal, ProcessChain: []procutil.Process{{Name: "gh", PID: 1}}},
	})
	require.NoError(t, err)
	assert.False(t, prompted, "trust rule match should return prompted=false")
}

func TestRequireApproval_AutoApproved_AutoApproveRule(t *testing.T) {
	mgr := NewManagerForTest(ManagerConfig{})
	mustAddRule(t, mgr, &Request{
		ID:         "r1",
		Type:       RequestTypeGetSecret,
		Items:      []ItemInfo{{Path: "/org/freedesktop/secrets/collection/default/i1"}},
		SenderInfo: ghSender,
	})

	prompted, err := mgr.RequireApproval(context.Background(), RequestInfo{
		Items:      []ItemInfo{{Path: "/org/freedesktop/secrets/collection/default/i2"}},
		Type:       RequestTypeGetSecret,
		SenderInfo: ghSender,
	})
	require.NoError(t, err)
	assert.False(t, prompted, "auto-approve rule match should return prompted=false")
}

func TestRequireApproval_NotAutoApproved_ManualApproval(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManagerForTest(ManagerConfig{})

		call := startApproval(context.Background(), mgr, getSecretRequest())
		require.NoError(t, mgr.Approve(waitPendingID(t, mgr)))

		prompted, err := call.wait()
		require.NoError(t, err)
		assert.True(t, prompted, "manual approval should return prompted=true")
	})
}

func TestPassthrough(t *testing.T) {
	mgr, obs := newObservedManager(ManagerConfig{})

	items := []ItemInfo{{Label: "xdg:schema=org.gnome.keyring.Note"}}
	searchAttrs := map[string]string{"xdg:schema": "org.gnome.keyring.Note"}
	caller := ClientClaim{Name: "test-client"}.Sender(":1.42")
	caller.InvokerName = "app.service"
	caller.PID = 123

	require.NoError(t, mgr.Passthrough(RequestInfo{Items: items, Type: RequestTypeSearch, Payload: SearchPayload{Attributes: searchAttrs}, SenderInfo: caller}))

	events := obs.Events()
	require.Len(t, events, 1)
	assert.Equal(t, evAutoApproved, events[0].Type)
	assert.Equal(t, RequestTypeSearch, events[0].Request.Type)

	history := mgr.History()
	require.Len(t, history, 1)
	assert.Equal(t, ResolutionAutoApproved, history[0].Resolution)
	assert.Equal(t, ClientName("test-client"), history[0].Request.SenderInfo.Client)
	assert.Equal(t, caller.Sender, history[0].Request.SenderInfo.Sender)
	search, _ := history[0].Request.Search()
	assert.Equal(t, "org.gnome.keyring.Note", search.Attributes["xdg:schema"], "search attributes preserved")

	assert.Zero(t, mgr.PendingCount(), "passthrough request should not be pending")

	// Test with unlock type
	mgr2 := NewManagerForTest(ManagerConfig{})

	unlockItems := []ItemInfo{{Path: "/org/freedesktop/secrets/collection/login", Label: "Login"}}
	require.NoError(t, mgr2.Passthrough(RequestInfo{Items: unlockItems, Type: RequestTypeUnlock, SenderInfo: caller}))

	history2 := mgr2.History()
	require.Len(t, history2, 1)
	assert.Equal(t, RequestTypeUnlock, history2[0].Request.Type)
	assert.Equal(t, "/org/freedesktop/secrets/collection/login", string(history2[0].Request.Items[0].Path), "item path preserved")
}

func TestPassthrough_DenyRule(t *testing.T) {
	mgr := NewManagerForTest(ManagerConfig{TrustRules: []TrustRule{
		{Name: "no-unlock", Action: RuleDeny, RequestTypes: []RequestType{RequestTypeUnlock}},
		{Name: "approve-search", Action: RuleApprove, RequestTypes: []RequestType{RequestTypeSearch}},
	}})

	err := mgr.Passthrough(RequestInfo{Type: RequestTypeUnlock, SenderInfo: SenderInfo{Client: ClientLocal}})
	require.ErrorIs(t, err, ErrDeniedByRule)
	assert.EqualError(t, err, "access denied by trust rule: no-unlock")
	assert.Equal(t, ResolutionDenied, mgr.History()[0].Resolution)

	require.NoError(t, mgr.Passthrough(RequestInfo{Type: RequestTypeSearch, SenderInfo: SenderInfo{Client: ClientLocal}}))
	assert.Equal(t, ResolutionAutoApproved, mgr.History()[0].Resolution)
}

func TestRequireApproval_NotAutoApproved_Denied(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManagerForTest(ManagerConfig{})

		call := startApproval(context.Background(), mgr, getSecretRequest())
		require.NoError(t, mgr.Deny(waitPendingID(t, mgr)))

		prompted, err := call.wait()
		require.Equal(t, ErrDenied, err)
		assert.False(t, prompted, "denied request should return prompted=false")
	})
}

func TestResolution_Approves(t *testing.T) {
	tests := []struct {
		resolution Resolution
		want       bool
	}{
		{ResolutionApproved, true},
		{ResolutionAutoApproved, true},
		{ResolutionDenied, false},
		{ResolutionExpired, false},
		{ResolutionCancelled, false},
		{ResolutionIgnored, false},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, tt.resolution.Approves(), tt.resolution)
	}
}

func TestCheckTrustRules_Client(t *testing.T) {
	mgr := NewManagerForTest(ManagerConfig{
		TrustRules: []TrustRule{
			{Name: "symbiosis", Action: RuleApprove, RequestTypes: []RequestType{RequestTypeGetSecret}, Client: MustGlob("symbiosis")},
			{Name: "dev-search", Action: RuleApprove, RequestTypes: []RequestType{RequestTypeSearch}, Client: MustGlob("dev-*")},
			{Name: "no-client", Action: RuleApprove, RequestTypes: []RequestType{RequestTypeDelete}},
			{Name: "no-client-deny", Action: RuleDeny, RequestTypes: []RequestType{RequestTypeWrite}},
			{Name: "any-client", Action: RuleApprove, RequestTypes: []RequestType{RequestTypeUnlock}, Client: MustGlob("*")},
		},
	})

	cases := []struct {
		client ClientName
		typ    RequestType
		want   string
	}{
		{"symbiosis", RequestTypeGetSecret, "symbiosis"},
		{ClientLocal, RequestTypeGetSecret, ""},
		{"symbiosis-2", RequestTypeGetSecret, ""},
		{"dev-proto", RequestTypeSearch, "dev-search"},
		{ClientLocal, RequestTypeSearch, ""},
		{ClientLocal, RequestTypeDelete, "no-client"},
		{"symbiosis", RequestTypeDelete, ""},
		{ClientLocal, RequestTypeWrite, "no-client-deny"},
		{"symbiosis", RequestTypeWrite, "no-client-deny"},
		{ClientLocal, RequestTypeUnlock, "any-client"},
		{"symbiosis", RequestTypeUnlock, "any-client"},
	}
	for _, c := range cases {
		rule, ok := mgr.CheckTrustRules(RequestInfo{Type: c.typ, SenderInfo: SenderInfo{Client: c.client}})
		got := ""
		if ok {
			got = rule.Name
		}
		assert.Equal(t, c.want, got, "client %q, type %q", c.client, c.typ)
	}
}

// A forwarded bus's sender has no local process, so no process block matches
// it, not even one that would match anything.
func TestCheckTrustRules_ProcessFailsClosedWithoutLocalProcess(t *testing.T) {
	mgr := NewManagerForTest(ManagerConfig{
		TrustRules: []TrustRule{
			{Name: "any-unit", Action: RuleApprove, Client: MustGlob("*"), RequestTypes: []RequestType{RequestTypeGetSecret}, Process: &ProcessMatcher{Unit: MustGlob("*")}},
			{Name: "empty-process", Action: RuleApprove, Client: MustGlob("*"), RequestTypes: []RequestType{RequestTypeSearch}, Process: &ProcessMatcher{}},
		},
	})

	forwarded := ClientClaim{Name: "symbiosis"}.Sender(":1.7")
	forwarded.PID = 42
	_, ok := mgr.CheckTrustRules(RequestInfo{Type: RequestTypeGetSecret, SenderInfo: forwarded})
	assert.False(t, ok, `unit "*"`)
	_, ok = mgr.CheckTrustRules(RequestInfo{Type: RequestTypeSearch, SenderInfo: forwarded})
	assert.False(t, ok, "empty process")

	local := SenderInfo{Client: ClientLocal, SystemdUnit: "app.service"}
	_, ok = mgr.CheckTrustRules(RequestInfo{Type: RequestTypeGetSecret, SenderInfo: local})
	assert.True(t, ok, `unit "*" on a local sender`)
}

// An expired request ends exactly once. It leaves no pending request and one
// expired entry in History.
func TestManager_RequireApproval_ExpiryEndsRequestOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManagerForTest(ManagerConfig{Timeout: 20 * time.Millisecond})

		_, err := mgr.RequireApproval(t.Context(), getSecretRequest())

		require.ErrorIs(t, err, ErrTimeout)
		assert.Zero(t, mgr.PendingCount())
		// The expiry records the entry after it wakes RequireApproval.
		synctest.Wait()
		assert.Equal(t, []Resolution{ResolutionExpired}, historyResolutions(mgr))
	})
}

// A request resolved before its timeout is not expired later.
func TestManager_ResolvedRequestDoesNotExpire(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManagerForTest(ManagerConfig{Timeout: 20 * time.Millisecond})
		id := mgr.Create(sampleGPGSignRequest())

		require.NoError(t, mgr.Deny(id))
		time.Sleep(60 * time.Millisecond)

		assert.Equal(t, []Resolution{ResolutionDenied}, historyResolutions(mgr))
	})
}

func TestResolution_UnmarshalText(t *testing.T) {
	for _, r := range resolutions {
		data, err := json.Marshal(r)
		require.NoError(t, err)
		var got Resolution
		require.NoError(t, json.Unmarshal(data, &got))
		assert.Equal(t, r, got)
	}
	var got Resolution
	assert.Error(t, json.Unmarshal([]byte(`"bogus"`), &got))
	assert.Error(t, json.Unmarshal([]byte(`""`), &got))
}

// Calls the given function on each ended request, as an observer.
type onEnded struct {
	IgnoreEvents
	f func(HistoryEntry)
}

func (o *onEnded) RequestEnded(e HistoryEntry) { o.f(e) }

// An approval's cache entry and auto-approve rule exist before anyone hears that
// the request ended, so the client's next request does not prompt again.
func TestApprove_SettlesBeforeTheEnd(t *testing.T) {
	for _, tc := range []struct {
		name    string
		approve func(*Manager, RequestID) error
		settled func(*Manager, RequestInfo) bool
	}{
		{"cache entry", (*Manager).Approve, func(m *Manager, info RequestInfo) bool { return m.cache.covers(info) }},
		{"auto-approve rule", (*Manager).ApproveAndAutoApprove, func(m *Manager, info RequestInfo) bool {
			_, ok := m.CheckAutoApproveRules(info)
			return ok
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				mgr := NewManagerForTest(ManagerConfig{ApprovalWindow: time.Minute})
				info := getSecretRequest()
				info.SenderInfo = LocalSenderForTest("gh", "/usr/bin/gh")
				info.SenderInfo.Sender = busSender(":1.5").Sender

				var settled bool
				mgr.Subscribe(&onEnded{f: func(HistoryEntry) { settled = tc.settled(mgr, info) }})
				call := startApproval(t.Context(), mgr, info)
				require.NoError(t, tc.approve(mgr, waitPendingID(t, mgr)))
				call.wait()
				assert.True(t, settled, "observers heard of the end before the approval settled")
			})
		})
	}
}
