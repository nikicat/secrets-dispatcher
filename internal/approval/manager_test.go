package approval

import (
	"context"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestManager_RequireApproval_Approved(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})

	var wg sync.WaitGroup
	var approvalErr error

	wg.Add(1)
	go func() {
		defer wg.Done()
		approvalErr = mgr.RequireApproval(context.Background(), "test-client", []ItemInfo{{Path: "/test/item"}}, "/session/1", RequestTypeGetSecret, nil, SenderInfo{})
	}()

	// Wait for request to appear
	var reqID string
	for i := 0; i < 100; i++ {
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

	wg.Add(1)
	go func() {
		defer wg.Done()
		approvalErr = mgr.RequireApproval(context.Background(), "test-client", []ItemInfo{{Path: "/test/item"}}, "/session/1", RequestTypeGetSecret, nil, SenderInfo{})
	}()

	// Wait for request to appear
	var reqID string
	for i := 0; i < 100; i++ {
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

	err := mgr.RequireApproval(context.Background(), "test-client", []ItemInfo{{Path: "/test/item"}}, "/session/1", RequestTypeGetSecret, nil, SenderInfo{})

	if err != ErrTimeout {
		t.Errorf("expected ErrTimeout, got: %v", err)
	}
}

func TestManager_RequireApproval_ContextCanceled(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})

	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	var approvalErr error

	wg.Add(1)
	go func() {
		defer wg.Done()
		approvalErr = mgr.RequireApproval(ctx, "test-client", []ItemInfo{{Path: "/test/item"}}, "/session/1", RequestTypeGetSecret, nil, SenderInfo{})
	}()

	// Wait for request to appear
	for i := 0; i < 100; i++ {
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
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			mgr.RequireApproval(ctx, "test-client", []ItemInfo{{Path: "/test/item"}}, "/session/1", RequestTypeGetSecret, nil, SenderInfo{})
		}(i)
	}

	// Wait for requests to appear
	for i := 0; i < 100; i++ {
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

	wg.Add(1)
	go func() {
		defer wg.Done()
		approvalErr = mgr.RequireApproval(context.Background(), "test-client", []ItemInfo{{Path: "/test/item"}}, "/session/1", RequestTypeGetSecret, nil, SenderInfo{})
	}()

	// Wait for request to appear
	var reqID string
	for i := 0; i < 100; i++ {
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

	// Cancel closes the done channel with result=false, triggering the ctx.Done or done branch.
	// RequireApproval sees done channel closed with result=false => ErrDenied.
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
	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			results[n] = mgr.RequireApproval(context.Background(), "test-client", []ItemInfo{{Path: "/test/item"}}, "/session/1", RequestTypeGetSecret, nil, SenderInfo{})
		}(i)
	}

	// Wait for requests to appear
	for i := 0; i < 100; i++ {
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
	err := mgr.RequireApproval(context.Background(), "test-client", []ItemInfo{{Path: "/test/item"}}, "/session/1", RequestTypeGetSecret, nil, SenderInfo{})
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
	wg.Add(1)
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		mgr.RequireApproval(ctx, "test-client", []ItemInfo{{Path: "/test/item1"}, {Path: "/test/item2"}}, "/session/42", RequestTypeGetSecret, nil, SenderInfo{})
	}()

	// Wait for request to appear
	for i := 0; i < 100; i++ {
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
	if req.Client != "test-client" {
		t.Errorf("expected client 'test-client', got '%s'", req.Client)
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
	wg.Add(1)
	go func() {
		defer wg.Done()
		mgr.RequireApproval(context.Background(), "test-client", []ItemInfo{{Path: "/test/item"}}, "/session/1", RequestTypeGetSecret, nil, SenderInfo{})
	}()

	// Wait for request to appear
	var reqID string
	for i := 0; i < 100; i++ {
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

// testObserver collects events for testing.
type testObserver struct {
	mu     sync.Mutex
	events []Event
}

func (o *testObserver) OnEvent(e Event) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, e)
}

func (o *testObserver) Events() []Event {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]Event{}, o.events...)
}

func (o *testObserver) WaitForEvents(count int, timeout time.Duration) []Event {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		events := o.Events()
		if len(events) >= count {
			return events
		}
		time.Sleep(10 * time.Millisecond)
	}
	return o.Events()
}

func TestManager_Observer_Created(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
	obs := &testObserver{}
	mgr.Subscribe(obs)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		mgr.RequireApproval(ctx, "test-client", []ItemInfo{{Path: "/test/item"}}, "/session/1", RequestTypeGetSecret, nil, SenderInfo{})
	}()

	// Wait for created event
	events := obs.WaitForEvents(1, time.Second)
	if len(events) < 1 {
		t.Fatal("expected at least 1 event")
	}
	if events[0].Type != EventRequestCreated {
		t.Errorf("expected EventRequestCreated, got %v", events[0].Type)
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
	wg.Add(1)
	go func() {
		defer wg.Done()
		mgr.RequireApproval(context.Background(), "test-client", []ItemInfo{{Path: "/test/item"}}, "/session/1", RequestTypeGetSecret, nil, SenderInfo{})
	}()

	// Wait for request to appear
	var reqID string
	for i := 0; i < 100; i++ {
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
	events := obs.WaitForEvents(2, time.Second)
	if len(events) < 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	if events[0].Type != EventRequestCreated {
		t.Errorf("expected first event to be Created, got %v", events[0].Type)
	}
	if events[1].Type != EventRequestApproved {
		t.Errorf("expected second event to be Approved, got %v", events[1].Type)
	}

	mgr.Unsubscribe(obs)
}

func TestManager_Observer_Denied(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
	obs := &testObserver{}
	mgr.Subscribe(obs)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		mgr.RequireApproval(context.Background(), "test-client", []ItemInfo{{Path: "/test/item"}}, "/session/1", RequestTypeGetSecret, nil, SenderInfo{})
	}()

	// Wait for request to appear
	var reqID string
	for i := 0; i < 100; i++ {
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
	events := obs.WaitForEvents(2, time.Second)
	if len(events) < 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	if events[1].Type != EventRequestDenied {
		t.Errorf("expected second event to be Denied, got %v", events[1].Type)
	}

	mgr.Unsubscribe(obs)
}

func TestManager_Observer_Expired(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 100 * time.Millisecond, HistoryMax: 100})
	obs := &testObserver{}
	mgr.Subscribe(obs)

	// This will timeout
	mgr.RequireApproval(context.Background(), "test-client", []ItemInfo{{Path: "/test/item"}}, "/session/1", RequestTypeGetSecret, nil, SenderInfo{})

	// Wait for events
	events := obs.WaitForEvents(2, time.Second)
	if len(events) < 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	if events[0].Type != EventRequestCreated {
		t.Errorf("expected first event to be Created, got %v", events[0].Type)
	}
	if events[1].Type != EventRequestExpired {
		t.Errorf("expected second event to be Expired, got %v", events[1].Type)
	}

	mgr.Unsubscribe(obs)
}

func TestManager_Observer_CancelMethod(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
	obs := &testObserver{}
	mgr.Subscribe(obs)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		mgr.RequireApproval(context.Background(), "test-client", []ItemInfo{{Path: "/test/item"}}, "/session/1", RequestTypeGetSecret, nil, SenderInfo{})
	}()

	// Wait for request to appear
	var reqID string
	for i := 0; i < 100; i++ {
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
	events := obs.WaitForEvents(2, time.Second)
	if len(events) < 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	if events[0].Type != EventRequestCreated {
		t.Errorf("expected first event to be Created, got %v", events[0].Type)
	}
	if events[1].Type != EventRequestCancelled {
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
	wg.Add(1)
	go func() {
		defer wg.Done()
		err := mgr.RequireApproval(ctx, "test-client", []ItemInfo{{Path: "/test/item"}}, "/session/1", RequestTypeGetSecret, nil, SenderInfo{})
		if err != context.Canceled {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	}()

	// Wait for created event
	events := obs.WaitForEvents(1, time.Second)
	if len(events) < 1 {
		t.Fatal("expected at least 1 event (created)")
	}

	// Cancel the context
	cancel()
	wg.Wait()

	// Wait for cancelled event
	events = obs.WaitForEvents(2, time.Second)
	if len(events) < 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	if events[0].Type != EventRequestCreated {
		t.Errorf("expected first event to be Created, got %v", events[0].Type)
	}
	if events[1].Type != EventRequestCancelled {
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
	wg.Add(1)
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		mgr.RequireApproval(ctx, "test-client", []ItemInfo{{Path: "/test/item"}}, "/session/1", RequestTypeGetSecret, nil, SenderInfo{})
	}()

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
	wg.Add(1)
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		mgr.RequireApproval(ctx, "test-client", []ItemInfo{{Path: "/test/item"}}, "/session/1", RequestTypeGetSecret, nil, SenderInfo{})
	}()

	// Wait for created event on both observers
	events1 := obs1.WaitForEvents(1, time.Second)
	events2 := obs2.WaitForEvents(1, time.Second)

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
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			obs := &testObserver{}
			mgr.Subscribe(obs)
			subscribed.Add(1)
		}()
	}

	wg.Wait()

	if subscribed.Load() != 10 {
		t.Errorf("expected 10 subscribed, got %d", subscribed.Load())
	}
}

func TestHistory_RecordsApproved(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		mgr.RequireApproval(context.Background(), "test-client", []ItemInfo{{Path: "/test/item"}}, "/session/1", RequestTypeGetSecret, nil, SenderInfo{})
	}()

	// Wait for request to appear
	var reqID string
	for i := 0; i < 100; i++ {
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
	wg.Add(1)
	go func() {
		defer wg.Done()
		mgr.RequireApproval(context.Background(), "test-client", []ItemInfo{{Path: "/test/item"}}, "/session/1", RequestTypeGetSecret, nil, SenderInfo{})
	}()

	// Wait for request to appear
	var reqID string
	for i := 0; i < 100; i++ {
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
	mgr.RequireApproval(context.Background(), "test-client", []ItemInfo{{Path: "/test/item"}}, "/session/1", RequestTypeGetSecret, nil, SenderInfo{})

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
	wg.Add(1)
	go func() {
		defer wg.Done()
		mgr.RequireApproval(ctx, "test-client", []ItemInfo{{Path: "/test/item"}}, "/session/1", RequestTypeGetSecret, nil, SenderInfo{})
	}()

	// Wait for request to appear
	for i := 0; i < 100; i++ {
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
		mgr.RequireApproval(context.Background(), "test-client", []ItemInfo{{Path: "/test/item"}}, "/session/1", RequestTypeGetSecret, nil, SenderInfo{})
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
	for i := 0; i < 3; i++ {
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			mgr.RequireApproval(context.Background(), "test-client", []ItemInfo{{Path: "/test/item"}}, "/session/1", RequestTypeGetSecret, nil, SenderInfo{})
		}()

		// Wait for request to appear
		var reqID string
		for j := 0; j < 100; j++ {
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
	sender := SenderInfo{Sender: ":1.100"}

	// First request: must be manually approved
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		mgr.RequireApproval(context.Background(), "client", items, "/s/1", RequestTypeGetSecret, nil, sender)
	}()

	var reqID string
	for i := 0; i < 100; i++ {
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

	// Second request: same sender+item within window → auto-approved (no pending)
	err := mgr.RequireApproval(context.Background(), "client", items, "/s/1", RequestTypeGetSecret, nil, sender)
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
	sender := SenderInfo{Sender: ":1.100"}

	// Approve first request
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		mgr.RequireApproval(context.Background(), "client", items, "/s/1", RequestTypeGetSecret, nil, sender)
	}()

	var reqID string
	for i := 0; i < 100; i++ {
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
	err := mgr.RequireApproval(ctx, "client", items, "/s/1", RequestTypeGetSecret, nil, sender)
	if err == nil {
		t.Fatal("expected approval to be required after cache expiry")
	}
}

func TestApprovalCache_DifferentSender(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100, ApprovalWindow: time.Second})

	items := []ItemInfo{{Path: "/test/item1"}}

	// Approve for sender1
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		mgr.RequireApproval(context.Background(), "client", items, "/s/1", RequestTypeGetSecret, nil, SenderInfo{Sender: ":1.100"})
	}()

	var reqID string
	for i := 0; i < 100; i++ {
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
	err := mgr.RequireApproval(ctx, "client", items, "/s/1", RequestTypeGetSecret, nil, SenderInfo{Sender: ":1.999"})
	if err == nil {
		t.Fatal("different sender should not be auto-approved from cache")
	}
}

func TestApprovalCache_DeleteBypassesCache(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100, ApprovalWindow: time.Second})

	items := []ItemInfo{{Path: "/test/item1"}}
	sender := SenderInfo{Sender: ":1.100"}

	// Approve a GetSecret request first — populates cache
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		mgr.RequireApproval(context.Background(), "client", items, "/s/1", RequestTypeGetSecret, nil, sender)
	}()

	var reqID string
	for i := 0; i < 100; i++ {
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
	err := mgr.RequireApproval(context.Background(), "client", items, "/s/1", RequestTypeGetSecret, nil, sender)
	if err != nil {
		t.Fatalf("GetSecret should be auto-approved from cache, got: %v", err)
	}

	// Delete should NOT use cache — must create a pending request
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err = mgr.RequireApproval(ctx, "client", items, "/s/1", RequestTypeDelete, nil, sender)
	if err == nil {
		t.Fatal("Delete should not be auto-approved from cache")
	}
}

func TestApprovalCache_WriteBypassesCache(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100, ApprovalWindow: time.Second})

	items := []ItemInfo{{Path: "/test/item1"}}
	sender := SenderInfo{Sender: ":1.100"}

	// Approve a GetSecret request first — populates cache
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		mgr.RequireApproval(context.Background(), "client", items, "/s/1", RequestTypeGetSecret, nil, sender)
	}()

	var reqID string
	for i := 0; i < 100; i++ {
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
	err := mgr.RequireApproval(ctx, "client", items, "/s/1", RequestTypeWrite, nil, sender)
	if err == nil {
		t.Fatal("Write should not be auto-approved from cache")
	}
}

func TestCacheItemForSender_AutoApprovesRead(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100, ApprovalWindow: time.Second})

	sender := SenderInfo{Sender: ":1.200"}
	itemPath := "/org/freedesktop/secrets/collection/default/i123"

	// Manually cache the item (simulates post-CreateItem caching)
	mgr.CacheItemForSender(sender.Sender, itemPath)

	// GetSecret for the same item should be auto-approved
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := mgr.RequireApproval(ctx, "client", []ItemInfo{{Path: itemPath}}, "/s/1", RequestTypeGetSecret, nil, sender)
	if err != nil {
		t.Fatalf("GetSecret should be auto-approved after CacheItemForSender, got: %v", err)
	}
}

func TestCacheItemForSender_DifferentSenderNotCached(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100, ApprovalWindow: time.Second})

	itemPath := "/org/freedesktop/secrets/collection/default/i123"

	// Cache for sender :1.200
	mgr.CacheItemForSender(":1.200", itemPath)

	// Different sender should NOT be auto-approved
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := mgr.RequireApproval(ctx, "client", []ItemInfo{{Path: itemPath}}, "/s/1", RequestTypeGetSecret, nil, SenderInfo{Sender: ":1.300"})
	if err == nil {
		t.Fatal("different sender should not be auto-approved")
	}
}

func TestApprovalCache_DifferentItem(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100, ApprovalWindow: time.Second})

	sender := SenderInfo{Sender: ":1.100"}

	// Approve for item1
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		mgr.RequireApproval(context.Background(), "client", []ItemInfo{{Path: "/test/item1"}}, "/s/1", RequestTypeGetSecret, nil, sender)
	}()

	var reqID string
	for i := 0; i < 100; i++ {
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
	err := mgr.RequireApproval(ctx, "client", []ItemInfo{{Path: "/test/item2"}}, "/s/1", RequestTypeGetSecret, nil, sender)
	if err == nil {
		t.Fatal("different item should not be auto-approved from cache")
	}
}

func TestManager_EventOrder_CancelledContext(t *testing.T) {
	// Regression test: with async notify (go o.OnEvent), EventRequestCancelled
	// can arrive before EventRequestCreated when the context is already cancelled.
	// The client processes request_cancelled first (no-op, request not in list),
	// then request_created (adds it), leaving a stale request in the UI forever.
	for i := 0; i < 100; i++ {
		mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
		obs := &testObserver{}
		mgr.Subscribe(obs)

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // pre-cancel so select fires ctx.Done() immediately

		mgr.RequireApproval(ctx, "client", []ItemInfo{{Path: "/test/item"}}, "/s/1", RequestTypeGetSecret, nil, SenderInfo{})

		events := obs.WaitForEvents(2, time.Second)
		if len(events) < 2 {
			t.Fatalf("iteration %d: expected 2 events, got %d", i, len(events))
		}
		if events[0].Type != EventRequestCreated {
			t.Fatalf("iteration %d: expected EventRequestCreated first, got %v then %v", i, events[0].Type, events[1].Type)
		}
		if events[1].Type != EventRequestCancelled {
			t.Fatalf("iteration %d: expected EventRequestCancelled second, got %v then %v", i, events[0].Type, events[1].Type)
		}

		mgr.Unsubscribe(obs)
	}
}

func TestShouldIgnore_ChromeDummy(t *testing.T) {
	chromeItems := []ItemInfo{{
		Path:       "/org/freedesktop/secrets/collection/default/123",
		Label:      "Chrome Safe Storage",
		Attributes: map[string]string{"xdg:schema": "_chrome_dummy_schema_for_unlocking"},
	}}
	normalItems := []ItemInfo{{
		Path:       "/org/freedesktop/secrets/collection/default/456",
		Label:      "My Secret",
		Attributes: map[string]string{"xdg:schema": "org.gnome.keyring.Note"},
	}}

	t.Run("enabled + write + matching schema", func(t *testing.T) {
		mgr := NewManager(ManagerConfig{Timeout: time.Second, HistoryMax: 10, IgnoreChromeDummy: true})
		if !mgr.ShouldIgnore(chromeItems, RequestTypeWrite) {
			t.Error("expected true")
		}
	})
	t.Run("enabled + read + matching schema", func(t *testing.T) {
		mgr := NewManager(ManagerConfig{Timeout: time.Second, HistoryMax: 10, IgnoreChromeDummy: true})
		if mgr.ShouldIgnore(chromeItems, RequestTypeGetSecret) {
			t.Error("expected false for non-write")
		}
	})
	t.Run("enabled + write + different schema", func(t *testing.T) {
		mgr := NewManager(ManagerConfig{Timeout: time.Second, HistoryMax: 10, IgnoreChromeDummy: true})
		if mgr.ShouldIgnore(normalItems, RequestTypeWrite) {
			t.Error("expected false for non-chrome schema")
		}
	})
	t.Run("disabled + write + matching schema", func(t *testing.T) {
		mgr := NewManager(ManagerConfig{Timeout: time.Second, HistoryMax: 10, IgnoreChromeDummy: false})
		if mgr.ShouldIgnore(chromeItems, RequestTypeWrite) {
			t.Error("expected false when disabled")
		}
	})
}

func TestRecordIgnored(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: time.Second, HistoryMax: 10, IgnoreChromeDummy: true})
	obs := &testObserver{}
	mgr.Subscribe(obs)

	items := []ItemInfo{{
		Path:       "/org/freedesktop/secrets/collection/default/123",
		Label:      "Chrome Safe Storage",
		Attributes: map[string]string{"xdg:schema": "_chrome_dummy_schema_for_unlocking"},
	}}

	mgr.RecordIgnored("test-client", items, "/session/1", SenderInfo{})

	// Check event fired
	events := obs.WaitForEvents(1, time.Second)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != EventRequestIgnored {
		t.Errorf("expected EventRequestIgnored, got %v", events[0].Type)
	}

	// Check history entry
	history := mgr.History()
	if len(history) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(history))
	}
	if history[0].Resolution != ResolutionIgnored {
		t.Errorf("expected resolution 'ignored', got '%s'", history[0].Resolution)
	}
	if history[0].Request.Client != "test-client" {
		t.Errorf("expected client 'test-client', got '%s'", history[0].Request.Client)
	}

	// No pending requests
	if mgr.PendingCount() != 0 {
		t.Error("ignored request should not be pending")
	}

	mgr.Unsubscribe(obs)
}

func TestCheckTrustedSigner_WalksProcessChain(t *testing.T) {
	// Use the current test process as the "trusted" exe.
	selfExe, err := os.Readlink("/proc/self/exe")
	if err != nil {
		t.Skipf("cannot read /proc/self/exe: %v", err)
	}
	selfPID := uint32(os.Getpid())

	mgr := NewManager(ManagerConfig{
		Timeout:    5 * time.Second,
		HistoryMax: 100,
		TrustedSigners: []TrustedSigner{
			{ExePath: selfExe, RepoPath: "my-repo"},
		},
	})
	selfChain := []ProcessInfo{
		{Name: "thin-client", PID: 1},
		{Name: "git", PID: 2},
		{Name: "test-binary", PID: selfPID},
	}

	t.Run("match in chain", func(t *testing.T) {
		info := SenderInfo{ProcessChain: selfChain}
		if !mgr.CheckTrustedSigner(info, "my-repo", nil) {
			t.Error("expected match")
		}
	})

	t.Run("no match wrong repo", func(t *testing.T) {
		info := SenderInfo{ProcessChain: selfChain}
		if mgr.CheckTrustedSigner(info, "other-repo", nil) {
			t.Error("expected no match for wrong repo")
		}
	})

	t.Run("no match wrong exe", func(t *testing.T) {
		info := SenderInfo{ProcessChain: []ProcessInfo{{Name: "git", PID: 1}}}
		if mgr.CheckTrustedSigner(info, "my-repo", nil) {
			t.Error("expected no match for wrong exe")
		}
	})

	t.Run("empty chain", func(t *testing.T) {
		if mgr.CheckTrustedSigner(SenderInfo{}, "my-repo", nil) {
			t.Error("expected no match for empty chain")
		}
	})

	t.Run("empty repo matches any", func(t *testing.T) {
		m := NewManager(ManagerConfig{
			Timeout:        5 * time.Second,
			HistoryMax:     100,
			TrustedSigners: []TrustedSigner{{ExePath: selfExe}},
		})
		info := SenderInfo{ProcessChain: selfChain}
		if !m.CheckTrustedSigner(info, "any-repo", nil) {
			t.Error("expected match with empty repo_path")
		}
	})

	t.Run("file prefix match", func(t *testing.T) {
		m := NewManager(ManagerConfig{
			Timeout:    5 * time.Second,
			HistoryMax: 100,
			TrustedSigners: []TrustedSigner{
				{ExePath: selfExe, FilePrefix: "secret-service/"},
			},
		})
		info := SenderInfo{ProcessChain: selfChain}
		if !m.CheckTrustedSigner(info, "any", []string{"secret-service/default/i1.age", "secret-service/login/i2.age"}) {
			t.Error("expected match when all files under prefix")
		}
	})

	t.Run("file prefix reject", func(t *testing.T) {
		m := NewManager(ManagerConfig{
			Timeout:    5 * time.Second,
			HistoryMax: 100,
			TrustedSigners: []TrustedSigner{
				{ExePath: selfExe, FilePrefix: "secret-service/"},
			},
		})
		info := SenderInfo{ProcessChain: selfChain}
		if m.CheckTrustedSigner(info, "any", []string{"secret-service/default/i1.age", "other/sneaky.gpg"}) {
			t.Error("expected no match when a file is outside prefix")
		}
	})
}

func TestCheckTrustRules(t *testing.T) {
	// Override extractCollection for tests
	origExtract := extractCollection
	extractCollection = func(path string) string {
		// Simple parser: /org/freedesktop/secrets/collection/<name>/... → <name>
		parts := strings.Split(path, "/")
		if len(parts) >= 6 {
			return parts[5]
		}
		return ""
	}
	defer func() { extractCollection = origExtract }()

	rules := []TrustRule{
		{
			Name:         "approve-gh-search",
			Action:       "approve",
			RequestTypes: []string{"search"},
			Process:      &ProcessMatcher{Name: "gh"},
		},
		{
			Name:         "ignore-chrome-write",
			Action:       "ignore",
			RequestTypes: []string{"write"},
			Process:      &ProcessMatcher{Exe: "/opt/google/chrome/chrome"},
			Secret:       &SecretMatcher{Collection: "default"},
		},
		{
			Name:    "approve-all-from-unit",
			Action:  "approve",
			Process: &ProcessMatcher{Unit: "gopass-*"},
		},
		{
			Name:   "approve-by-label",
			Action: "approve",
			Secret: &SecretMatcher{Label: "GitHub*"},
		},
		{
			Name:             "approve-search-attrs",
			Action:           "approve",
			RequestTypes:     []string{"search"},
			SearchAttributes: map[string]string{"xdg:schema": "org.gnome.keyring.Note"},
		},
		{
			Name:   "approve-by-attributes",
			Action: "approve",
			Secret: &SecretMatcher{
				Attributes: map[string]string{"xdg:schema": "org.gnome.keyring.NetworkPassword"},
			},
		},
	}

	mgr := NewManager(ManagerConfig{
		Timeout:    5 * time.Second,
		HistoryMax: 100,
		TrustRules: rules,
	})

	ghChain := []ProcessInfo{{Name: "gh", PID: 100, Exe: "/usr/bin/gh"}}
	chromeChain := []ProcessInfo{{Name: "chrome", PID: 200, Exe: "/opt/google/chrome/chrome"}}
	gopassSender := SenderInfo{UnitName: "gopass-secret-service.service", ProcessChain: []ProcessInfo{{Name: "gopass", PID: 300}}}

	t.Run("match process name + request type", func(t *testing.T) {
		rule := mgr.CheckTrustRules(
			SenderInfo{ProcessChain: ghChain},
			nil,
			RequestTypeSearch,
			nil,
		)
		if rule == nil || rule.Name != "approve-gh-search" {
			t.Errorf("expected approve-gh-search, got %v", rule)
		}
	})

	t.Run("no match wrong request type", func(t *testing.T) {
		rule := mgr.CheckTrustRules(
			SenderInfo{ProcessChain: ghChain},
			nil,
			RequestTypeGetSecret,
			nil,
		)
		// gh doing get_secret: doesn't match rule 0 (search only), no other process match
		// Could match rule 3 (label) or rule 5 (attributes) depending on items
		// With nil items, no label or attributes to match, so no match
		if rule != nil {
			t.Errorf("expected no match, got %v", rule.Name)
		}
	})

	t.Run("match exe + collection for ignore", func(t *testing.T) {
		items := []ItemInfo{{
			Path:  "/org/freedesktop/secrets/collection/default/123",
			Label: "Chrome Safe Storage",
		}}
		rule := mgr.CheckTrustRules(
			SenderInfo{ProcessChain: chromeChain},
			items,
			RequestTypeWrite,
			nil,
		)
		if rule == nil || rule.Name != "ignore-chrome-write" {
			t.Errorf("expected ignore-chrome-write, got %v", rule)
		}
	})

	t.Run("no match wrong collection", func(t *testing.T) {
		items := []ItemInfo{{
			Path:  "/org/freedesktop/secrets/collection/login/123",
			Label: "Chrome Safe Storage",
		}}
		rule := mgr.CheckTrustRules(
			SenderInfo{ProcessChain: chromeChain},
			items,
			RequestTypeWrite,
			nil,
		)
		// Chrome write to "login" collection: rule 1 requires "default", so no match
		if rule != nil {
			t.Errorf("expected no match, got %v", rule.Name)
		}
	})

	t.Run("match unit glob", func(t *testing.T) {
		rule := mgr.CheckTrustRules(
			gopassSender,
			[]ItemInfo{{Path: "/org/freedesktop/secrets/collection/default/1"}},
			RequestTypeGetSecret,
			nil,
		)
		if rule == nil || rule.Name != "approve-all-from-unit" {
			t.Errorf("expected approve-all-from-unit, got %v", rule)
		}
	})

	t.Run("match label glob", func(t *testing.T) {
		items := []ItemInfo{{
			Path:  "/org/freedesktop/secrets/collection/default/1",
			Label: "GitHub Token",
		}}
		rule := mgr.CheckTrustRules(
			SenderInfo{ProcessChain: []ProcessInfo{{Name: "random-app", PID: 999}}},
			items,
			RequestTypeGetSecret,
			nil,
		)
		if rule == nil || rule.Name != "approve-by-label" {
			t.Errorf("expected approve-by-label, got %v", rule)
		}
	})

	t.Run("match search attributes", func(t *testing.T) {
		rule := mgr.CheckTrustRules(
			SenderInfo{ProcessChain: []ProcessInfo{{Name: "app", PID: 1}}},
			nil,
			RequestTypeSearch,
			map[string]string{"xdg:schema": "org.gnome.keyring.Note"},
		)
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
		rule := mgr.CheckTrustRules(
			SenderInfo{ProcessChain: []ProcessInfo{{Name: "nm-applet", PID: 500}}},
			items,
			RequestTypeGetSecret,
			nil,
		)
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
					Attributes: map[string]string{"username": "kubelogin/tokencache/*"},
				},
			}},
		})
		items := []ItemInfo{{
			Path:       "/org/freedesktop/secrets/collection/default/1",
			Label:      "token",
			Attributes: map[string]string{"username": "kubelogin/tokencache/e91d908"},
		}}
		rule := m.CheckTrustRules(
			SenderInfo{ProcessChain: []ProcessInfo{{Name: "kubectl", PID: 1}}},
			items,
			RequestTypeGetSecret,
			nil,
		)
		if rule == nil || rule.Name != "approve-glob-attrs" {
			t.Errorf("expected approve-glob-attrs, got %v", rule)
		}
		// Non-matching value
		items[0].Attributes["username"] = "other/path"
		rule = m.CheckTrustRules(
			SenderInfo{ProcessChain: []ProcessInfo{{Name: "kubectl", PID: 1}}},
			items,
			RequestTypeGetSecret,
			nil,
		)
		if rule != nil {
			t.Errorf("expected nil for non-matching glob, got %v", rule)
		}
	})

	t.Run("first match wins", func(t *testing.T) {
		// gh doing search matches rule 0 (approve-gh-search), not rule 4 (approve-search-attrs)
		rule := mgr.CheckTrustRules(
			SenderInfo{ProcessChain: ghChain},
			nil,
			RequestTypeSearch,
			map[string]string{"xdg:schema": "org.gnome.keyring.Note"},
		)
		if rule == nil || rule.Name != "approve-gh-search" {
			t.Errorf("expected first match approve-gh-search, got %v", rule)
		}
	})

	t.Run("empty rules", func(t *testing.T) {
		m := NewManager(ManagerConfig{Timeout: time.Second, HistoryMax: 10})
		rule := m.CheckTrustRules(SenderInfo{}, nil, RequestTypeGetSecret, nil)
		if rule != nil {
			t.Errorf("expected nil from empty rules, got %v", rule)
		}
	})
}

func TestTrustRules_RequireApproval_Approve(t *testing.T) {
	mgr := NewManager(ManagerConfig{
		Timeout:    5 * time.Second,
		HistoryMax: 100,
		TrustRules: []TrustRule{
			{
				Name:         "approve-gh",
				Action:       "approve",
				RequestTypes: []string{"search"},
				Process:      &ProcessMatcher{Name: "gh"},
			},
		},
	})
	obs := &testObserver{}
	mgr.Subscribe(obs)

	err := mgr.RequireApproval(
		context.Background(), "client",
		nil, "/s/1",
		RequestTypeSearch, nil,
		SenderInfo{ProcessChain: []ProcessInfo{{Name: "gh", PID: 1}}},
	)
	if err != nil {
		t.Fatalf("expected nil (auto-approved), got %v", err)
	}
	if mgr.PendingCount() != 0 {
		t.Error("trust rule approve should not create pending request")
	}

	events := obs.WaitForEvents(1, time.Second)
	if len(events) < 1 || events[0].Type != EventRequestAutoApproved {
		t.Errorf("expected EventRequestAutoApproved, got %v", events)
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
				RequestTypes: []string{"write"},
				Secret:       &SecretMatcher{Attributes: map[string]string{"xdg:schema": "_chrome_dummy_schema_for_unlocking"}},
			},
		},
	})
	obs := &testObserver{}
	mgr.Subscribe(obs)

	items := []ItemInfo{{
		Path:       "/org/freedesktop/secrets/collection/default/1",
		Attributes: map[string]string{"xdg:schema": "_chrome_dummy_schema_for_unlocking"},
	}}
	err := mgr.RequireApproval(
		context.Background(), "client",
		items, "/s/1",
		RequestTypeWrite, nil,
		SenderInfo{},
	)
	if err != ErrIgnored {
		t.Fatalf("expected ErrIgnored, got %v", err)
	}
	if mgr.PendingCount() != 0 {
		t.Error("trust rule ignore should not create pending request")
	}

	events := obs.WaitForEvents(1, time.Second)
	if len(events) < 1 || events[0].Type != EventRequestIgnored {
		t.Errorf("expected EventRequestIgnored, got %v", events)
	}

	history := mgr.History()
	if len(history) != 1 || history[0].Resolution != ResolutionIgnored {
		t.Errorf("expected ignored in history, got %v", history)
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
				RequestTypes: []string{"search"},
				Process:      &ProcessMatcher{Name: "gh"},
			},
		},
	})

	// Non-matching request should go to pending and eventually timeout
	err := mgr.RequireApproval(
		context.Background(), "client",
		[]ItemInfo{{Path: "/test/item"}}, "/s/1",
		RequestTypeGetSecret, nil,
		SenderInfo{ProcessChain: []ProcessInfo{{Name: "curl", PID: 1}}},
	)
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
				Process: &ProcessMatcher{Name: "test"},
				// Action is empty — should default to "approve"
			},
		},
	})

	err := mgr.RequireApproval(
		context.Background(), "client",
		nil, "/s/1",
		RequestTypeGetSecret, nil,
		SenderInfo{ProcessChain: []ProcessInfo{{Name: "test", PID: 1}}},
	)
	if err != nil {
		t.Fatalf("expected nil (default action approve), got %v", err)
	}
}
