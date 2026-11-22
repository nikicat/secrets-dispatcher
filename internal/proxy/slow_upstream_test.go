package proxy

import (
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/procutil"
	"github.com/nikicat/secrets-dispatcher/internal/sender"
)

// mockNotifier records calls to NotifySlowUpstream.
type mockNotifier struct {
	notifyCalls  int
	dismissCalls int
	lastRequest  approval.RequestInfo
}

func (m *mockNotifier) NotifySlowUpstream(req approval.RequestInfo) func() {
	m.notifyCalls++
	m.lastRequest = req
	return func() { m.dismissCalls++ }
}

func TestWithSlowNotify_FastCall(t *testing.T) {
	notifier := &mockNotifier{}
	ctx := RequestCall(approval.RequestInfo{Items: []approval.ItemInfo{{Label: "test-secret"}}})

	call := WithSlowNotify(SlowUpstream{Notifier: notifier, Threshold: 100 * time.Millisecond}, ctx, func() *dbus.Call {
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

func TestWithSlowNotify_SlowCall(t *testing.T) {
	notifier := &mockNotifier{}
	ctx := RequestCall(approval.RequestInfo{Type: approval.RequestTypeGetSecret, Items: []approval.ItemInfo{{Label: "my-password"}}, SenderInfo: approval.SenderInfo{Info: sender.Info{PID: 1234}}})

	call := WithSlowNotify(SlowUpstream{Notifier: notifier, Threshold: 50 * time.Millisecond}, ctx, func() *dbus.Call {
		time.Sleep(150 * time.Millisecond)
		return &dbus.Call{}
	})

	if call == nil {
		t.Fatal("expected non-nil call")
	}

	if notifier.notifyCalls != 1 {
		t.Errorf("expected 1 notify call for slow call, got %d", notifier.notifyCalls)
	}
	if notifier.dismissCalls != 1 {
		t.Errorf("expected 1 dismiss call after slow call completes, got %d", notifier.dismissCalls)
	}
	if len(notifier.lastRequest.Items) != 1 || notifier.lastRequest.Items[0].Label != "my-password" {
		t.Errorf("expected items passed through, got %v", notifier.lastRequest.Items)
	}
	if notifier.lastRequest.Type != approval.RequestTypeGetSecret {
		t.Errorf("expected reqType get_secret, got %q", notifier.lastRequest.Type)
	}
	if notifier.lastRequest.SenderInfo.PID != 1234 {
		t.Errorf("expected SenderInfo.PID 1234, got %d", notifier.lastRequest.SenderInfo.PID)
	}
}

func TestWithSlowNotify_NilNotifier(t *testing.T) {
	call := WithSlowNotify(SlowUpstream{Threshold: 50 * time.Millisecond}, UpstreamCallContext{}, func() *dbus.Call {
		return &dbus.Call{}
	})
	if call == nil {
		t.Fatal("expected non-nil call with nil notifier")
	}
}

func TestWithSlowNotify_ZeroThreshold(t *testing.T) {
	notifier := &mockNotifier{}
	call := WithSlowNotify(SlowUpstream{Notifier: notifier}, UpstreamCallContext{}, func() *dbus.Call {
		return &dbus.Call{}
	})
	if call == nil {
		t.Fatal("expected non-nil call with zero threshold")
	}
	if notifier.notifyCalls != 0 {
		t.Errorf("expected 0 notify calls with zero threshold, got %d", notifier.notifyCalls)
	}
}

func TestWithSlowNotify_LazyResolution(t *testing.T) {
	resolved := false
	notifier := &mockNotifier{}
	ctx := SenderCall(func() approval.SenderInfo {
		resolved = true
		return approval.SenderInfo{
			PID: 5678,
			ProcessChain: []procutil.Process{
				{Name: "git", PID: 5678},
				{Name: "zsh", PID: 1000},
			},
		}
	})

	// Fast call: the sender must NOT be called
	WithSlowNotify(SlowUpstream{Notifier: notifier, Threshold: 100 * time.Millisecond}, ctx, func() *dbus.Call {
		return &dbus.Call{}
	})
	if resolved {
		t.Fatal("sender should not be resolved on fast path")
	}

	// Slow call: the sender must be called
	WithSlowNotify(SlowUpstream{Notifier: notifier, Threshold: 50 * time.Millisecond}, ctx, func() *dbus.Call {
		time.Sleep(150 * time.Millisecond)
		return &dbus.Call{}
	})
	if !resolved {
		t.Fatal("sender should be resolved on slow path")
	}

	if notifier.lastRequest.SenderInfo.PID != 5678 {
		t.Errorf("expected resolved PID 5678, got %d", notifier.lastRequest.SenderInfo.PID)
	}
	if len(notifier.lastRequest.SenderInfo.ProcessChain) != 2 {
		t.Errorf("expected 2 process chain entries, got %d", len(notifier.lastRequest.SenderInfo.ProcessChain))
	}
}

func TestWithSlowNotify_Always(t *testing.T) {
	tests := []struct {
		name      string
		always    bool
		call      func(approval.RequestInfo) UpstreamCallContext
		wantCalls int
	}{
		{"default, unprompted", false, RequestCall, 1},
		{"default, prompted", false, PromptedCall, 0},
		{"always, unprompted", true, RequestCall, 1},
		{"always, prompted", true, PromptedCall, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			notifier := &mockNotifier{}
			slow := SlowUpstream{Notifier: notifier, Threshold: 20 * time.Millisecond, Always: tc.always}
			WithSlowNotify(slow, tc.call(approval.RequestInfo{}), func() *dbus.Call {
				time.Sleep(80 * time.Millisecond)
				return &dbus.Call{}
			})
			if notifier.notifyCalls != tc.wantCalls {
				t.Errorf("notify calls = %d, want %d", notifier.notifyCalls, tc.wantCalls)
			}
		})
	}
}
