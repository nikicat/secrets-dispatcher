package proxy

import (
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

// UpstreamCallContext carries caller and item information for slow upstream
// notifications. SenderInfo is pre-filled for approval-gated calls;
// ResolveSender is set for non-gated calls that have access to a D-Bus
// message.
type UpstreamCallContext struct {
	RequestType   approval.RequestType
	Items         []approval.ItemInfo
	SenderInfo    approval.SenderInfo
	ResolveSender func() approval.SenderInfo // nil if unavailable
	// True if the user approved the request at a prompt.
	Prompted bool
}

// UpstreamNotifier shows informational notifications about slow upstream calls.
type UpstreamNotifier interface {
	// NotifySlowUpstream shows a notification that the upstream is likely
	// waiting for pinentry. Returns a function to dismiss it.
	NotifySlowUpstream(ctx UpstreamCallContext) func()
}

type propResult struct {
	v   dbus.Variant
	err error
}

// Where to report an upstream call that outlasts Threshold.
// The zero value reports nothing.
type SlowUpstream struct {
	Notifier  UpstreamNotifier
	Threshold time.Duration
	// True if the calls of prompted requests are reported too.
	Always bool
}

// Reports whether a call made for ctx gets a notification once it outlasts
// Threshold.
func (s SlowUpstream) reports(ctx UpstreamCallContext) bool {
	return s.Notifier != nil && s.Threshold > 0 && (s.Always || !ctx.Prompted)
}

// Returns fn's result. Shows a notification through slow.Notifier while fn runs
// past slow.Threshold, for a call that SlowUpstream.reports accepts, and calls
// ctx.ResolveSender only when that notification fires and ctx.SenderInfo is empty.
func WithSlowNotify[T any](slow SlowUpstream, ctx UpstreamCallContext, fn func() T) T {
	if !slow.reports(ctx) {
		return fn()
	}

	done := make(chan T, 1)
	go func() {
		done <- fn()
	}()

	timer := time.NewTimer(slow.Threshold)
	defer timer.Stop()

	select {
	case result := <-done:
		return result
	case <-timer.C:
		if ctx.SenderInfo.PID == 0 && len(ctx.SenderInfo.ProcessChain) == 0 && ctx.ResolveSender != nil {
			ctx.SenderInfo = ctx.ResolveSender()
		}
		dismiss := slow.Notifier.NotifySlowUpstream(ctx)
		result := <-done
		dismiss()
		return result
	}
}
