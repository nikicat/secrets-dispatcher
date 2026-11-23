package approval

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Returns a valid gpg_sign request with an empty sender.
func sampleGPGSignRequest() RequestInfo {
	return signRequest(SenderInfo{}, "myrepo", []string{"main.go"})
}

// Returns the first event of the given kind in the slice, or nil.
func findEvent(events []testEvent, et eventKind) *testEvent {
	for i := range events {
		if events[i].Type == et {
			return &events[i]
		}
	}
	return nil
}

// A valid request gets an ID, is listed as pending, and tells observers it
// was created.
func TestCreate_GPGSign_ValidInfo(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
	obs := &testObserver{}
	mgr.Subscribe(obs)

	id := mgr.Create(sampleGPGSignRequest())
	if id == "" {
		t.Fatal("expected non-empty request ID")
	}

	_, ok := mgr.GetPending(id)
	assert.True(t, ok, "request %q is not pending", id)

	events := obs.WaitForEvents(t.Context(), 1, time.Second)
	ev := findEvent(events, evCreated)
	if ev == nil {
		t.Fatalf("expected a created event, got %d events: %v", len(events), events)
	}
	if ev.Request.ID != id {
		t.Errorf("event request ID %q != returned ID %q", ev.Request.ID, id)
	}
}

// A gpg_sign request ends as expired after the manager's timeout, and is no
// longer pending afterwards.
func TestCreate_GPGSign_Expiry(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 50 * time.Millisecond, HistoryMax: 100})
	obs := &testObserver{}
	mgr.Subscribe(obs)

	id := mgr.Create(sampleGPGSignRequest())

	// Wait for Created + Expired events (budget: 200ms, well within 10s test timeout).
	events := obs.WaitForEvents(t.Context(), 2, 200*time.Millisecond)
	if len(events) < 2 {
		t.Fatalf("expected 2 events (Created, Expired), got %d", len(events))
	}

	// addPending arms the expiry only after the created event is delivered.
	assert.Equal(t, evCreated, events[0].Type)
	assert.Equal(t, evExpired, events[1].Type)
	assert.Equal(t, id, events[1].Request.ID)
	_, ok := mgr.GetPending(id)
	assert.False(t, ok, "expired request %q is still pending", id)
}

// Approving a gpg_sign request ends it as approved, and the request is no
// longer pending.
func TestCreate_GPGSign_Approve(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
	obs := &testObserver{}
	mgr.Subscribe(obs)

	id := mgr.Create(sampleGPGSignRequest())

	if err := mgr.Approve(id); err != nil {
		t.Fatalf("Approve failed: %v", err)
	}

	events := obs.WaitForEvents(t.Context(), 2, time.Second)
	if len(events) < 2 {
		t.Fatalf("expected 2 events (Created, Approved), got %d", len(events))
	}
	assert.Equal(t, evCreated, events[0].Type)
	assert.Equal(t, evApproved, events[1].Type)
	_, ok := mgr.GetPending(id)
	assert.False(t, ok, "approved request %q is still pending", id)
}

// Denying a gpg_sign request ends it as denied.
func TestCreate_GPGSign_Deny(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
	obs := &testObserver{}
	mgr.Subscribe(obs)

	id := mgr.Create(sampleGPGSignRequest())

	if err := mgr.Deny(id); err != nil {
		t.Fatalf("Deny failed: %v", err)
	}

	events := obs.WaitForEvents(t.Context(), 2, time.Second)
	if len(events) < 2 {
		t.Fatalf("expected 2 events (Created, Denied), got %d", len(events))
	}
	assert.Equal(t, evCreated, events[0].Type)
	assert.Equal(t, evDenied, events[1].Type)
}

// Cancelling a gpg_sign request ends it as cancelled, ends its pending state
// and records the cancellation in History.
func TestCreate_GPGSign_Cancel(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
	obs := &testObserver{}
	mgr.Subscribe(obs)

	id := mgr.Create(sampleGPGSignRequest())

	if err := mgr.Cancel(id); err != nil {
		t.Fatalf("Cancel failed: %v", err)
	}

	events := obs.WaitForEvents(t.Context(), 2, time.Second)
	if len(events) < 2 {
		t.Fatalf("expected 2 events (Created, Cancelled), got %d", len(events))
	}
	assert.Equal(t, evCreated, events[0].Type)
	assert.Equal(t, evCancelled, events[1].Type)
	_, ok := mgr.GetPending(id)
	assert.False(t, ok, "cancelled request %q is still pending", id)

	time.Sleep(50 * time.Millisecond)
	history := mgr.History()
	if len(history) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(history))
	}
	if history[0].Resolution != ResolutionCancelled {
		t.Errorf("expected resolution 'cancelled', got '%s'", history[0].Resolution)
	}
}

// The pending request carries the GPGSignInfo it was created with.
func TestCreate_GPGSign_InfoPreserved(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})

	in := sampleGPGSignRequest()
	info, _ := in.GPGSign()
	id := mgr.Create(in)

	req, ok := mgr.GetPending(id)
	require.True(t, ok, "request %q is not pending", id)
	if req.Type != RequestTypeGPGSign {
		t.Errorf("expected type %q, got %q", RequestTypeGPGSign, req.Type)
	}
	got, ok := req.GPGSign()
	require.True(t, ok, "expected GPGSignInfo on pending request")
	if got.KeyID != info.KeyID {
		t.Errorf("KeyID mismatch: got %q, want %q", got.KeyID, info.KeyID)
	}
	if got.RepoName != info.RepoName {
		t.Errorf("RepoName mismatch: got %q, want %q", got.RepoName, info.RepoName)
	}
}

// Concurrent creations each get a distinct ID and all stay pending.
func TestCreate_GPGSign_Concurrent(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})

	const n = 5
	var (
		mu  sync.Mutex
		ids []RequestID
		wg  sync.WaitGroup
	)

	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			id := mgr.Create(sampleGPGSignRequest())
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
	seen := make(map[RequestID]bool)
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

// The record ends as auto-approved rather than approved, so History shows it
// as auto_approved, and nothing stays pending.
func TestRecordAutoApprovedGPGSign_Resolution(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
	obs := &testObserver{}
	mgr.Subscribe(obs)

	id := mgr.RecordAutoApprovedGPGSign(sampleGPGSignRequest(), GPGResult{Signature: []byte("sig"), Status: []byte("status")})
	if id == "" {
		t.Fatal("expected non-empty request ID")
	}

	events := obs.WaitForEvents(t.Context(), 1, time.Second)
	ev := findEvent(events, evAutoApproved)
	if ev == nil {
		t.Fatalf("expected an auto-approved end, got events: %v", events)
	}
	if ev.Request.ID != id {
		t.Errorf("event request ID %q != returned ID %q", ev.Request.ID, id)
	}

	// Must NOT appear in pending.
	if mgr.PendingCount() != 0 {
		t.Errorf("expected 0 pending requests, got %d", mgr.PendingCount())
	}

	// History should record auto_approved resolution.
	time.Sleep(50 * time.Millisecond)
	history := mgr.History()
	if len(history) != 1 {
		t.Fatalf("expected 1 history entry, got %d", len(history))
	}
	if history[0].Resolution != ResolutionAutoApproved {
		t.Errorf("expected resolution %q, got %q", ResolutionAutoApproved, history[0].Resolution)
	}
}

func TestHistoryEntryGPGOutput(t *testing.T) {
	tests := []struct {
		name       string
		reqType    RequestType
		resolution Resolution
		want       bool
	}{
		{"gpg_sign approved", RequestTypeGPGSign, ResolutionApproved, true},
		{"gpg_sign auto-approved", RequestTypeGPGSign, ResolutionAutoApproved, true},
		{"gpg_sign denied", RequestTypeGPGSign, ResolutionDenied, false},
		{"gpg_sign cancelled", RequestTypeGPGSign, ResolutionCancelled, false},
		{"gpg_sign expired", RequestTypeGPGSign, ResolutionExpired, false},
		{"get_secret approved", RequestTypeGetSecret, ResolutionApproved, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &Request{Type: tt.reqType, GPGResult: GPGResult{Signature: []byte("sig")}}
			got := HistoryEntry{Request: req, Resolution: tt.resolution}.GPGOutput()
			if (got != nil) != tt.want {
				t.Fatalf("GPGOutput() = %v, want non-nil %v", got, tt.want)
			}
			if got != nil && got != &req.GPGResult {
				t.Errorf("GPGOutput() = %p, want the request's own GPGResult %p", got, &req.GPGResult)
			}
		})
	}
}

// Await returns the History entry of an expired gpg_sign request, and the
// entry carries no gpg output.
func TestAwait_GPGSignExpiry(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 20 * time.Millisecond, HistoryMax: 100})

	got := mgr.Await(t.Context(), sampleGPGSignRequest())

	assert.Equal(t, ResolutionExpired, got.Resolution)
	assert.Nil(t, got.GPGOutput())
	assert.Zero(t, mgr.PendingCount())
}
