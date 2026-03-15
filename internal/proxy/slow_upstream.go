package proxy

import (
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

// UpstreamCallContext carries caller and item information for slow upstream
// notifications. SenderInfo is pre-filled for approval-gated calls;
// ResolveSender is set for non-gated calls that have access to a D-Bus
// message (resolved lazily only when the notification fires).
type UpstreamCallContext struct {
	RequestType   approval.RequestType
	Items         []approval.ItemInfo
	SenderInfo    approval.SenderInfo
	ResolveSender func() approval.SenderInfo // lazy; nil if unavailable
}

// UpstreamNotifier shows informational notifications about slow upstream calls.
type UpstreamNotifier interface {
	// NotifySlowUpstream shows a notification that the upstream is likely
	// waiting for pinentry. Returns a function to dismiss it.
	NotifySlowUpstream(ctx UpstreamCallContext) func()
}

// propResult bundles a GetProperty return pair for use with withSlowNotify.
type propResult struct {
	v   dbus.Variant
	err error
}

// WithSlowNotify wraps an arbitrary blocking call. If fn doesn't complete
// within threshold, fires a notification via notifier. The notification is
// dismissed when fn completes. SenderInfo is resolved lazily just before
// the notification fires.
func WithSlowNotify[T any](threshold time.Duration, notifier UpstreamNotifier, ctx UpstreamCallContext, fn func() T) T {
	if notifier == nil || threshold <= 0 {
		return fn()
	}

	done := make(chan T, 1)
	go func() {
		done <- fn()
	}()

	timer := time.NewTimer(threshold)
	defer timer.Stop()

	select {
	case result := <-done:
		return result
	case <-timer.C:
		if ctx.SenderInfo.PID == 0 && len(ctx.SenderInfo.ProcessChain) == 0 && ctx.ResolveSender != nil {
			ctx.SenderInfo = ctx.ResolveSender()
		}
		dismiss := notifier.NotifySlowUpstream(ctx)
		result := <-done
		dismiss()
		return result
	}
}

// callWithSlowNotify is a convenience wrapper for *dbus.Call return type.
func callWithSlowNotify(threshold time.Duration, notifier UpstreamNotifier, ctx UpstreamCallContext, fn func() *dbus.Call) *dbus.Call {
	return WithSlowNotify(threshold, notifier, ctx, fn)
}
