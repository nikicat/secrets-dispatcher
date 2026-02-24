package approval

import (
	"sync"
	"testing"
	"time"
)

// sampleGPGSignInfo returns a valid GPGSignInfo for use in tests.
func sampleGPGSignInfo() *GPGSignInfo {
	return &GPGSignInfo{
		RepoName:     "myrepo",
		CommitMsg:    "fix: thing",
		Author:       "Alice <alice@example.com>",
		Committer:    "Alice <alice@example.com>",
		KeyID:        "ABCD1234",
		ChangedFiles: []string{"main.go"},
	}
}

// findEvent returns the first event of the given type in the slice, or nil if not found.
func findEvent(events []Event, et EventType) *Event {
	for i := range events {
		if events[i].Type == et {
			return &events[i]
		}
	}
	return nil
}

// TestCreateGPGSignRequest_ValidInfo verifies Case 1:
// CreateGPGSignRequest with valid GPGSignInfo returns a non-empty ID and
// the request appears in the pending list. EventRequestCreated fires.
func TestCreateGPGSignRequest_ValidInfo(t *testing.T) {
	mgr := NewManager(5*time.Second, 100)
	obs := &testObserver{}
	mgr.Subscribe(obs)

	id, err := mgr.CreateGPGSignRequest("test-client", sampleGPGSignInfo())
	if err != nil {
		t.Fatalf("CreateGPGSignRequest returned unexpected error: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty request ID")
	}

	// Request must appear in pending list.
	pending := mgr.List()
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending request, got %d", len(pending))
	}
	if pending[0].ID != id {
		t.Errorf("pending request ID %q does not match returned ID %q", pending[0].ID, id)
	}

	// EventRequestCreated must fire.
	events := obs.WaitForEvents(1, time.Second)
	ev := findEvent(events, EventRequestCreated)
	if ev == nil {
		t.Fatalf("expected EventRequestCreated to fire, got %d events: %v", len(events), events)
	}
	if ev.Request.ID != id {
		t.Errorf("event request ID %q != returned ID %q", ev.Request.ID, id)
	}
}

// TestCreateGPGSignRequest_NilInfo verifies Case 2:
// CreateGPGSignRequest with nil GPGSignInfo returns "", non-nil error.
func TestCreateGPGSignRequest_NilInfo(t *testing.T) {
	mgr := NewManager(5*time.Second, 100)

	id, err := mgr.CreateGPGSignRequest("test-client", nil)
	if err == nil {
		t.Fatal("expected error for nil GPGSignInfo, got nil")
	}
	if id != "" {
		t.Errorf("expected empty ID on error, got %q", id)
	}
}

// TestCreateGPGSignRequest_Expiry verifies Case 3 (ERR-03):
// A gpg_sign request fires EventRequestExpired after the manager timeout and
// is then removed from the pending map.
func TestCreateGPGSignRequest_Expiry(t *testing.T) {
	mgr := NewManager(50*time.Millisecond, 100)
	obs := &testObserver{}
	mgr.Subscribe(obs)

	id, err := mgr.CreateGPGSignRequest("test-client", sampleGPGSignInfo())
	if err != nil {
		t.Fatalf("CreateGPGSignRequest failed: %v", err)
	}

	// Wait for Created + Expired events (budget: 200ms, well within 10s test timeout).
	events := obs.WaitForEvents(2, 200*time.Millisecond)
	if len(events) < 2 {
		t.Fatalf("expected 2 events (Created, Expired), got %d", len(events))
	}

	// Both event types must be present (order is non-deterministic due to async dispatch).
	if findEvent(events, EventRequestCreated) == nil {
		t.Errorf("EventRequestCreated not found in events: %v", events)
	}
	expiredEv := findEvent(events, EventRequestExpired)
	if expiredEv == nil {
		t.Errorf("EventRequestExpired not found in events: %v", events)
	} else if expiredEv.Request.ID != id {
		t.Errorf("expired event request ID %q != created ID %q", expiredEv.Request.ID, id)
	}

	// Request must no longer be in pending after expiry.
	pending := mgr.List()
	for _, req := range pending {
		if req.ID == id {
			t.Errorf("expired request %q still appears in pending list", id)
		}
	}
}

// TestCreateGPGSignRequest_Approve verifies Case 4:
// Approve(id) fires EventRequestApproved and removes the request from pending.
func TestCreateGPGSignRequest_Approve(t *testing.T) {
	mgr := NewManager(5*time.Second, 100)
	obs := &testObserver{}
	mgr.Subscribe(obs)

	id, err := mgr.CreateGPGSignRequest("test-client", sampleGPGSignInfo())
	if err != nil {
		t.Fatalf("CreateGPGSignRequest failed: %v", err)
	}

	if err := mgr.Approve(id); err != nil {
		t.Fatalf("Approve failed: %v", err)
	}

	// Wait for Created + Approved (order non-deterministic due to async dispatch).
	events := obs.WaitForEvents(2, time.Second)
	if len(events) < 2 {
		t.Fatalf("expected 2 events (Created, Approved), got %d", len(events))
	}
	if findEvent(events, EventRequestApproved) == nil {
		t.Errorf("EventRequestApproved not found in events: %v", events)
	}

	// Request must be gone from pending.
	pending := mgr.List()
	for _, req := range pending {
		if req.ID == id {
			t.Errorf("approved request %q still in pending list", id)
		}
	}
}

// TestCreateGPGSignRequest_Deny verifies Case 5:
// Deny(id) fires EventRequestDenied.
func TestCreateGPGSignRequest_Deny(t *testing.T) {
	mgr := NewManager(5*time.Second, 100)
	obs := &testObserver{}
	mgr.Subscribe(obs)

	id, err := mgr.CreateGPGSignRequest("test-client", sampleGPGSignInfo())
	if err != nil {
		t.Fatalf("CreateGPGSignRequest failed: %v", err)
	}

	if err := mgr.Deny(id); err != nil {
		t.Fatalf("Deny failed: %v", err)
	}

	// Wait for Created + Denied (order non-deterministic due to async dispatch).
	events := obs.WaitForEvents(2, time.Second)
	if len(events) < 2 {
		t.Fatalf("expected 2 events (Created, Denied), got %d", len(events))
	}
	if findEvent(events, EventRequestDenied) == nil {
		t.Errorf("EventRequestDenied not found in events: %v", events)
	}
}

// TestCreateGPGSignRequest_Cancel verifies that Cancel(id) fires EventRequestCancelled
// and removes the request from pending.
func TestCreateGPGSignRequest_Cancel(t *testing.T) {
	mgr := NewManager(5*time.Second, 100)
	obs := &testObserver{}
	mgr.Subscribe(obs)

	id, err := mgr.CreateGPGSignRequest("test-client", sampleGPGSignInfo())
	if err != nil {
		t.Fatalf("CreateGPGSignRequest failed: %v", err)
	}

	if err := mgr.Cancel(id); err != nil {
		t.Fatalf("Cancel failed: %v", err)
	}

	// Wait for Created + Cancelled events.
	events := obs.WaitForEvents(2, time.Second)
	if len(events) < 2 {
		t.Fatalf("expected 2 events (Created, Cancelled), got %d", len(events))
	}
	if findEvent(events, EventRequestCancelled) == nil {
		t.Errorf("EventRequestCancelled not found in events: %v", events)
	}

	// Request must be gone from pending.
	pending := mgr.List()
	for _, req := range pending {
		if req.ID == id {
			t.Errorf("cancelled request %q still in pending list", id)
		}
	}

	// History should record cancellation.
	time.Sleep(50 * time.Millisecond)
	history := mgr.History()
	if len(history) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(history))
	}
	if history[0].Resolution != ResolutionCancelled {
		t.Errorf("expected resolution 'cancelled', got '%s'", history[0].Resolution)
	}
}

// TestCreateGPGSignRequest_GPGSignInfoPreserved verifies that GPGSignInfo fields
// are correctly stored on the Request and visible via List().
func TestCreateGPGSignRequest_GPGSignInfoPreserved(t *testing.T) {
	mgr := NewManager(5*time.Second, 100)

	info := sampleGPGSignInfo()
	id, err := mgr.CreateGPGSignRequest("test-client", info)
	if err != nil {
		t.Fatalf("CreateGPGSignRequest failed: %v", err)
	}

	pending := mgr.List()
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending request, got %d", len(pending))
	}

	req := pending[0]
	if req.ID != id {
		t.Errorf("request ID mismatch: got %q, want %q", req.ID, id)
	}
	if req.Type != RequestTypeGPGSign {
		t.Errorf("expected type %q, got %q", RequestTypeGPGSign, req.Type)
	}
	if req.GPGSignInfo == nil {
		t.Fatal("expected non-nil GPGSignInfo on pending request")
	}
	if req.GPGSignInfo.KeyID != info.KeyID {
		t.Errorf("KeyID mismatch: got %q, want %q", req.GPGSignInfo.KeyID, info.KeyID)
	}
	if req.GPGSignInfo.RepoName != info.RepoName {
		t.Errorf("RepoName mismatch: got %q, want %q", req.GPGSignInfo.RepoName, info.RepoName)
	}
}

// TestCreateGPGSignRequest_Concurrent verifies that multiple concurrent
// CreateGPGSignRequest calls don't race or corrupt state.
func TestCreateGPGSignRequest_Concurrent(t *testing.T) {
	mgr := NewManager(5*time.Second, 100)

	const n = 5
	var (
		mu  sync.Mutex
		ids []string
		wg  sync.WaitGroup
	)

	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			id, err := mgr.CreateGPGSignRequest("test-client", sampleGPGSignInfo())
			if err != nil {
				t.Errorf("CreateGPGSignRequest failed: %v", err)
				return
			}
			mu.Lock()
			ids = append(ids, id)
			mu.Unlock()
		}()
	}
	wg.Wait()

	if len(ids) != n {
		t.Fatalf("expected %d IDs, got %d", n, len(ids))
	}

	// All IDs must be unique.
	seen := make(map[string]bool)
	for _, id := range ids {
		if seen[id] {
			t.Errorf("duplicate request ID: %q", id)
		}
		seen[id] = true
	}

	if mgr.PendingCount() != n {
		t.Errorf("expected %d pending requests, got %d", n, mgr.PendingCount())
	}
}
