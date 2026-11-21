package proxy

import (
	"context"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

// What a slow upstream notification shows about one upstream call.
type UpstreamCallContext struct {
	request       approval.RequestInfo
	resolveSender func() approval.SenderInfo
	prompted      bool
}

// Returns the context of an upstream call made for info that no prompt gated.
func RequestCall(info approval.RequestInfo) UpstreamCallContext {
	return UpstreamCallContext{request: info}
}

// Returns the context of an upstream call made for info after the user
// approved info at a prompt.
func PromptedCall(info approval.RequestInfo) UpstreamCallContext {
	return UpstreamCallContext{request: info, prompted: true}
}

// Returns the context of an upstream call that serves no approval request.
// resolve is called only if the call's slow upstream notification fires.
func SenderCall(resolve func() approval.SenderInfo) UpstreamCallContext {
	return UpstreamCallContext{resolveSender: resolve}
}

// Requires the user's approval of info, as approval.Manager.RequireApproval
// does, and returns the context of the upstream call made for info. Errors as
// RequireApproval does.
func requireApproval(ctx context.Context, m *approval.Manager, info approval.RequestInfo) (UpstreamCallContext, error) {
	prompted, err := m.RequireApproval(ctx, info)
	if prompted {
		return PromptedCall(info), err
	}
	return RequestCall(info), err
}

// UpstreamNotifier shows informational notifications about slow upstream calls.
type UpstreamNotifier interface {
	// Shows a notification about a slow upstream call made for req, and returns a
	// function that dismisses it.
	NotifySlowUpstream(req approval.RequestInfo) func()
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
	return s.Notifier != nil && s.Threshold > 0 && (s.Always || !ctx.prompted)
}

// Returns fn's result. Shows a notification through slow.Notifier while fn
// runs past slow.Threshold, unless SlowUpstream.reports rejects ctx. The sender
// of ctx is resolved only when that notification fires.
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
		req := ctx.request
		if ctx.resolveSender != nil {
			req.SenderInfo = ctx.resolveSender()
		}
		dismiss := slow.Notifier.NotifySlowUpstream(req)
		result := <-done
		dismiss()
		return result
	}
}
