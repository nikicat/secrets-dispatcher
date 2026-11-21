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
	info := &GPGSignInfo{
		RepoName:     "myrepo",
		CommitMsg:    "fix: thing",
		Author:       "Alice <alice@example.com>",
		Committer:    "Alice <alice@example.com>",
		KeyID:        "ABCD1234",
		ChangedFiles: []string{"main.go"},
		CommitObject: "tree 0\n",
	}
	return info.RequestInfo(SenderInfo{})
}

func mustCreateSampleGPGSign(t *testing.T, mgr *Manager) string {
	t.Helper()
	id, err := mgr.CreateGPGSignRequest(sampleGPGSignRequest())
	if err != nil {
		t.Fatalf("CreateGPGSignRequest failed: %v", err)
	}
	return id
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

// A valid request gets an ID, is listed as pending, and fires
// EventRequestCreated.
func TestCreateGPGSignRequest_ValidInfo(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
	obs := &testObserver{}
	mgr.Subscribe(obs)

	id := mustCreateSampleGPGSign(t, mgr)
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
	events := obs.WaitForEvents(t.Context(), 1, time.Second)
	ev := findEvent(events, EventRequestCreated)
	if ev == nil {
		t.Fatalf("expected EventRequestCreated to fire, got %d events: %v", len(events), events)
	}
	if ev.Request.ID != id {
		t.Errorf("event request ID %q != returned ID %q", ev.Request.ID, id)
	}
}

// A request with no GPGSignInfo gets no ID and an error.
func TestCreateGPGSignRequest_NilInfo(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})

	id, err := mgr.CreateGPGSignRequest(RequestInfo{Type: RequestTypeGPGSign})
	if err == nil {
		t.Fatal("expected error for nil GPGSignInfo, got nil")
	}
	if id != "" {
		t.Errorf("expected empty ID on error, got %q", id)
	}
}

// A gpg_sign request fires EventRequestExpired after the manager's timeout,
// and is no longer pending afterwards.
func TestCreateGPGSignRequest_Expiry(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 50 * time.Millisecond, HistoryMax: 100})
	obs := &testObserver{}
	mgr.Subscribe(obs)

	id := mustCreateSampleGPGSign(t, mgr)

	// Wait for Created + Expired events (budget: 200ms, well within 10s test timeout).
	events := obs.WaitForEvents(t.Context(), 2, 200*time.Millisecond)
	if len(events) < 2 {
		t.Fatalf("expected 2 events (Created, Expired), got %d", len(events))
	}

	// addPending arms the expiry only after the created event is delivered.
	assert.Equal(t, EventRequestCreated, events[0].Type)
	assert.Equal(t, EventRequestExpired, events[1].Type)
	assert.Equal(t, id, events[1].Request.ID)

	// Request must no longer be in pending after expiry.
	pending := mgr.List()
	for _, req := range pending {
		if req.ID == id {
			t.Errorf("expired request %q still appears in pending list", id)
		}
	}
}

// Approving a gpg_sign request fires EventRequestApproved, and the request is
// no longer pending.
func TestCreateGPGSignRequest_Approve(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
	obs := &testObserver{}
	mgr.Subscribe(obs)

	id := mustCreateSampleGPGSign(t, mgr)

	if err := mgr.Approve(id); err != nil {
		t.Fatalf("Approve failed: %v", err)
	}

	events := obs.WaitForEvents(t.Context(), 2, time.Second)
	if len(events) < 2 {
		t.Fatalf("expected 2 events (Created, Approved), got %d", len(events))
	}
	assert.Equal(t, EventRequestCreated, events[0].Type)
	assert.Equal(t, EventRequestApproved, events[1].Type)

	// Request must be gone from pending.
	pending := mgr.List()
	for _, req := range pending {
		if req.ID == id {
			t.Errorf("approved request %q still in pending list", id)
		}
	}
}

// Denying a gpg_sign request fires EventRequestDenied.
func TestCreateGPGSignRequest_Deny(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
	obs := &testObserver{}
	mgr.Subscribe(obs)

	id := mustCreateSampleGPGSign(t, mgr)

	if err := mgr.Deny(id); err != nil {
		t.Fatalf("Deny failed: %v", err)
	}

	events := obs.WaitForEvents(t.Context(), 2, time.Second)
	if len(events) < 2 {
		t.Fatalf("expected 2 events (Created, Denied), got %d", len(events))
	}
	assert.Equal(t, EventRequestCreated, events[0].Type)
	assert.Equal(t, EventRequestDenied, events[1].Type)
}

// TestCreateGPGSignRequest_Cancel verifies that Cancel(id) fires EventRequestCancelled
// and removes the request from pending.
func TestCreateGPGSignRequest_Cancel(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
	obs := &testObserver{}
	mgr.Subscribe(obs)

	id := mustCreateSampleGPGSign(t, mgr)

	if err := mgr.Cancel(id); err != nil {
		t.Fatalf("Cancel failed: %v", err)
	}

	events := obs.WaitForEvents(t.Context(), 2, time.Second)
	if len(events) < 2 {
		t.Fatalf("expected 2 events (Created, Cancelled), got %d", len(events))
	}
	assert.Equal(t, EventRequestCreated, events[0].Type)
	assert.Equal(t, EventRequestCancelled, events[1].Type)

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
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})

	in := sampleGPGSignRequest()
	info := in.GPGSignInfo
	id, err := mgr.CreateGPGSignRequest(in)
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
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})

	const n = 5
	var (
		mu  sync.Mutex
		ids []string
		wg  sync.WaitGroup
	)

	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			id, err := mgr.CreateGPGSignRequest(sampleGPGSignRequest())
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

// TestRecordAutoApprovedGPGSign_Resolution verifies that RecordAutoApprovedGPGSign
// fires EventRequestAutoApproved (not EventRequestApproved) so history shows "auto_approved".
func TestRecordAutoApprovedGPGSign_Resolution(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
	obs := &testObserver{}
	mgr.Subscribe(obs)

	id, err := mgr.RecordAutoApprovedGPGSign(sampleGPGSignRequest(), GPGResult{Signature: []byte("sig"), Status: []byte("status")})
	if err != nil {
		t.Fatalf("RecordAutoApprovedGPGSign returned unexpected error: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty request ID")
	}

	events := obs.WaitForEvents(t.Context(), 1, time.Second)
	ev := findEvent(events, EventRequestAutoApproved)
	if ev == nil {
		t.Fatalf("expected EventRequestAutoApproved, got events: %v", events)
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

// AwaitGPGSign returns the History entry of an expired request, and the entry
// carries no gpg output.
func TestAwaitGPGSign_Expiry(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 20 * time.Millisecond, HistoryMax: 100})

	got, err := mgr.AwaitGPGSign(t.Context(), sampleGPGSignRequest())

	require.NoError(t, err)
	assert.Equal(t, ResolutionExpired, got.Resolution)
	assert.Nil(t, got.GPGOutput())
	assert.Zero(t, mgr.PendingCount())
}
