package approval

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestManager_RequireApproval_Approved(t *testing.T) {
	mgr := NewManager(5*time.Second, 100, 0)

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
	mgr := NewManager(5*time.Second, 100, 0)

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
	mgr := NewManager(100*time.Millisecond, 100, 0)

	err := mgr.RequireApproval(context.Background(), "test-client", []ItemInfo{{Path: "/test/item"}}, "/session/1", RequestTypeGetSecret, nil, SenderInfo{})

	if err != ErrTimeout {
		t.Errorf("expected ErrTimeout, got: %v", err)
	}
}

func TestManager_RequireApproval_ContextCanceled(t *testing.T) {
	mgr := NewManager(5*time.Second, 100, 0)

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
	mgr := NewManager(5*time.Second, 100, 0)

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
	mgr := NewManager(5*time.Second, 100, 0)

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
	mgr := NewManager(5*time.Second, 100, 0)

	err := mgr.Cancel("nonexistent-id")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestManager_Approve_NotFound(t *testing.T) {
	mgr := NewManager(5*time.Second, 100, 0)

	err := mgr.Approve("nonexistent-id")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestManager_Deny_NotFound(t *testing.T) {
	mgr := NewManager(5*time.Second, 100, 0)

	err := mgr.Deny("nonexistent-id")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestManager_Concurrent(t *testing.T) {
	mgr := NewManager(5*time.Second, 100, 0)

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
	mgr := NewManager(5*time.Second, 100, 0)

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
	mgr := NewManager(5*time.Second, 100, 0)

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
	mgr := NewManager(5*time.Second, 100, 0)
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
	mgr := NewManager(5*time.Second, 100, 0)
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
	mgr := NewManager(5*time.Second, 100, 0)
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
	mgr := NewManager(100*time.Millisecond, 100, 0)
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
	mgr := NewManager(5*time.Second, 100, 0)
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
	mgr := NewManager(5*time.Second, 100, 0)
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
	mgr := NewManager(5*time.Second, 100, 0)
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
	mgr := NewManager(5*time.Second, 100, 0)
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
	mgr := NewManager(5*time.Second, 100, 0)

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
	mgr := NewManager(5*time.Second, 100, 0)

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
	mgr := NewManager(5*time.Second, 100, 0)

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
	mgr := NewManager(100*time.Millisecond, 100, 0)

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
	mgr := NewManager(5*time.Second, 100, 0)

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
	mgr := NewManager(100*time.Millisecond, historyMax, 0)

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
	mgr := NewManager(5*time.Second, 100, 0)

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
	mgr := NewManager(5*time.Second, 100, time.Second)

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
	mgr := NewManager(5*time.Second, 100, 50*time.Millisecond)

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
	mgr := NewManager(5*time.Second, 100, time.Second)

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

func TestApprovalCache_DifferentItem(t *testing.T) {
	mgr := NewManager(5*time.Second, 100, time.Second)

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
