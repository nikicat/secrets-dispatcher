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
	lastCtx      UpstreamCallContext
}

func (m *mockNotifier) NotifySlowUpstream(ctx UpstreamCallContext) func() {
	m.mu.Lock()
	m.notifyCalls++
	m.lastCtx = ctx
	m.mu.Unlock()
	return func() {
		m.mu.Lock()
		m.dismissCalls++
		m.mu.Unlock()
	}
}

func TestCallWithSlowNotify_FastCall(t *testing.T) {
	notifier := &mockNotifier{}
	ctx := UpstreamCallContext{Items: []approval.ItemInfo{{Label: "test-secret"}}}

	call := callWithSlowNotify(100*time.Millisecond, notifier, ctx, func() *dbus.Call {
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
	ctx := UpstreamCallContext{
		RequestType: approval.RequestTypeGetSecret,
		Items:       []approval.ItemInfo{{Label: "my-password"}},
		SenderInfo:  approval.SenderInfo{PID: 1234},
	}

	call := callWithSlowNotify(50*time.Millisecond, notifier, ctx, func() *dbus.Call {
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
	if len(notifier.lastCtx.Items) != 1 || notifier.lastCtx.Items[0].Label != "my-password" {
		t.Errorf("expected items passed through, got %v", notifier.lastCtx.Items)
	}
	if notifier.lastCtx.RequestType != approval.RequestTypeGetSecret {
		t.Errorf("expected reqType get_secret, got %q", notifier.lastCtx.RequestType)
	}
	if notifier.lastCtx.SenderInfo.PID != 1234 {
		t.Errorf("expected SenderInfo.PID 1234, got %d", notifier.lastCtx.SenderInfo.PID)
	}
}

func TestCallWithSlowNotify_NilNotifier(t *testing.T) {
	call := callWithSlowNotify(50*time.Millisecond, nil, UpstreamCallContext{}, func() *dbus.Call {
		return &dbus.Call{}
	})
	if call == nil {
		t.Fatal("expected non-nil call with nil notifier")
	}
}

func TestCallWithSlowNotify_ZeroThreshold(t *testing.T) {
	notifier := &mockNotifier{}
	call := callWithSlowNotify(0, notifier, UpstreamCallContext{}, func() *dbus.Call {
		return &dbus.Call{}
	})
	if call == nil {
		t.Fatal("expected non-nil call with zero threshold")
	}
	if notifier.notifyCalls != 0 {
		t.Errorf("expected 0 notify calls with zero threshold, got %d", notifier.notifyCalls)
	}
}

func TestCallWithSlowNotify_LazyResolution(t *testing.T) {
	resolved := false
	notifier := &mockNotifier{}
	ctx := UpstreamCallContext{
		RequestType: approval.RequestTypeSearch,
		ResolveSender: func() approval.SenderInfo {
			resolved = true
			return approval.SenderInfo{
				PID: 5678,
				ProcessChain: []approval.ProcessInfo{
					{Name: "git", PID: 5678},
					{Name: "zsh", PID: 1000},
				},
			}
		},
	}

	// Fast call: ResolveSender must NOT be called
	callWithSlowNotify(100*time.Millisecond, notifier, ctx, func() *dbus.Call {
		return &dbus.Call{}
	})
	if resolved {
		t.Fatal("ResolveSender should not be called on fast path")
	}

	// Slow call: ResolveSender must be called
	callWithSlowNotify(50*time.Millisecond, notifier, ctx, func() *dbus.Call {
		time.Sleep(150 * time.Millisecond)
		return &dbus.Call{}
	})
	if !resolved {
		t.Fatal("ResolveSender should be called on slow path")
	}

	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	if notifier.lastCtx.SenderInfo.PID != 5678 {
		t.Errorf("expected resolved PID 5678, got %d", notifier.lastCtx.SenderInfo.PID)
	}
	if len(notifier.lastCtx.SenderInfo.ProcessChain) != 2 {
		t.Errorf("expected 2 process chain entries, got %d", len(notifier.lastCtx.SenderInfo.ProcessChain))
	}
}

func TestCallWithSlowNotify_PrefilledSenderSkipsResolve(t *testing.T) {
	resolved := false
	notifier := &mockNotifier{}
	ctx := UpstreamCallContext{
		RequestType: approval.RequestTypeGetSecret,
		SenderInfo:  approval.SenderInfo{PID: 999},
		ResolveSender: func() approval.SenderInfo {
			resolved = true
			return approval.SenderInfo{PID: 0}
		},
	}

	// Slow call with pre-filled SenderInfo: ResolveSender must NOT be called
	callWithSlowNotify(50*time.Millisecond, notifier, ctx, func() *dbus.Call {
		time.Sleep(150 * time.Millisecond)
		return &dbus.Call{}
	})
	if resolved {
		t.Fatal("ResolveSender should not be called when SenderInfo is pre-filled")
	}

	notifier.mu.Lock()
	defer notifier.mu.Unlock()
	if notifier.lastCtx.SenderInfo.PID != 999 {
		t.Errorf("expected pre-filled PID 999, got %d", notifier.lastCtx.SenderInfo.PID)
	}
}
