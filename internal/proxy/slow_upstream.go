package proxy

import (
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

// UpstreamNotifier shows informational notifications about slow upstream calls.
type UpstreamNotifier interface {
	// NotifySlowUpstream shows a notification that the upstream is likely
	// waiting for pinentry. Returns a function to dismiss it.
	NotifySlowUpstream(reqType approval.RequestType, items []approval.ItemInfo) func()
}

// propResult bundles a GetProperty return pair for use with withSlowNotify.
type propResult struct {
	v   dbus.Variant
	err error
}

// WithSlowNotify wraps an arbitrary blocking call. If fn doesn't complete
// within threshold, fires a notification via notifier. The notification is
// dismissed when fn completes.
func WithSlowNotify[T any](threshold time.Duration, notifier UpstreamNotifier, reqType approval.RequestType, items []approval.ItemInfo, fn func() T) T {
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
		dismiss := notifier.NotifySlowUpstream(reqType, items)
		result := <-done
		dismiss()
		return result
	}
}

// callWithSlowNotify is a convenience wrapper for *dbus.Call return type.
// Kept for backward compatibility with tests.
func callWithSlowNotify(threshold time.Duration, notifier UpstreamNotifier, reqType approval.RequestType, items []approval.ItemInfo, fn func() *dbus.Call) *dbus.Call {
	return WithSlowNotify(threshold, notifier, reqType, items, fn)
}
