package approval

import (
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Returns a valid gpg_sign request with an empty sender.
func sampleGPGSignRequest() RequestInfo {
	return CommitSignRequestForTest(SenderInfo{}, "myrepo", "main.go")
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
	mgr, obs := newObservedManager(ManagerConfig{})

	id := mgr.Create(sampleGPGSignRequest())
	require.NotEmpty(t, id, "request ID")

	_, ok := mgr.GetPending(id)
	assert.True(t, ok, "request %q is not pending", id)

	events := obs.Events()
	ev := findEvent(events, evCreated)
	require.NotNil(t, ev, "expected a created event, got %v", events)
	assert.Equal(t, id, ev.Request.ID, "event request ID")
}

// A gpg_sign request ends as expired after the manager's timeout, and is no
// longer pending afterwards.
func TestCreate_GPGSign_Expiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const timeout = 50 * time.Millisecond
		mgr, obs := newObservedManager(ManagerConfig{Timeout: timeout})

		id := mgr.Create(sampleGPGSignRequest())

		time.Sleep(2 * timeout)
		events := obs.Events()
		require.Len(t, events, 2, "expected Created and Expired events")

		assert.Equal(t, evCreated, events[0].Type)
		assert.Equal(t, evExpired, events[1].Type)
		assert.Equal(t, id, events[1].Request.ID)
		_, ok := mgr.GetPending(id)
		assert.False(t, ok, "expired request %q is still pending", id)
	})
}

// Approving, denying or cancelling a gpg_sign request ends it with that
// resolution, in its end event and in History, and it is no longer pending.
func TestCreate_GPGSign_Resolve(t *testing.T) {
	tests := []struct {
		resolve func(*Manager, RequestID) error
		want    Resolution
	}{
		{(*Manager).Approve, ResolutionApproved},
		{(*Manager).Deny, ResolutionDenied},
		{(*Manager).Cancel, ResolutionCancelled},
	}
	for _, tt := range tests {
		t.Run(string(tt.want), func(t *testing.T) {
			mgr, obs := newObservedManager(ManagerConfig{})

			id := mgr.Create(sampleGPGSignRequest())

			require.NoError(t, tt.resolve(mgr, id))

			events := obs.Events()
			require.Len(t, events, 2, "expected created and end events")
			assert.Equal(t, evCreated, events[0].Type)
			assert.Equal(t, eventKind(tt.want), events[1].Type)
			_, ok := mgr.GetPending(id)
			assert.False(t, ok, "resolved request %q is still pending", id)

			history := mgr.History()
			require.Len(t, history, 1)
			assert.Equal(t, tt.want, history[0].Resolution)
		})
	}
}

// The pending request carries the GPGSignInfo it was created with.
func TestCreate_GPGSign_InfoPreserved(t *testing.T) {
	mgr := NewManagerForTest(ManagerConfig{})

	in := sampleGPGSignRequest()
	info, _ := in.GPGSign()
	id := mgr.Create(in)

	req, ok := mgr.GetPending(id)
	require.True(t, ok, "request %q is not pending", id)
	assert.Equal(t, RequestTypeGPGSign, req.Type)
	got, ok := req.GPGSign()
	require.True(t, ok, "expected GPGSignInfo on pending request")
	assert.Equal(t, info.KeyID, got.KeyID)
	assert.Equal(t, info.RepoName, got.RepoName)
}

// Concurrent creations each get a distinct ID and all stay pending.
func TestCreate_GPGSign_Concurrent(t *testing.T) {
	mgr := NewManagerForTest(ManagerConfig{})

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

	require.Len(t, ids, n)

	// All IDs must be unique.
	seen := make(map[RequestID]bool)
	for _, id := range ids {
		assert.False(t, seen[id], "duplicate request ID: %q", id)
		seen[id] = true
	}

	assert.Equal(t, n, mgr.PendingCount(), "pending requests")
}

// The record ends as auto-approved rather than approved, so History shows it
// as auto_approved, and nothing stays pending.
func TestRecordAutoApprovedGPGSign_Resolution(t *testing.T) {
	mgr, obs := newObservedManager(ManagerConfig{})

	id := mgr.RecordAutoApprovedGPGSign(sampleGPGSignRequest(), GPGResult{Signature: []byte("sig"), Status: []byte("status")})
	require.NotEmpty(t, id, "request ID")

	events := obs.Events()
	ev := findEvent(events, evAutoApproved)
	require.NotNil(t, ev, "expected an auto-approved end, got events: %v", events)
	assert.Equal(t, id, ev.Request.ID, "event request ID")

	// Must NOT appear in pending.
	assert.Zero(t, mgr.PendingCount(), "pending requests")

	// History should record auto_approved resolution.
	history := mgr.History()
	require.Len(t, history, 1)
	assert.Equal(t, ResolutionAutoApproved, history[0].Resolution)
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
			if !tt.want {
				assert.Nil(t, got, "GPGOutput()")
				return
			}
			assert.Same(t, &req.GPGResult, got, "GPGOutput() is not the request's own GPGResult")
		})
	}
}

// Await returns the History entry of an expired gpg_sign request, and the
// entry carries no gpg output.
func TestAwait_GPGSignExpiry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		mgr := NewManagerForTest(ManagerConfig{Timeout: 20 * time.Millisecond})

		got := mgr.Await(t.Context(), sampleGPGSignRequest())

		assert.Equal(t, ResolutionExpired, got.Resolution)
		assert.Nil(t, got.GPGOutput())
		assert.Zero(t, mgr.PendingCount())
	})
}
