package proxy

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/procutil"
	"github.com/nikicat/secrets-dispatcher/internal/sender"
)

// Counts its slow-upstream notifications and their dismissals.
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

func instantCall() *dbus.Call { return &dbus.Call{} }

// An upstream call that takes the given time to return.
func callTaking(d time.Duration) func() *dbus.Call {
	return func() *dbus.Call {
		time.Sleep(d)
		return &dbus.Call{}
	}
}

func TestWithSlowNotify_FastCall(t *testing.T) {
	notifier := &mockNotifier{}
	ctx := RequestCall(approval.RequestInfo{Items: []approval.ItemInfo{{Label: "test-secret"}}})

	call := WithSlowNotify(SlowUpstream{Notifier: notifier, Threshold: 100 * time.Millisecond}, ctx, instantCall)

	require.NotNil(t, call)
	assert.Equal(t, 0, notifier.notifyCalls, "notify calls for fast call")
	assert.Equal(t, 0, notifier.dismissCalls, "dismiss calls for fast call")
}

func TestWithSlowNotify_SlowCall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		notifier := &mockNotifier{}
		ctx := RequestCall(approval.RequestInfo{Type: approval.RequestTypeGetSecret, Items: []approval.ItemInfo{{Label: "my-password"}}, SenderInfo: approval.SenderInfo{Info: sender.Info{PID: 1234}}})

		call := WithSlowNotify(SlowUpstream{Notifier: notifier, Threshold: 50 * time.Millisecond}, ctx, callTaking(150*time.Millisecond))

		require.NotNil(t, call)
		assert.Equal(t, 1, notifier.notifyCalls, "notify calls for slow call")
		assert.Equal(t, 1, notifier.dismissCalls, "dismiss calls after slow call completes")
		if assert.Len(t, notifier.lastRequest.Items, 1) {
			assert.Equal(t, "my-password", notifier.lastRequest.Items[0].Label)
		}
		assert.Equal(t, approval.RequestTypeGetSecret, notifier.lastRequest.Type)
		assert.Equal(t, procutil.PID(1234), notifier.lastRequest.SenderInfo.PID)
	})
}

func TestWithSlowNotify_NilNotifier(t *testing.T) {
	call := WithSlowNotify(SlowUpstream{Threshold: 50 * time.Millisecond}, UpstreamCallContext{}, instantCall)
	require.NotNil(t, call, "call with nil notifier")
}

func TestWithSlowNotify_ZeroThreshold(t *testing.T) {
	notifier := &mockNotifier{}
	call := WithSlowNotify(SlowUpstream{Notifier: notifier}, UpstreamCallContext{}, instantCall)
	require.NotNil(t, call, "call with zero threshold")
	assert.Equal(t, 0, notifier.notifyCalls, "notify calls with zero threshold")
}

func TestWithSlowNotify_LazyResolution(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
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
		WithSlowNotify(SlowUpstream{Notifier: notifier, Threshold: 100 * time.Millisecond}, ctx, instantCall)
		require.False(t, resolved, "sender should not be resolved on fast path")

		// Slow call: the sender must be called
		WithSlowNotify(SlowUpstream{Notifier: notifier, Threshold: 50 * time.Millisecond}, ctx, callTaking(150*time.Millisecond))
		require.True(t, resolved, "sender should be resolved on slow path")

		assert.Equal(t, procutil.PID(5678), notifier.lastRequest.SenderInfo.PID)
		assert.Len(t, notifier.lastRequest.SenderInfo.ProcessChain, 2)
	})
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
			synctest.Test(t, func(t *testing.T) {
				notifier := &mockNotifier{}
				slow := SlowUpstream{Notifier: notifier, Threshold: 20 * time.Millisecond, Always: tc.always}
				WithSlowNotify(slow, tc.call(approval.RequestInfo{}), callTaking(80*time.Millisecond))
				assert.Equal(t, tc.wantCalls, notifier.notifyCalls, "notify calls")
			})
		})
	}
}
