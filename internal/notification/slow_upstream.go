package notification

import (
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

// persistentNotifier sends persistent notifications and can dismiss them.
type persistentNotifier interface {
	NotifyPersistent(summary, body, icon string) (NotificationID, error)
	Close(id NotificationID) error
}

// Shows proxy.UpstreamNotifier's notifications on the desktop.
type SlowUpstreamNotifier struct {
	notifier persistentNotifier
}

func NewSlowUpstreamNotifier(notifier *DBusNotifier) *SlowUpstreamNotifier {
	return &SlowUpstreamNotifier{notifier: notifier}
}

func newSlowUpstreamNotifier(notifier persistentNotifier) *SlowUpstreamNotifier {
	return &SlowUpstreamNotifier{notifier: notifier}
}

func (s *SlowUpstreamNotifier) NotifySlowUpstream(req approval.RequestInfo) func() {
	summary := slowUpstreamSummary(req.Type)
	body := formatSlowUpstreamBody(req)

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

func slowUpstreamSummary(reqType approval.RequestType) string {
	switch reqType {
	case approval.RequestTypeGPGSign:
		return "Signing commit"
	case approval.RequestTypeSSHSign:
		return "SSH key signing"
	case approval.RequestTypeSearch:
		return "Searching keyring"
	case approval.RequestTypeUnlock:
		return "Unlocking keyring"
	default:
		return "Waiting for keyring unlock"
	}
}

// Returns the escaped display labels of the given items, with their count when
// there are several.
func itemsLine(items []approval.ItemInfo) string {
	labels := make([]string, len(items))
	for i, item := range items {
		labels[i] = escapeMarkup(item.DisplayLabel())
	}
	line := strings.Join(labels, ", ")
	if len(items) > 1 {
		line += fmt.Sprintf(" (%d items)", len(items))
	}
	return line
}

// Returns the markup body of a slow upstream notification for the given
// request. It names the request's items and its sender's process chain, with
// every value escaped (ADR 0007).
func formatSlowUpstreamBody(req approval.RequestInfo) string {
	lines := []string{
		itemsLine(req.Items),
		formatProcessChain(req.SenderInfo.ProcessChain, false),
	}
	return strings.Join(slices.DeleteFunc(lines, func(l string) bool { return l == "" }), "\n")
}
