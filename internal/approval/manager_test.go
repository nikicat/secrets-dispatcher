package approval

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nikicat/secrets-dispatcher/internal/poll"
	"github.com/nikicat/secrets-dispatcher/internal/procutil"
	"github.com/nikicat/secrets-dispatcher/internal/sender"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManager_RequireApproval_Approved(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})

	var wg sync.WaitGroup
	var approvalErr error

	wg.Go(func() {
		_, approvalErr = mgr.RequireApproval(context.Background(), RequestInfo{Items: []ItemInfo{{Path: "/test/item"}}, Session: "/session/1", Type: RequestTypeGetSecret})
	})

	// Wait for request to appear
	var reqID RequestID
	for range 100 {
		reqs := mgr.List()
		if len(reqs) > 0 {
			reqID = reqs[0].ID
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if reqID == "" {
		t.Fatal("request did not appear in pending list")
	}

	// Approve the request
	if err := mgr.Approve(reqID); err != nil {
		t.Fatalf("Approve failed: %v", err)
	}

	wg.Wait()

	if approvalErr != nil {
		t.Errorf("RequireApproval returned error: %v", approvalErr)
	}
}

func TestManager_RequireApproval_Denied(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})

	var wg sync.WaitGroup
	var approvalErr error

	wg.Go(func() {
		_, approvalErr = mgr.RequireApproval(context.Background(), RequestInfo{Items: []ItemInfo{{Path: "/test/item"}}, Session: "/session/1", Type: RequestTypeGetSecret})
	})

	// Wait for request to appear
	var reqID RequestID
	for range 100 {
		reqs := mgr.List()
		if len(reqs) > 0 {
			reqID = reqs[0].ID
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if reqID == "" {
		t.Fatal("request did not appear in pending list")
	}

	// Deny the request
	if err := mgr.Deny(reqID); err != nil {
		t.Fatalf("Deny failed: %v", err)
	}

	wg.Wait()

	if approvalErr != ErrDenied {
		t.Errorf("expected ErrDenied, got: %v", approvalErr)
	}
}

func TestManager_RequireApproval_Timeout(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 100 * time.Millisecond, HistoryMax: 100})

	_, err := mgr.RequireApproval(context.Background(), RequestInfo{Items: []ItemInfo{{Path: "/test/item"}}, Session: "/session/1", Type: RequestTypeGetSecret})

	if err != ErrTimeout {
		t.Errorf("expected ErrTimeout, got: %v", err)
	}
}

func TestManager_RequireApproval_ContextCanceled(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})

	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	var approvalErr error

	wg.Go(func() {
		_, approvalErr = mgr.RequireApproval(ctx, RequestInfo{Items: []ItemInfo{{Path: "/test/item"}}, Session: "/session/1", Type: RequestTypeGetSecret})
	})

	// Wait for request to appear
	for range 100 {
		if mgr.PendingCount() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Cancel context
	cancel()

	wg.Wait()

	if approvalErr != context.Canceled {
		t.Errorf("expected context.Canceled, got: %v", approvalErr)
	}
}

func TestManager_List(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})

	// Start multiple approval requests
	var wg sync.WaitGroup
	for i := range 3 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			_, _ = mgr.RequireApproval(ctx, RequestInfo{Items: []ItemInfo{{Path: "/test/item"}}, Session: "/session/1", Type: RequestTypeGetSecret})
		}(i)
	}

	// Wait for requests to appear
	for range 100 {
		if mgr.PendingCount() >= 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	reqs := mgr.List()
	if len(reqs) != 3 {
		t.Errorf("expected 3 pending requests, got %d", len(reqs))
	}

	wg.Wait()
}

func TestManager_Cancel_Success(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})

	var wg sync.WaitGroup
	var approvalErr error

	wg.Go(func() {
		_, approvalErr = mgr.RequireApproval(context.Background(), RequestInfo{Items: []ItemInfo{{Path: "/test/item"}}, Session: "/session/1", Type: RequestTypeGetSecret})
	})

	// Wait for request to appear
	var reqID RequestID
	for range 100 {
		reqs := mgr.List()
		if len(reqs) > 0 {
			reqID = reqs[0].ID
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if reqID == "" {
		t.Fatal("request did not appear in pending list")
	}

	// Cancel the request
	if err := mgr.Cancel(reqID); err != nil {
		t.Fatalf("Cancel failed: %v", err)
	}

	wg.Wait()

	// Cancel resolves the request as cancelled, which RequireApproval reports as ErrDenied.
	if approvalErr != ErrDenied {
		t.Errorf("expected ErrDenied after cancel, got: %v", approvalErr)
	}

	// Verify request is cleaned up
	if mgr.PendingCount() != 0 {
		t.Error("request should be cleaned up after cancel")
	}
}

func TestManager_Cancel_NotFound(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})

	err := mgr.Cancel("nonexistent-id")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestManager_Approve_NotFound(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})

	err := mgr.Approve("nonexistent-id")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestManager_Deny_NotFound(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})

	err := mgr.Deny("nonexistent-id")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestManager_Concurrent(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})

	const numRequests = 10
	var wg sync.WaitGroup

	// Track results
	results := make([]error, numRequests)

	// Start approval requests
	for i := range numRequests {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, results[n] = mgr.RequireApproval(context.Background(), RequestInfo{Items: []ItemInfo{{Path: "/test/item"}}, Session: "/session/1", Type: RequestTypeGetSecret})
		}(i)
	}

	// Wait for requests to appear
	for range 100 {
		if mgr.PendingCount() >= numRequests {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Approve half, deny half
	reqs := mgr.List()
	for i, req := range reqs {
		if i%2 == 0 {
			mgr.Approve(req.ID)
		} else {
			mgr.Deny(req.ID)
		}
	}

	wg.Wait()

	// Verify all requests completed
	var approved, denied int
	for _, err := range results {
		if err == nil {
			approved++
		} else if err == ErrDenied {
			denied++
		} else {
			t.Errorf("unexpected error: %v", err)
		}
	}

	if approved+denied != numRequests {
		t.Errorf("expected %d total results, got %d approved + %d denied", numRequests, approved, denied)
	}
}

func TestManager_Disabled(t *testing.T) {
	mgr := NewDisabledManager()

	// Should auto-approve immediately
	_, err := mgr.RequireApproval(context.Background(), RequestInfo{Items: []ItemInfo{{Path: "/test/item"}}, Session: "/session/1", Type: RequestTypeGetSecret})
	if err != nil {
		t.Errorf("disabled manager should auto-approve, got: %v", err)
	}

	// No pending requests
	if mgr.PendingCount() != 0 {
		t.Error("disabled manager should have no pending requests")
	}
}

func TestManager_RequestFields(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})

	var wg sync.WaitGroup
	wg.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		_, _ = mgr.RequireApproval(ctx, RequestInfo{Items: []ItemInfo{{Path: "/test/item1"}, {Path: "/test/item2"}}, Session: "/session/42", Type: RequestTypeGetSecret, SenderInfo: SenderInfo{Client: "test-client"}})
	})

	// Wait for request to appear
	for range 100 {
		if mgr.PendingCount() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	reqs := mgr.List()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(reqs))
	}

	req := reqs[0]
	if req.SenderInfo.Client != "test-client" {
		t.Errorf("expected client 'test-client', got '%s'", req.SenderInfo.Client)
	}
	if len(req.Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(req.Items))
	}
	if req.Session != "/session/42" {
		t.Errorf("expected session '/session/42', got '%s'", req.Session)
	}
	if req.ID == "" {
		t.Error("expected non-empty ID")
	}
	if req.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}
	if req.ExpiresAt.IsZero() {
		t.Error("expected non-zero ExpiresAt")
	}
	if req.ExpiresAt.Before(req.CreatedAt) {
		t.Error("ExpiresAt should be after CreatedAt")
	}

	wg.Wait()
}

func TestManager_CleanupAfterApproval(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})

	var wg sync.WaitGroup
	wg.Go(func() {
		_, _ = mgr.RequireApproval(context.Background(), RequestInfo{Items: []ItemInfo{{Path: "/test/item"}}, Session: "/session/1", Type: RequestTypeGetSecret})
	})

	// Wait for request to appear
	var reqID RequestID
	for range 100 {
		reqs := mgr.List()
		if len(reqs) > 0 {
			reqID = reqs[0].ID
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Approve
	mgr.Approve(reqID)
	wg.Wait()

	// Verify request is cleaned up
	if mgr.PendingCount() != 0 {
		t.Error("request should be cleaned up after approval")
	}

	// Trying to approve again should fail
	if err := mgr.Approve(reqID); err != ErrNotFound {
		t.Errorf("second approval should return ErrNotFound, got: %v", err)
	}
}

// Ignores every event. A test observer embeds it and overrides the methods for
// the events it reads.
type ignoreEvents struct{}

func (ignoreEvents) RequestCreated(Pending)      {}
func (ignoreEvents) RequestEnded(HistoryEntry)   {}
func (ignoreEvents) RuleAdded(AutoApproveRule)   {}
func (ignoreEvents) RuleRemoved(AutoApproveRule) {}

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
	ignoreEvents
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

func (o *testObserver) WaitForEvents(ctx context.Context, count int, timeout time.Duration) []testEvent {
	poll.Until(ctx, timeout, 10*time.Millisecond, func() bool { return len(o.Events()) >= count })
	return o.Events()
}

func TestManager_Observer_Created(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
	obs := &testObserver{}
	mgr.Subscribe(obs)

	var wg sync.WaitGroup
	wg.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		_, _ = mgr.RequireApproval(ctx, RequestInfo{Items: []ItemInfo{{Path: "/test/item"}}, Session: "/session/1", Type: RequestTypeGetSecret})
	})

	// Wait for created event
	events := obs.WaitForEvents(t.Context(), 1, time.Second)
	if len(events) < 1 {
		t.Fatal("expected at least 1 event")
	}
	if events[0].Type != evCreated {
		t.Errorf("expected evCreated, got %v", events[0].Type)
	}
	if events[0].Request == nil {
		t.Error("expected request in event")
	}

	wg.Wait()
	mgr.Unsubscribe(obs)
}

func TestManager_Observer_Approved(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
	obs := &testObserver{}
	mgr.Subscribe(obs)

	var wg sync.WaitGroup
	wg.Go(func() {
		_, _ = mgr.RequireApproval(context.Background(), RequestInfo{Items: []ItemInfo{{Path: "/test/item"}}, Session: "/session/1", Type: RequestTypeGetSecret})
	})

	// Wait for request to appear
	var reqID RequestID
	for range 100 {
		reqs := mgr.List()
		if len(reqs) > 0 {
			reqID = reqs[0].ID
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if reqID == "" {
		t.Fatal("request did not appear")
	}

	// Approve
	mgr.Approve(reqID)
	wg.Wait()

	// Wait for events
	events := obs.WaitForEvents(t.Context(), 2, time.Second)
	if len(events) < 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	if events[0].Type != evCreated {
		t.Errorf("expected first event to be Created, got %v", events[0].Type)
	}
	if events[1].Type != evApproved {
		t.Errorf("expected second event to be Approved, got %v", events[1].Type)
	}

	mgr.Unsubscribe(obs)
}

func TestManager_Observer_Denied(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
	obs := &testObserver{}
	mgr.Subscribe(obs)

	var wg sync.WaitGroup
	wg.Go(func() {
		_, _ = mgr.RequireApproval(context.Background(), RequestInfo{Items: []ItemInfo{{Path: "/test/item"}}, Session: "/session/1", Type: RequestTypeGetSecret})
	})

	// Wait for request to appear
	var reqID RequestID
	for range 100 {
		reqs := mgr.List()
		if len(reqs) > 0 {
			reqID = reqs[0].ID
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Deny
	mgr.Deny(reqID)
	wg.Wait()

	// Wait for events
	events := obs.WaitForEvents(t.Context(), 2, time.Second)
	if len(events) < 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	if events[1].Type != evDenied {
		t.Errorf("expected second event to be Denied, got %v", events[1].Type)
	}

	mgr.Unsubscribe(obs)
}

func TestManager_Observer_Expired(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 100 * time.Millisecond, HistoryMax: 100})
	obs := &testObserver{}
	mgr.Subscribe(obs)

	// This will timeout
	_, _ = mgr.RequireApproval(context.Background(), RequestInfo{Items: []ItemInfo{{Path: "/test/item"}}, Session: "/session/1", Type: RequestTypeGetSecret})

	// Wait for events
	events := obs.WaitForEvents(t.Context(), 2, time.Second)
	if len(events) < 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	if events[0].Type != evCreated {
		t.Errorf("expected first event to be Created, got %v", events[0].Type)
	}
	if events[1].Type != evExpired {
		t.Errorf("expected second event to be Expired, got %v", events[1].Type)
	}

	mgr.Unsubscribe(obs)
}

func TestManager_Observer_CancelMethod(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
	obs := &testObserver{}
	mgr.Subscribe(obs)

	var wg sync.WaitGroup
	wg.Go(func() {
		_, _ = mgr.RequireApproval(context.Background(), RequestInfo{Items: []ItemInfo{{Path: "/test/item"}}, Session: "/session/1", Type: RequestTypeGetSecret})
	})

	// Wait for request to appear
	var reqID RequestID
	for range 100 {
		reqs := mgr.List()
		if len(reqs) > 0 {
			reqID = reqs[0].ID
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if reqID == "" {
		t.Fatal("request did not appear")
	}

	// Cancel via Cancel() method
	mgr.Cancel(reqID)
	wg.Wait()

	// Wait for events
	events := obs.WaitForEvents(t.Context(), 2, time.Second)
	if len(events) < 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	if events[0].Type != evCreated {
		t.Errorf("expected first event to be Created, got %v", events[0].Type)
	}
	if events[1].Type != evCancelled {
		t.Errorf("expected second event to be Cancelled, got %v", events[1].Type)
	}

	mgr.Unsubscribe(obs)
}

func TestManager_Observer_Cancelled(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
	obs := &testObserver{}
	mgr.Subscribe(obs)

	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Go(func() {
		_, err := mgr.RequireApproval(ctx, RequestInfo{Items: []ItemInfo{{Path: "/test/item"}}, Session: "/session/1", Type: RequestTypeGetSecret})
		if err != context.Canceled {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	})

	// Wait for created event
	events := obs.WaitForEvents(t.Context(), 1, time.Second)
	if len(events) < 1 {
		t.Fatal("expected at least 1 event (created)")
	}

	// Cancel the context
	cancel()
	wg.Wait()

	// Wait for cancelled event
	events = obs.WaitForEvents(t.Context(), 2, time.Second)
	if len(events) < 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	if events[0].Type != evCreated {
		t.Errorf("expected first event to be Created, got %v", events[0].Type)
	}
	if events[1].Type != evCancelled {
		t.Errorf("expected second event to be Cancelled, got %v", events[1].Type)
	}

	mgr.Unsubscribe(obs)
}

func TestManager_Observer_Unsubscribe(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
	obs := &testObserver{}
	mgr.Subscribe(obs)
	mgr.Unsubscribe(obs)

	var wg sync.WaitGroup
	wg.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_, _ = mgr.RequireApproval(ctx, RequestInfo{Items: []ItemInfo{{Path: "/test/item"}}, Session: "/session/1", Type: RequestTypeGetSecret})
	})

	wg.Wait()

	// Should receive no events after unsubscribe
	events := obs.Events()
	if len(events) != 0 {
		t.Errorf("expected 0 events after unsubscribe, got %d", len(events))
	}
}

func TestManager_Observer_MultipleObservers(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
	obs1 := &testObserver{}
	obs2 := &testObserver{}
	mgr.Subscribe(obs1)
	mgr.Subscribe(obs2)

	var wg sync.WaitGroup
	wg.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		_, _ = mgr.RequireApproval(ctx, RequestInfo{Items: []ItemInfo{{Path: "/test/item"}}, Session: "/session/1", Type: RequestTypeGetSecret})
	})

	// Wait for created event on both observers
	events1 := obs1.WaitForEvents(t.Context(), 1, time.Second)
	events2 := obs2.WaitForEvents(t.Context(), 1, time.Second)

	if len(events1) < 1 {
		t.Error("observer 1 should receive events")
	}
	if len(events2) < 1 {
		t.Error("observer 2 should receive events")
	}

	wg.Wait()
	mgr.Unsubscribe(obs1)
	mgr.Unsubscribe(obs2)
}

func TestManager_Observer_ConcurrentSubscribe(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})

	var wg sync.WaitGroup
	var subscribed atomic.Int32

	// Subscribe concurrently
	for range 10 {
		wg.Go(func() {
			obs := &testObserver{}
			mgr.Subscribe(obs)
			subscribed.Add(1)
		})
	}

	wg.Wait()

	if subscribed.Load() != 10 {
		t.Errorf("expected 10 subscribed, got %d", subscribed.Load())
	}
}

func TestHistory_RecordsApproved(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})

	var wg sync.WaitGroup
	wg.Go(func() {
		_, _ = mgr.RequireApproval(context.Background(), RequestInfo{Items: []ItemInfo{{Path: "/test/item"}}, Session: "/session/1", Type: RequestTypeGetSecret})
	})

	// Wait for request to appear
	var reqID RequestID
	for range 100 {
		reqs := mgr.List()
		if len(reqs) > 0 {
			reqID = reqs[0].ID
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if reqID == "" {
		t.Fatal("request did not appear")
	}

	// Approve
	mgr.Approve(reqID)
	wg.Wait()

	// Give time for history to be recorded
	time.Sleep(50 * time.Millisecond)

	// Check history
	history := mgr.History()
	if len(history) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(history))
	}
	if history[0].Resolution != ResolutionApproved {
		t.Errorf("expected resolution 'approved', got '%s'", history[0].Resolution)
	}
	if history[0].Request.ID != reqID {
		t.Errorf("expected request ID %s, got %s", reqID, history[0].Request.ID)
	}
}

func TestHistory_RecordsDenied(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})

	var wg sync.WaitGroup
	wg.Go(func() {
		_, _ = mgr.RequireApproval(context.Background(), RequestInfo{Items: []ItemInfo{{Path: "/test/item"}}, Session: "/session/1", Type: RequestTypeGetSecret})
	})

	// Wait for request to appear
	var reqID RequestID
	for range 100 {
		reqs := mgr.List()
		if len(reqs) > 0 {
			reqID = reqs[0].ID
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Deny
	mgr.Deny(reqID)
	wg.Wait()

	// Give time for history to be recorded
	time.Sleep(50 * time.Millisecond)

	// Check history
	history := mgr.History()
	if len(history) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(history))
	}
	if history[0].Resolution != ResolutionDenied {
		t.Errorf("expected resolution 'denied', got '%s'", history[0].Resolution)
	}
}

func TestHistory_RecordsExpired(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 100 * time.Millisecond, HistoryMax: 100})

	// This will timeout
	_, _ = mgr.RequireApproval(context.Background(), RequestInfo{Items: []ItemInfo{{Path: "/test/item"}}, Session: "/session/1", Type: RequestTypeGetSecret})

	// Give time for history to be recorded
	time.Sleep(50 * time.Millisecond)

	// Check history
	history := mgr.History()
	if len(history) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(history))
	}
	if history[0].Resolution != ResolutionExpired {
		t.Errorf("expected resolution 'expired', got '%s'", history[0].Resolution)
	}
}

func TestHistory_RecordsCancelled(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})

	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Go(func() {
		_, _ = mgr.RequireApproval(ctx, RequestInfo{Items: []ItemInfo{{Path: "/test/item"}}, Session: "/session/1", Type: RequestTypeGetSecret})
	})

	// Wait for request to appear
	for range 100 {
		if mgr.PendingCount() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Cancel the context
	cancel()
	wg.Wait()

	// Give time for history to be recorded
	time.Sleep(50 * time.Millisecond)

	// Check history
	history := mgr.History()
	if len(history) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(history))
	}
	if history[0].Resolution != ResolutionCancelled {
		t.Errorf("expected resolution 'cancelled', got '%s'", history[0].Resolution)
	}
}

func TestHistory_LimitEnforced(t *testing.T) {
	historyMax := 5
	mgr := NewManager(ManagerConfig{Timeout: 100 * time.Millisecond, HistoryMax: historyMax})

	// Create more requests than the history limit
	for i := 0; i < historyMax+3; i++ {
		_, _ = mgr.RequireApproval(context.Background(), RequestInfo{Items: []ItemInfo{{Path: "/test/item"}}, Session: "/session/1", Type: RequestTypeGetSecret})
	}

	// Check history is limited
	history := mgr.History()
	if len(history) != historyMax {
		t.Errorf("expected %d history entries (limit), got %d", historyMax, len(history))
	}
}

func TestHistory_NewestFirst(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})

	// Create and resolve 3 requests in sequence
	for range 3 {
		var wg sync.WaitGroup
		wg.Go(func() {
			_, _ = mgr.RequireApproval(context.Background(), RequestInfo{Items: []ItemInfo{{Path: "/test/item"}}, Session: "/session/1", Type: RequestTypeGetSecret})
		})

		// Wait for request to appear
		var reqID RequestID
		for range 100 {
			reqs := mgr.List()
			if len(reqs) > 0 {
				reqID = reqs[0].ID
				break
			}
			time.Sleep(10 * time.Millisecond)
		}

		mgr.Approve(reqID)
		wg.Wait()

		// Small delay to ensure different timestamps
		time.Sleep(10 * time.Millisecond)
	}

	// Give time for history to be recorded
	time.Sleep(50 * time.Millisecond)

	// Check history is in reverse chronological order (newest first)
	history := mgr.History()
	if len(history) != 3 {
		t.Fatalf("expected 3 history entries, got %d", len(history))
	}

	for i := 0; i < len(history)-1; i++ {
		if history[i].ResolvedAt.Before(history[i+1].ResolvedAt) {
			t.Errorf("history entry %d resolved before entry %d, but should be after (newest first)", i, i+1)
		}
	}
}

func TestApprovalCache_SameSenderItemAutoApproved(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100, ApprovalWindow: time.Second})

	items := []ItemInfo{{Path: "/test/item1"}}
	sender := SenderInfo{Sender: new(sender.UniqueName(":1.100"))}

	// First request: must be manually approved
	var wg sync.WaitGroup
	wg.Go(func() {
		_, _ = mgr.RequireApproval(context.Background(), RequestInfo{Items: items, Session: "/s/1", Type: RequestTypeGetSecret, SenderInfo: sender})
	})

	var reqID RequestID
	for range 100 {
		reqs := mgr.List()
		if len(reqs) > 0 {
			reqID = reqs[0].ID
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if reqID == "" {
		t.Fatal("first request did not appear")
	}
	mgr.Approve(reqID)
	wg.Wait()

	// A second request from the same sender for the same item, within the window,
	// is approved without going pending.
	_, err := mgr.RequireApproval(context.Background(), RequestInfo{Items: items, Session: "/s/1", Type: RequestTypeGetSecret, SenderInfo: sender})
	if err != nil {
		t.Fatalf("expected auto-approval from cache, got: %v", err)
	}
	if mgr.PendingCount() != 0 {
		t.Error("cached approval should not create a pending request")
	}
}

func TestApprovalCache_Expired(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100, ApprovalWindow: 50 * time.Millisecond})

	items := []ItemInfo{{Path: "/test/item1"}}
	sender := SenderInfo{Sender: new(sender.UniqueName(":1.100"))}

	// Approve first request
	var wg sync.WaitGroup
	wg.Go(func() {
		_, _ = mgr.RequireApproval(context.Background(), RequestInfo{Items: items, Session: "/s/1", Type: RequestTypeGetSecret, SenderInfo: sender})
	})

	var reqID RequestID
	for range 100 {
		reqs := mgr.List()
		if len(reqs) > 0 {
			reqID = reqs[0].ID
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mgr.Approve(reqID)
	wg.Wait()

	// Wait for cache to expire
	time.Sleep(60 * time.Millisecond)

	// Second request should NOT be auto-approved
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := mgr.RequireApproval(ctx, RequestInfo{Items: items, Session: "/s/1", Type: RequestTypeGetSecret, SenderInfo: sender})
	if err == nil {
		t.Fatal("expected approval to be required after cache expiry")
	}
}

func TestApprovalCache_DifferentSender(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100, ApprovalWindow: time.Second})

	items := []ItemInfo{{Path: "/test/item1"}}

	// Approve for sender1
	var wg sync.WaitGroup
	wg.Go(func() {
		_, _ = mgr.RequireApproval(context.Background(), RequestInfo{Items: items, Session: "/s/1", Type: RequestTypeGetSecret, SenderInfo: SenderInfo{Sender: new(sender.UniqueName(":1.100"))}})
	})

	var reqID RequestID
	for range 100 {
		reqs := mgr.List()
		if len(reqs) > 0 {
			reqID = reqs[0].ID
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mgr.Approve(reqID)
	wg.Wait()

	// Different sender should NOT be auto-approved
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := mgr.RequireApproval(ctx, RequestInfo{Items: items, Session: "/s/1", Type: RequestTypeGetSecret, SenderInfo: SenderInfo{Sender: new(sender.UniqueName(":1.999"))}})
	if err == nil {
		t.Fatal("different sender should not be auto-approved from cache")
	}
}

func TestApprovalCache_DeleteBypassesCache(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100, ApprovalWindow: time.Second})

	items := []ItemInfo{{Path: "/test/item1"}}
	sender := SenderInfo{Sender: new(sender.UniqueName(":1.100"))}

	// Approving a GetSecret request first fills the cache.
	var wg sync.WaitGroup
	wg.Go(func() {
		_, _ = mgr.RequireApproval(context.Background(), RequestInfo{Items: items, Session: "/s/1", Type: RequestTypeGetSecret, SenderInfo: sender})
	})

	var reqID RequestID
	for range 100 {
		reqs := mgr.List()
		if len(reqs) > 0 {
			reqID = reqs[0].ID
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if reqID == "" {
		t.Fatal("first request did not appear")
	}
	mgr.Approve(reqID)
	wg.Wait()

	// Verify GetSecret is cached (auto-approved)
	_, err := mgr.RequireApproval(context.Background(), RequestInfo{Items: items, Session: "/s/1", Type: RequestTypeGetSecret, SenderInfo: sender})
	if err != nil {
		t.Fatalf("GetSecret should be auto-approved from cache, got: %v", err)
	}

	// A delete request does not use the cache, so it goes pending.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = mgr.RequireApproval(ctx, RequestInfo{Items: items, Session: "/s/1", Type: RequestTypeDelete, SenderInfo: sender})
	if err == nil {
		t.Fatal("Delete should not be auto-approved from cache")
	}
}

func TestApprovalCache_WriteBypassesCache(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100, ApprovalWindow: time.Second})

	items := []ItemInfo{{Path: "/test/item1"}}
	sender := SenderInfo{Sender: new(sender.UniqueName(":1.100"))}

	// Approving a GetSecret request first fills the cache.
	var wg sync.WaitGroup
	wg.Go(func() {
		_, _ = mgr.RequireApproval(context.Background(), RequestInfo{Items: items, Session: "/s/1", Type: RequestTypeGetSecret, SenderInfo: sender})
	})

	var reqID RequestID
	for range 100 {
		reqs := mgr.List()
		if len(reqs) > 0 {
			reqID = reqs[0].ID
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if reqID == "" {
		t.Fatal("first request did not appear")
	}
	mgr.Approve(reqID)
	wg.Wait()

	// Write should NOT use cache
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := mgr.RequireApproval(ctx, RequestInfo{Items: items, Session: "/s/1", Type: RequestTypeWrite, SenderInfo: sender})
	if err == nil {
		t.Fatal("Write should not be auto-approved from cache")
	}
}

func TestCacheItemForSender_AutoApprovesRead(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100, ApprovalWindow: time.Second})

	sender := SenderInfo{Sender: new(sender.UniqueName(":1.200"))}
	itemPath := "/org/freedesktop/secrets/collection/default/i123"

	// Manually cache the item (simulates post-CreateItem caching)
	mgr.CacheItemForSender(sender, itemPath)

	// GetSecret for the same item should be auto-approved
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := mgr.RequireApproval(ctx, RequestInfo{Items: []ItemInfo{{Path: itemPath}}, Session: "/s/1", Type: RequestTypeGetSecret, SenderInfo: sender})
	if err != nil {
		t.Fatalf("GetSecret should be auto-approved after CacheItemForSender, got: %v", err)
	}
}

func TestCacheItemForSender_DifferentSenderNotCached(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100, ApprovalWindow: time.Second})

	itemPath := "/org/freedesktop/secrets/collection/default/i123"

	// Cache for sender :1.200
	mgr.CacheItemForSender(SenderInfo{Sender: new(sender.UniqueName(":1.200"))}, itemPath)

	// Different sender should NOT be auto-approved
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := mgr.RequireApproval(ctx, RequestInfo{Items: []ItemInfo{{Path: itemPath}}, Session: "/s/1", Type: RequestTypeGetSecret, SenderInfo: SenderInfo{Sender: new(sender.UniqueName(":1.300"))}})
	if err == nil {
		t.Fatal("different sender should not be auto-approved")
	}
}

// An approval cached on one client does not cover the same unique name on
// another.
func TestCacheItemForSender_DifferentClientNotCached(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100, ApprovalWindow: time.Second})

	itemPath := "/org/freedesktop/secrets/collection/default/i123"
	mgr.CacheItemForSender(SenderInfo{Client: "a", Sender: new(sender.UniqueName(":1.200"))}, itemPath)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := mgr.RequireApproval(ctx, RequestInfo{Items: []ItemInfo{{Path: itemPath}}, Session: "/s/1", Type: RequestTypeGetSecret, SenderInfo: SenderInfo{Client: "b", Sender: new(sender.UniqueName(":1.200"))}})
	if err == nil {
		t.Fatal("the same sender name on another client should not be auto-approved")
	}
}

// A request that did not come over D-Bus has no sender to tie an approval to,
// so another process could reuse it.
func TestCacheItemForSender_NoSenderNotCached(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100, ApprovalWindow: time.Second})

	itemPath := "/org/freedesktop/secrets/collection/default/i123"
	mgr.CacheItemForSender(SenderInfo{}, itemPath)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := mgr.RequireApproval(ctx, RequestInfo{Items: []ItemInfo{{Path: itemPath}}, Session: "/s/1", Type: RequestTypeGetSecret})
	if err == nil {
		t.Fatal("a request without a sender should not be auto-approved")
	}
}

// An approval cached before a client name is released does not cover the same
// unique name once the name is claimed again.
func TestReleaseClient_DropsCachedApprovals(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100, ApprovalWindow: time.Minute})

	itemPath := "/org/freedesktop/secrets/collection/default/i123"
	sender := SenderInfo{Client: "vm", Sender: new(sender.UniqueName(":1.5"))}
	if !mgr.ClaimClient("vm") {
		t.Fatal("first claim of vm failed")
	}
	if mgr.ClaimClient("vm") {
		t.Fatal("vm claimed twice")
	}
	mgr.CacheItemForSender(sender, itemPath)
	mgr.ReleaseClient("vm")
	if !mgr.ClaimClient("vm") {
		t.Fatal("released name could not be claimed again")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := mgr.RequireApproval(ctx, RequestInfo{Items: []ItemInfo{{Path: itemPath}}, Session: "/s/1", Type: RequestTypeGetSecret, SenderInfo: sender})
	if err == nil {
		t.Fatal("an approval cached for the released bus approved the new one")
	}
}

func TestApprovalCache_DifferentItem(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100, ApprovalWindow: time.Second})

	sender := SenderInfo{Sender: new(sender.UniqueName(":1.100"))}

	// Approve for item1
	var wg sync.WaitGroup
	wg.Go(func() {
		_, _ = mgr.RequireApproval(context.Background(), RequestInfo{Items: []ItemInfo{{Path: "/test/item1"}}, Session: "/s/1", Type: RequestTypeGetSecret, SenderInfo: sender})
	})

	var reqID RequestID
	for range 100 {
		reqs := mgr.List()
		if len(reqs) > 0 {
			reqID = reqs[0].ID
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mgr.Approve(reqID)
	wg.Wait()

	// Different item should NOT be auto-approved
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := mgr.RequireApproval(ctx, RequestInfo{Items: []ItemInfo{{Path: "/test/item2"}}, Session: "/s/1", Type: RequestTypeGetSecret, SenderInfo: sender})
	if err == nil {
		t.Fatal("different item should not be auto-approved from cache")
	}
}

func TestManager_EventOrder_CancelledContext(t *testing.T) {
	// The created event must reach observers before the cancelled one, even
	// when the context is already cancelled.
	for i := range 100 {
		mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
		obs := &testObserver{}
		mgr.Subscribe(obs)

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // pre-cancel so select fires ctx.Done() immediately

		_, _ = mgr.RequireApproval(ctx, RequestInfo{Items: []ItemInfo{{Path: "/test/item"}}, Session: "/s/1", Type: RequestTypeGetSecret})

		events := obs.WaitForEvents(t.Context(), 2, time.Second)
		if len(events) < 2 {
			t.Fatalf("iteration %d: expected 2 events, got %d", i, len(events))
		}
		if events[0].Type != evCreated {
			t.Fatalf("iteration %d: expected evCreated first, got %v then %v", i, events[0].Type, events[1].Type)
		}
		if events[1].Type != evCancelled {
			t.Fatalf("iteration %d: expected evCancelled second, got %v then %v", i, events[0].Type, events[1].Type)
		}

		mgr.Unsubscribe(obs)
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
		mgr := NewManager(ManagerConfig{Timeout: time.Second, HistoryMax: 10, IgnoreChromeDummy: true})
		if !mgr.dropsChromeDummyWrite(RequestInfo{Items: chromeItems, Type: RequestTypeWrite}) {
			t.Error("expected true")
		}
	})
	t.Run("enabled + read + matching schema", func(t *testing.T) {
		mgr := NewManager(ManagerConfig{Timeout: time.Second, HistoryMax: 10, IgnoreChromeDummy: true})
		if mgr.dropsChromeDummyWrite(RequestInfo{Items: chromeItems, Type: RequestTypeGetSecret}) {
			t.Error("expected false for non-write")
		}
	})
	t.Run("enabled + write + different schema", func(t *testing.T) {
		mgr := NewManager(ManagerConfig{Timeout: time.Second, HistoryMax: 10, IgnoreChromeDummy: true})
		if mgr.dropsChromeDummyWrite(RequestInfo{Items: normalItems, Type: RequestTypeWrite}) {
			t.Error("expected false for non-chrome schema")
		}
	})
	t.Run("disabled + write + matching schema", func(t *testing.T) {
		mgr := NewManager(ManagerConfig{Timeout: time.Second, HistoryMax: 10, IgnoreChromeDummy: false})
		if mgr.dropsChromeDummyWrite(RequestInfo{Items: chromeItems, Type: RequestTypeWrite}) {
			t.Error("expected false when disabled")
		}
	})
}

func TestRequireApproval_ChromeDummyWriteIgnored(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: time.Second, HistoryMax: 10, IgnoreChromeDummy: true})
	obs := &testObserver{}
	mgr.Subscribe(obs)

	items := []ItemInfo{{
		Path:       "/org/freedesktop/secrets/collection/default/123",
		Label:      "Chrome Safe Storage",
		Attributes: map[string]string{"xdg:schema": chromeDummySchema},
	}}

	prompted, err := mgr.RequireApproval(t.Context(), RequestInfo{Items: items, Session: "/session/1", Type: RequestTypeWrite, SenderInfo: SenderInfo{Client: "test-client"}})
	require.ErrorIs(t, err, ErrIgnored)
	assert.False(t, prompted)

	// Check event fired
	events := obs.WaitForEvents(t.Context(), 1, time.Second)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != evIgnored {
		t.Errorf("expected evIgnored, got %v", events[0].Type)
	}

	// Check history entry
	history := mgr.History()
	if len(history) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(history))
	}
	if history[0].Resolution != ResolutionIgnored {
		t.Errorf("expected resolution 'ignored', got '%s'", history[0].Resolution)
	}
	if history[0].Request.SenderInfo.Client != "test-client" {
		t.Errorf("expected client 'test-client', got '%s'", history[0].Request.SenderInfo.Client)
	}

	// No pending requests
	if mgr.PendingCount() != 0 {
		t.Error("ignored request should not be pending")
	}

	mgr.Unsubscribe(obs)
}

// Objects of each kind that git signs.
const (
	testCommitObject = "tree 0\nauthor A <a@x> 1 +0000\ncommitter A <a@x> 1 +0000\n\nfix\n"
	testTagObject    = "object def\ntype commit\ntag v1\ntagger A <a@x> 1 +0000\n\nv1\n"
	testPushObject   = "certificate version 0.1\npusher A <a@x> 1 +0000\npushee https://x/r.git\n\n0 1 refs/heads/main\n"
)

// Returns a gpg_sign request from the given sender to sign a commit in the given
// repo, with the given files staged.
func signRequest(sender SenderInfo, repo RepoName, files []string) RequestInfo {
	return signRequestOf(sender, repo, testCommitObject, files)
}

// Returns a gpg_sign request from the given sender to sign the given object in
// the given repo, with the given files staged.
func signRequestOf(sender SenderInfo, repo RepoName, object string, files []string) RequestInfo {
	info, err := NewGPGSignInfo(SignedObject(object), repo, "KEY", ListedFiles(files))
	if err != nil {
		panic(err)
	}
	return info.RequestInfo(sender)
}

func TestCheckTrustedSigner(t *testing.T) {
	const nvimExe = "/usr/bin/nvim"
	mgr := NewManager(ManagerConfig{
		Timeout:    5 * time.Second,
		HistoryMax: 100,
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
		assert.True(t, mgr.CheckTrustedSigner(signRequest(viaHelper, "my-repo", nil)))
	})

	t.Run("wrong repo", func(t *testing.T) {
		assert.False(t, mgr.CheckTrustedSigner(signRequest(viaHelper, "other-repo", nil)))
	})

	t.Run("empty chain", func(t *testing.T) {
		assert.False(t, mgr.CheckTrustedSigner(signRequest(SenderInfo{PeerTrusted: true}, "my-repo", nil)))
	})

	t.Run("empty repo matches any", func(t *testing.T) {
		m := NewManager(ManagerConfig{
			Timeout:        5 * time.Second,
			HistoryMax:     100,
			TrustedSigners: []TrustedSigner{{ExePath: nvimExe}},
		})
		assert.True(t, m.CheckTrustedSigner(signRequest(viaHelper, "any-repo", nil)))
	})

	t.Run("file prefix match and reject", func(t *testing.T) {
		m := NewManager(ManagerConfig{
			Timeout:        5 * time.Second,
			HistoryMax:     100,
			TrustedSigners: []TrustedSigner{{ExePath: nvimExe, FilePrefix: "secret-service/"}},
		})
		assert.True(t, m.CheckTrustedSigner(signRequest(viaHelper, "any", []string{"secret-service/default/i1.age", "secret-service/login/i2.age"})))
		assert.False(t, m.CheckTrustedSigner(signRequest(viaHelper, "any", []string{"secret-service/default/i1.age", "other/sneaky.gpg"})))
	})

	t.Run("file prefix matches only commits", func(t *testing.T) {
		m := NewManager(ManagerConfig{
			Timeout:        5 * time.Second,
			HistoryMax:     100,
			TrustedSigners: []TrustedSigner{{ExePath: nvimExe, FilePrefix: "secret-service/"}},
		})
		assert.False(t, m.CheckTrustedSigner(signRequestOf(viaHelper, "any", testTagObject, []string{"secret-service/x"})))
		assert.False(t, m.CheckTrustedSigner(signRequestOf(viaHelper, "any", testPushObject, []string{"secret-service/x"})))
	})

	t.Run("no file prefix matches a tag", func(t *testing.T) {
		assert.True(t, mgr.CheckTrustedSigner(signRequestOf(viaHelper, "my-repo", testTagObject, nil)))
	})
}

// A process that sets its comm to a unit name must not match a `unit` rule,
// which compares the caller's real systemd unit (ADR 0005).
func TestProcessUnitMatchesSystemdUnitNotComm(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
	mgr.trustRules = []TrustRule{{
		Name:    "approve-firefox-unit",
		Action:  "approve",
		Process: &ProcessMatcher{Unit: MustGlob("firefox.service")},
	}}
	items := []ItemInfo{{Path: "/org/freedesktop/secrets/collection/default/1"}}

	// Genuine caller: comm is "firefox", real systemd unit is "firefox.service".
	genuine := SenderInfo{Client: ClientLocal, InvokerName: "firefox", SystemdUnit: "firefox.service"}
	assert.NotNil(t, mgr.CheckTrustRules(RequestInfo{Items: items, Type: RequestTypeGetSecret, SenderInfo: genuine}),
		"unit rule should match the real systemd unit")

	// Spoofer: sets its comm to "firefox.service" but has no such systemd unit.
	spoofed := SenderInfo{Client: ClientLocal, InvokerName: "firefox.service", SystemdUnit: ""}
	assert.Nil(t, mgr.CheckTrustRules(RequestInfo{Items: items, Type: RequestTypeGetSecret, SenderInfo: spoofed}),
		"unit rule must not match a comm spoofed to look like a unit name")
}

// Malware that speaks the socket protocol directly, with a trusted signer among
// its ancestors, must still get a prompt (ADR 0008).
func TestCheckTrustedSigner_RejectsUntrustedPeer(t *testing.T) {
	const nvimExe = "/usr/bin/nvim"
	mgr := NewManager(ManagerConfig{
		Timeout:        5 * time.Second,
		HistoryMax:     100,
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
	assert.False(t, mgr.CheckTrustedSigner(signRequest(forged, "any", nil)),
		"a direct protocol speaker must not silently sign even with a trusted ancestor")

	// Setting only PeerTrusted lets the trusted ancestor match again. So the
	// gate, not the ancestry, blocked the forged request.
	trusted := forged
	trusted.PeerTrusted = true
	assert.True(t, mgr.CheckTrustedSigner(signRequest(trusted, "any", nil)))
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

	mgr := NewManager(ManagerConfig{
		Timeout:    5 * time.Second,
		HistoryMax: 100,
		TrustRules: rules,
	})

	ghChain := []procutil.Process{{Name: "gh", PID: 100, Exe: new(procutil.Path("/usr/bin/gh"))}}
	chromeChain := []procutil.Process{{Name: "chrome", PID: 200, Exe: new(procutil.Path("/opt/google/chrome/chrome"))}}
	gopassSender := SenderInfo{Client: ClientLocal, InvokerName: "gopass", SystemdUnit: "gopass-secret-service.service", ProcessChain: []procutil.Process{{Name: "gopass", PID: 300}}}

	t.Run("match process name + request type", func(t *testing.T) {
		rule := mgr.CheckTrustRules(RequestInfo{
			Type:       RequestTypeSearch,
			SenderInfo: SenderInfo{Client: ClientLocal, ProcessChain: ghChain},
		})
		if rule == nil || rule.Name != "approve-gh-search" {
			t.Errorf("expected approve-gh-search, got %v", rule)
		}
	})

	t.Run("no match wrong request type", func(t *testing.T) {
		rule := mgr.CheckTrustRules(RequestInfo{
			Type:       RequestTypeGetSecret,
			SenderInfo: SenderInfo{Client: ClientLocal, ProcessChain: ghChain},
		})
		// No rule matches. Rule 0 covers only search, and with no items neither
		// the label rule nor the attribute rule can match.
		if rule != nil {
			t.Errorf("expected no match, got %v", rule.Name)
		}
	})

	t.Run("match exe + collection for ignore", func(t *testing.T) {
		items := []ItemInfo{{
			Path:  "/org/freedesktop/secrets/collection/default/123",
			Label: "Chrome Safe Storage",
		}}
		rule := mgr.CheckTrustRules(RequestInfo{
			Items:      items,
			Type:       RequestTypeWrite,
			SenderInfo: SenderInfo{Client: ClientLocal, ProcessChain: chromeChain},
		})
		if rule == nil || rule.Name != "ignore-chrome-write" {
			t.Errorf("expected ignore-chrome-write, got %v", rule)
		}
	})

	t.Run("no match wrong collection", func(t *testing.T) {
		items := []ItemInfo{{
			Path:  "/org/freedesktop/secrets/collection/login/123",
			Label: "Chrome Safe Storage",
		}}
		rule := mgr.CheckTrustRules(RequestInfo{
			Items:      items,
			Type:       RequestTypeWrite,
			SenderInfo: SenderInfo{Client: ClientLocal, ProcessChain: chromeChain},
		})
		// Rule 1 requires the default collection, so a write to the login
		// collection matches no rule.
		if rule != nil {
			t.Errorf("expected no match, got %v", rule.Name)
		}
	})

	t.Run("match unit glob", func(t *testing.T) {
		rule := mgr.CheckTrustRules(RequestInfo{
			Items:      []ItemInfo{{Path: "/org/freedesktop/secrets/collection/default/1"}},
			Type:       RequestTypeGetSecret,
			SenderInfo: gopassSender,
		})
		if rule == nil || rule.Name != "approve-all-from-unit" {
			t.Errorf("expected approve-all-from-unit, got %v", rule)
		}
	})

	t.Run("match label glob", func(t *testing.T) {
		items := []ItemInfo{{
			Path:  "/org/freedesktop/secrets/collection/default/1",
			Label: "GitHub Token",
		}}
		rule := mgr.CheckTrustRules(RequestInfo{
			Items:      items,
			Type:       RequestTypeGetSecret,
			SenderInfo: SenderInfo{Client: ClientLocal, ProcessChain: []procutil.Process{{Name: "random-app", PID: 999}}},
		})
		if rule == nil || rule.Name != "approve-by-label" {
			t.Errorf("expected approve-by-label, got %v", rule)
		}
	})

	t.Run("match search attributes", func(t *testing.T) {
		rule := mgr.CheckTrustRules(RequestInfo{
			Type:       RequestTypeSearch,
			Payload:    SearchPayload{Attributes: map[string]string{"xdg:schema": "org.gnome.keyring.Note"}},
			SenderInfo: SenderInfo{Client: ClientLocal, ProcessChain: []procutil.Process{{Name: "app", PID: 1}}},
		})
		if rule == nil || rule.Name != "approve-search-attrs" {
			t.Errorf("expected approve-search-attrs, got %v", rule)
		}
	})

	t.Run("match item attributes", func(t *testing.T) {
		items := []ItemInfo{{
			Path:       "/org/freedesktop/secrets/collection/default/1",
			Label:      "WiFi Password",
			Attributes: map[string]string{"xdg:schema": "org.gnome.keyring.NetworkPassword", "ssid": "home"},
		}}
		rule := mgr.CheckTrustRules(RequestInfo{
			Items:      items,
			Type:       RequestTypeGetSecret,
			SenderInfo: SenderInfo{Client: ClientLocal, ProcessChain: []procutil.Process{{Name: "nm-applet", PID: 500}}},
		})
		if rule == nil || rule.Name != "approve-by-attributes" {
			t.Errorf("expected approve-by-attributes, got %v", rule)
		}
	})

	t.Run("match item attributes with glob", func(t *testing.T) {
		m := NewManager(ManagerConfig{
			Timeout:    time.Second,
			HistoryMax: 10,
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
		rule := m.CheckTrustRules(RequestInfo{
			Items:      items,
			Type:       RequestTypeGetSecret,
			SenderInfo: SenderInfo{Client: ClientLocal, ProcessChain: []procutil.Process{{Name: "kubectl", PID: 1}}},
		})
		if rule == nil || rule.Name != "approve-glob-attrs" {
			t.Errorf("expected approve-glob-attrs, got %v", rule)
		}
		// Non-matching value
		items[0].Attributes["username"] = "other/path"
		rule = m.CheckTrustRules(RequestInfo{
			Items:      items,
			Type:       RequestTypeGetSecret,
			SenderInfo: SenderInfo{Client: ClientLocal, ProcessChain: []procutil.Process{{Name: "kubectl", PID: 1}}},
		})
		if rule != nil {
			t.Errorf("expected nil for non-matching glob, got %v", rule)
		}
	})

	t.Run("first match wins", func(t *testing.T) {
		// gh doing search matches rule 0 (approve-gh-search), not rule 4 (approve-search-attrs)
		rule := mgr.CheckTrustRules(RequestInfo{
			Type:       RequestTypeSearch,
			Payload:    SearchPayload{Attributes: map[string]string{"xdg:schema": "org.gnome.keyring.Note"}},
			SenderInfo: SenderInfo{Client: ClientLocal, ProcessChain: ghChain},
		})
		if rule == nil || rule.Name != "approve-gh-search" {
			t.Errorf("expected first match approve-gh-search, got %v", rule)
		}
	})

	t.Run("empty rules", func(t *testing.T) {
		m := NewManager(ManagerConfig{Timeout: time.Second, HistoryMax: 10})
		rule := m.CheckTrustRules(RequestInfo{Type: RequestTypeGetSecret})
		if rule != nil {
			t.Errorf("expected nil from empty rules, got %v", rule)
		}
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
	mgr := NewManager(ManagerConfig{
		Timeout:    5 * time.Second,
		HistoryMax: 100,
		TrustRules: []TrustRule{
			{
				Name:         "approve-gh",
				Action:       "approve",
				RequestTypes: []RequestType{"search"},
				Process:      &ProcessMatcher{Name: MustGlob("gh")},
			},
		},
	})
	obs := &testObserver{}
	mgr.Subscribe(obs)

	_, err := mgr.RequireApproval(context.Background(), RequestInfo{
		Session:    "/s/1",
		Type:       RequestTypeSearch,
		SenderInfo: SenderInfo{Client: ClientLocal, ProcessChain: []procutil.Process{{Name: "gh", PID: 1}}},
	})
	if err != nil {
		t.Fatalf("expected nil (auto-approved), got %v", err)
	}
	if mgr.PendingCount() != 0 {
		t.Error("trust rule approve should not create pending request")
	}

	events := obs.WaitForEvents(t.Context(), 1, time.Second)
	if len(events) < 1 || events[0].Type != evAutoApproved {
		t.Errorf("expected evAutoApproved, got %v", events)
	}

	// Check history
	history := mgr.History()
	if len(history) != 1 || history[0].Resolution != ResolutionAutoApproved {
		t.Errorf("expected auto_approved in history, got %v", history)
	}

	mgr.Unsubscribe(obs)
}

func TestTrustRules_RequireApproval_Ignore(t *testing.T) {
	mgr := NewManager(ManagerConfig{
		Timeout:    5 * time.Second,
		HistoryMax: 100,
		TrustRules: []TrustRule{
			{
				Name:         "ignore-chrome",
				Action:       "ignore",
				RequestTypes: []RequestType{"write"},
				Secret:       &SecretMatcher{Attributes: map[string]Glob{"xdg:schema": MustGlob(chromeDummySchema)}},
			},
		},
	})
	obs := &testObserver{}
	mgr.Subscribe(obs)

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
	if mgr.PendingCount() != 0 {
		t.Error("trust rule ignore should not create pending request")
	}

	events := obs.WaitForEvents(t.Context(), 1, time.Second)
	if len(events) < 1 || events[0].Type != evIgnored {
		t.Errorf("expected evIgnored, got %v", events)
	}

	history := mgr.History()
	if len(history) != 1 || history[0].Resolution != ResolutionIgnored {
		t.Errorf("expected ignored in history, got %v", history)
	}

	mgr.Unsubscribe(obs)
}

func TestTrustRules_RequireApproval_Deny(t *testing.T) {
	mgr := NewManager(ManagerConfig{
		Timeout:    5 * time.Second,
		HistoryMax: 100,
		TrustRules: []TrustRule{
			{
				Name:    "deny-epiphany",
				Action:  "deny",
				Process: &ProcessMatcher{Name: MustGlob("epiphany-search")},
			},
		},
	})
	obs := &testObserver{}
	mgr.Subscribe(obs)

	_, err := mgr.RequireApproval(context.Background(), RequestInfo{
		Items:      []ItemInfo{{Path: "/org/freedesktop/secrets/collection/default/1"}},
		Session:    "/s/1",
		Type:       RequestTypeGetSecret,
		SenderInfo: SenderInfo{Client: ClientLocal, ProcessChain: []procutil.Process{{Name: "epiphany-search", PID: 1}}},
	})
	require.ErrorIs(t, err, ErrDeniedByRule)
	if mgr.PendingCount() != 0 {
		t.Error("trust rule deny should not create pending request")
	}

	events := obs.WaitForEvents(t.Context(), 1, time.Second)
	if len(events) < 1 || events[0].Type != evDenied {
		t.Errorf("expected evDenied, got %v", events)
	}

	history := mgr.History()
	if len(history) != 1 || history[0].Resolution != ResolutionDenied {
		t.Errorf("expected denied in history, got %v", history)
	}

	mgr.Unsubscribe(obs)
}

func TestTrustRules_RequireApproval_NoMatch(t *testing.T) {
	mgr := NewManager(ManagerConfig{
		Timeout:    100 * time.Millisecond,
		HistoryMax: 100,
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
	if err != ErrTimeout {
		t.Fatalf("expected ErrTimeout (no trust rule match), got %v", err)
	}
}

func TestTrustRules_DefaultActionIsApprove(t *testing.T) {
	mgr := NewManager(ManagerConfig{
		Timeout:    5 * time.Second,
		HistoryMax: 100,
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
	if err != nil {
		t.Fatalf("expected nil (default action approve), got %v", err)
	}
}

func TestTrustRules_CWDMatch(t *testing.T) {
	mgr := NewManager(ManagerConfig{
		Timeout:    5 * time.Second,
		HistoryMax: 100,
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
		Items:   []ItemInfo{{Path: "/org/freedesktop/secrets/collection/default/x"}},
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
	if err != nil {
		t.Fatalf("expected nil (CWD match), got %v", err)
	}

	// A working directory that does not match leaves the request to time out.
	mgr2 := NewManager(ManagerConfig{
		Timeout:    100 * time.Millisecond,
		HistoryMax: 100,
		TrustRules: mgr.trustRules,
	})
	_, err = mgr2.RequireApproval(context.Background(), RequestInfo{
		Items:   []ItemInfo{{Path: "/org/freedesktop/secrets/collection/default/x"}},
		Session: "/s/1",
		Type:    RequestTypeGetSecret,
		SenderInfo: SenderInfo{
			Client: ClientLocal,
			ProcessChain: []procutil.Process{
				{Name: "electron", PID: 100, Exe: new(procutil.Path("/usr/lib/electron39/electron")), CWD: new(procutil.Path("/opt/something-else"))},
			},
		},
	})
	if err != ErrTimeout {
		t.Fatalf("expected ErrTimeout (CWD mismatch), got %v", err)
	}
}

func TestRequireApproval_AutoApproved_Disabled(t *testing.T) {
	mgr := NewDisabledManager()
	prompted, err := mgr.RequireApproval(context.Background(), RequestInfo{Type: RequestTypeGetSecret})
	if err != nil {
		t.Fatal(err)
	}
	if prompted {
		t.Error("disabled manager should return prompted=false")
	}
}

func TestRequireApproval_AutoApproved_Cache(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100, ApprovalWindow: time.Second})
	sender := SenderInfo{Sender: new(sender.UniqueName(":1.1"))}
	items := []ItemInfo{{Path: "/test/item"}}

	// Manually cache
	mgr.CacheItemForSender(sender, items[0].Path)

	prompted, err := mgr.RequireApproval(context.Background(), RequestInfo{Items: items, Type: RequestTypeGetSecret, SenderInfo: sender})
	if err != nil {
		t.Fatal(err)
	}
	if prompted {
		t.Error("cache hit should return prompted=false")
	}
}

func TestRequireApproval_AutoApproved_TrustRule(t *testing.T) {
	mgr := NewManager(ManagerConfig{
		Timeout:    5 * time.Second,
		HistoryMax: 100,
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
	if err != nil {
		t.Fatal(err)
	}
	if prompted {
		t.Error("trust rule match should return prompted=false")
	}
}

func TestRequireApproval_AutoApproved_AutoApproveRule(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
	mustAddRule(t, mgr, &Request{
		ID:         "r1",
		Type:       RequestTypeGetSecret,
		Items:      []ItemInfo{{Path: "/org/freedesktop/secrets/collection/default/i1"}},
		SenderInfo: testSender("gh", "/usr/bin/gh"),
	})

	prompted, err := mgr.RequireApproval(context.Background(), RequestInfo{
		Items:      []ItemInfo{{Path: "/org/freedesktop/secrets/collection/default/i2"}},
		Type:       RequestTypeGetSecret,
		SenderInfo: testSender("gh", "/usr/bin/gh"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if prompted {
		t.Error("auto-approve rule match should return prompted=false")
	}
}

func TestRequireApproval_NotAutoApproved_ManualApproval(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})

	var prompted bool
	var approvalErr error
	var wg sync.WaitGroup
	wg.Go(func() {
		prompted, approvalErr = mgr.RequireApproval(context.Background(), RequestInfo{Items: []ItemInfo{{Path: "/test/item"}}, Type: RequestTypeGetSecret})
	})

	var reqID RequestID
	for range 100 {
		reqs := mgr.List()
		if len(reqs) > 0 {
			reqID = reqs[0].ID
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if reqID == "" {
		t.Fatal("request did not appear")
	}
	mgr.Approve(reqID)
	wg.Wait()

	if approvalErr != nil {
		t.Fatal(approvalErr)
	}
	if !prompted {
		t.Error("manual approval should return prompted=true")
	}
}

func TestPassthrough(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: time.Second, HistoryMax: 100})
	obs := &testObserver{}
	mgr.Subscribe(obs)

	items := []ItemInfo{{Label: "xdg:schema=org.gnome.keyring.Note"}}
	searchAttrs := map[string]string{"xdg:schema": "org.gnome.keyring.Note"}
	sender := SenderInfo{Client: "test-client", Sender: new(sender.UniqueName(":1.42")), InvokerName: "app.service", PID: 123}

	require.NoError(t, mgr.Passthrough(RequestInfo{Items: items, Type: RequestTypeSearch, Payload: SearchPayload{Attributes: searchAttrs}, SenderInfo: sender}))

	// Check event fired
	events := obs.WaitForEvents(t.Context(), 1, time.Second)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != evAutoApproved {
		t.Errorf("expected evAutoApproved, got %v", events[0].Type)
	}
	if events[0].Request.Type != RequestTypeSearch {
		t.Errorf("expected request type search, got %s", events[0].Request.Type)
	}

	// Check history entry
	history := mgr.History()
	if len(history) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(history))
	}
	if history[0].Resolution != ResolutionAutoApproved {
		t.Errorf("expected resolution 'auto_approved', got '%s'", history[0].Resolution)
	}
	if history[0].Request.SenderInfo.Client != "test-client" {
		t.Errorf("expected client 'test-client', got '%s'", history[0].Request.SenderInfo.Client)
	}
	assert.Equal(t, sender.Sender, history[0].Request.SenderInfo.Sender)
	if search, _ := history[0].Request.Search(); search.Attributes["xdg:schema"] != "org.gnome.keyring.Note" {
		t.Errorf("expected search attributes preserved, got %v", search.Attributes)
	}

	// No pending requests
	if mgr.PendingCount() != 0 {
		t.Error("passthrough request should not be pending")
	}

	mgr.Unsubscribe(obs)

	// Test with unlock type
	mgr2 := NewManager(ManagerConfig{Timeout: time.Second, HistoryMax: 100})
	obs2 := &testObserver{}
	mgr2.Subscribe(obs2)

	unlockItems := []ItemInfo{{Path: "/org/freedesktop/secrets/collection/login", Label: "Login"}}
	require.NoError(t, mgr2.Passthrough(RequestInfo{Items: unlockItems, Type: RequestTypeUnlock, SenderInfo: sender}))

	history2 := mgr2.History()
	if len(history2) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(history2))
	}
	if history2[0].Request.Type != RequestTypeUnlock {
		t.Errorf("expected request type unlock, got %s", history2[0].Request.Type)
	}
	if history2[0].Request.Items[0].Path != "/org/freedesktop/secrets/collection/login" {
		t.Errorf("expected item path preserved, got %s", history2[0].Request.Items[0].Path)
	}

	mgr2.Unsubscribe(obs2)
}

func TestPassthrough_DenyRule(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: time.Second, HistoryMax: 100, TrustRules: []TrustRule{
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
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})

	var prompted bool
	var approvalErr error
	var wg sync.WaitGroup
	wg.Go(func() {
		prompted, approvalErr = mgr.RequireApproval(context.Background(), RequestInfo{Items: []ItemInfo{{Path: "/test/item"}}, Type: RequestTypeGetSecret})
	})

	var reqID RequestID
	for range 100 {
		reqs := mgr.List()
		if len(reqs) > 0 {
			reqID = reqs[0].ID
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if reqID == "" {
		t.Fatal("request did not appear")
	}
	mgr.Deny(reqID)
	wg.Wait()

	if approvalErr != ErrDenied {
		t.Fatalf("expected ErrDenied, got %v", approvalErr)
	}
	if prompted {
		t.Error("denied request should return prompted=false")
	}
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
	mgr := NewManager(ManagerConfig{
		Timeout:    5 * time.Second,
		HistoryMax: 100,
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
		rule := mgr.CheckTrustRules(RequestInfo{Type: c.typ, SenderInfo: SenderInfo{Client: c.client}})
		got := ""
		if rule != nil {
			got = rule.Name
		}
		assert.Equal(t, c.want, got, "client %q, type %q", c.client, c.typ)
	}
}

// A forwarded bus's sender has no local process, so no process block matches
// it, not even one that would match anything.
func TestCheckTrustRules_ProcessFailsClosedWithoutLocalProcess(t *testing.T) {
	mgr := NewManager(ManagerConfig{
		Timeout:    5 * time.Second,
		HistoryMax: 100,
		TrustRules: []TrustRule{
			{Name: "any-unit", Action: RuleApprove, Client: MustGlob("*"), RequestTypes: []RequestType{RequestTypeGetSecret}, Process: &ProcessMatcher{Unit: MustGlob("*")}},
			{Name: "empty-process", Action: RuleApprove, Client: MustGlob("*"), RequestTypes: []RequestType{RequestTypeSearch}, Process: &ProcessMatcher{}},
		},
	})

	forwarded := SenderInfo{Client: "symbiosis", Sender: new(sender.UniqueName(":1.7")), PID: 42}
	assert.Nil(t, mgr.CheckTrustRules(RequestInfo{Type: RequestTypeGetSecret, SenderInfo: forwarded}), `unit "*"`)
	assert.Nil(t, mgr.CheckTrustRules(RequestInfo{Type: RequestTypeSearch, SenderInfo: forwarded}), "empty process")

	local := SenderInfo{Client: ClientLocal, SystemdUnit: "app.service"}
	assert.NotNil(t, mgr.CheckTrustRules(RequestInfo{Type: RequestTypeGetSecret, SenderInfo: local}), `unit "*" on a local sender`)
}

// An expired request ends exactly once. It leaves no pending request and one
// expired entry in History.
func TestManager_RequireApproval_ExpiryEndsRequestOnce(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 20 * time.Millisecond, HistoryMax: 100})

	_, err := mgr.RequireApproval(t.Context(), RequestInfo{Items: []ItemInfo{{Path: "/test/item"}}, Type: RequestTypeGetSecret})

	require.ErrorIs(t, err, ErrTimeout)
	assert.Zero(t, mgr.PendingCount())
	history := mgr.History()
	require.Len(t, history, 1)
	assert.Equal(t, ResolutionExpired, history[0].Resolution)
}

// A request resolved before its timeout is not expired later.
func TestManager_ResolvedRequestDoesNotExpire(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 20 * time.Millisecond, HistoryMax: 100})
	id := mgr.Create(sampleGPGSignRequest())

	require.NoError(t, mgr.Deny(id))
	time.Sleep(60 * time.Millisecond)

	history := mgr.History()
	require.Len(t, history, 1)
	assert.Equal(t, ResolutionDenied, history[0].Resolution)
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
