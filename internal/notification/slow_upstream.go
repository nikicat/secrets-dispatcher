package notification

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
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

// NotifySlowUpstream shows a notification with the secret label(s) and returns
// a function that dismisses it.
func (s *SlowUpstreamNotifier) NotifySlowUpstream(reqType approval.RequestType, items []approval.ItemInfo) func() {
	summary := slowUpstreamSummary(reqType)
	body := formatSlowUpstreamBody(items)

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

// formatSlowUpstreamBody builds the notification body from item labels.
func formatSlowUpstreamBody(items []approval.ItemInfo) string {
	if len(items) == 0 {
		return ""
	}
	if len(items) == 1 {
		return items[0].Label
	}
	labels := make([]string, len(items))
	for i, item := range items {
		labels[i] = item.Label
	}
	return fmt.Sprintf("%s (%d items)", strings.Join(labels, ", "), len(items))
}
