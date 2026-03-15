package proxy

import (
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

// mockNotifier records calls to NotifySlowUpstream.
type mockNotifier struct {
	mu           sync.Mutex
	notifyCalls  int
	dismissCalls int
	lastReqType  approval.RequestType
	lastItems    []approval.ItemInfo
}

func (m *mockNotifier) NotifySlowUpstream(reqType approval.RequestType, items []approval.ItemInfo) func() {
	m.mu.Lock()
	m.notifyCalls++
	m.lastReqType = reqType
	m.lastItems = items
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		m.dismissCalls++
		m.mu.Unlock()
	}
}

func TestCallWithSlowNotify_FastCall(t *testing.T) {
	notifier := &mockNotifier{}
	items := []approval.ItemInfo{{Label: "test-secret"}}

	call := callWithSlowNotify(100*time.Millisecond, notifier, "", items, func() *dbus.Call {
		// Completes instantly
		return &dbus.Call{}
	})

	if call == nil {
		t.Fatal("expected non-nil call")
	}
	if notifier.notifyCalls != 0 {
		t.Errorf("expected 0 notify calls for fast call, got %d", notifier.notifyCalls)
	}
	if notifier.dismissCalls != 0 {
		t.Errorf("expected 0 dismiss calls for fast call, got %d", notifier.dismissCalls)
	}
}

func TestCallWithSlowNotify_SlowCall(t *testing.T) {
	notifier := &mockNotifier{}
	items := []approval.ItemInfo{{Label: "my-password"}}

	call := callWithSlowNotify(50*time.Millisecond, notifier, approval.RequestTypeGetSecret, items, func() *dbus.Call {
		time.Sleep(150 * time.Millisecond)
		return &dbus.Call{}
	})

	if call == nil {
		t.Fatal("expected non-nil call")
	}

	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	if notifier.notifyCalls != 1 {
		t.Errorf("expected 1 notify call for slow call, got %d", notifier.notifyCalls)
	}
	if notifier.dismissCalls != 1 {
		t.Errorf("expected 1 dismiss call after slow call completes, got %d", notifier.dismissCalls)
	}
	if len(notifier.lastItems) != 1 || notifier.lastItems[0].Label != "my-password" {
		t.Errorf("expected items passed through, got %v", notifier.lastItems)
	}
	if notifier.lastReqType != approval.RequestTypeGetSecret {
		t.Errorf("expected reqType get_secret, got %q", notifier.lastReqType)
	}
}

func TestCallWithSlowNotify_NilNotifier(t *testing.T) {
	call := callWithSlowNotify(50*time.Millisecond, nil, "", nil, func() *dbus.Call {
		return &dbus.Call{}
	})
	if call == nil {
		t.Fatal("expected non-nil call with nil notifier")
	}
}

func TestCallWithSlowNotify_ZeroThreshold(t *testing.T) {
	notifier := &mockNotifier{}
	call := callWithSlowNotify(0, notifier, "", nil, func() *dbus.Call {
		return &dbus.Call{}
	})
	if call == nil {
		t.Fatal("expected non-nil call with zero threshold")
	}
	if notifier.notifyCalls != 0 {
		t.Errorf("expected 0 notify calls with zero threshold, got %d", notifier.notifyCalls)
	}
}
