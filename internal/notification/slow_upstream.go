package notification

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/proxy"
)

// persistentNotifier sends persistent notifications and can dismiss them.
type persistentNotifier interface {
	NotifyPersistent(summary, body, icon string) (uint32, error)
	Close(id uint32) error
}

// SlowUpstreamNotifier shows informational desktop notifications when an
// upstream Secret Service call (e.g. GPG decrypt via pinentry) takes longer
// than expected. This gives the user context about what triggered the pinentry.
type SlowUpstreamNotifier struct {
	notifier persistentNotifier
}

// NewSlowUpstreamNotifier creates a SlowUpstreamNotifier backed by the given DBusNotifier.
func NewSlowUpstreamNotifier(notifier *DBusNotifier) *SlowUpstreamNotifier {
	return &SlowUpstreamNotifier{notifier: notifier}
}

// newSlowUpstreamNotifier creates a SlowUpstreamNotifier from any persistentNotifier (for testing).
func newSlowUpstreamNotifier(notifier persistentNotifier) *SlowUpstreamNotifier {
	return &SlowUpstreamNotifier{notifier: notifier}
}

// NotifySlowUpstream shows a notification with the secret label(s) and
// optional process chain, then returns a function that dismisses it.
func (s *SlowUpstreamNotifier) NotifySlowUpstream(ctx proxy.UpstreamCallContext) func() {
	summary := slowUpstreamSummary(ctx.RequestType)
	body := formatSlowUpstreamBody(ctx)

	id, err := s.notifier.NotifyPersistent(summary, body, "dialog-password")
	if err != nil {
		slog.Debug("failed to send slow upstream notification", "error", err)
		return func() {}
	}

	return func() {
		if err := s.notifier.Close(id); err != nil {
			slog.Debug("failed to dismiss slow upstream notification", "error", err, "notification_id", id)
		}
	}
}

// slowUpstreamSummary returns the notification summary based on request type.
func slowUpstreamSummary(reqType approval.RequestType) string {
	switch reqType {
	case approval.RequestTypeGPGSign:
		return "Signing commit"
	case approval.RequestTypeSSHSign:
		return "SSH key signing"
	case approval.RequestTypeSearch:
		return "Searching keyring"
	default:
		return "Waiting for keyring unlock"
	}
}

// itemDisplayLabel returns the item's Label if non-empty, otherwise its Path.
func itemDisplayLabel(item approval.ItemInfo) string {
	if item.Label != "" {
		return item.Label
	}
	return item.Path
}

// formatSlowUpstreamBody builds the notification body from item labels and
// optional process chain.
func formatSlowUpstreamBody(ctx proxy.UpstreamCallContext) string {
	var b strings.Builder

	// Item labels (fall back to Path when Label is empty)
	switch len(ctx.Items) {
	case 0:
		// no items
	case 1:
		b.WriteString(itemDisplayLabel(ctx.Items[0]))
	default:
		labels := make([]string, len(ctx.Items))
		for i, item := range ctx.Items {
			labels[i] = itemDisplayLabel(item)
		}
		fmt.Fprintf(&b, "%s (%d items)", strings.Join(labels, ", "), len(ctx.Items))
	}

	// Process chain (Name ← Name convention, matching desktop.go)
	if len(ctx.SenderInfo.ProcessChain) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		for i, p := range ctx.SenderInfo.ProcessChain {
			if i > 0 {
				b.WriteString(" \u2190 ")
			}
			b.WriteString(p.Name)
		}
	}

	return b.String()
}
