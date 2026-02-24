// Package notification provides desktop notifications for approval requests.
package notification

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/godbus/dbus/v5"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

const (
	notifyDest      = "org.freedesktop.Notifications"
	notifyPath      = "/org/freedesktop/Notifications"
	notifyInterface = "org.freedesktop.Notifications"
)

// Notifier defines the interface for sending desktop notifications.
type Notifier interface {
	// Notify sends a notification and returns its ID.
	Notify(summary, body, icon string) (uint32, error)
	// Close closes a notification by ID.
	Close(id uint32) error
}

// DBusNotifier sends notifications via D-Bus.
type DBusNotifier struct {
	conn *dbus.Conn
}

// NewDBusNotifier creates a notifier using the session bus.
func NewDBusNotifier() (*DBusNotifier, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, fmt.Errorf("connect to session bus: %w", err)
	}
	return &DBusNotifier{conn: conn}, nil
}

// Notify sends a desktop notification.
func (n *DBusNotifier) Notify(summary, body, icon string) (uint32, error) {
	obj := n.conn.Object(notifyDest, notifyPath)
	call := obj.Call(
		notifyInterface+".Notify",
		0,
		"secrets-dispatcher", // app_name
		uint32(0),            // replaces_id (0 = new notification)
		icon,                 // app_icon
		summary,              // summary
		body,                 // body
		[]string{},           // actions
		map[string]dbus.Variant{
			"urgency": dbus.MakeVariant(byte(2)), // critical
		},
		int32(-1), // expire_timeout (-1 = server default)
	)
	if call.Err != nil {
		return 0, fmt.Errorf("notify call: %w", call.Err)
	}

	var id uint32
	if err := call.Store(&id); err != nil {
		return 0, fmt.Errorf("store notify result: %w", err)
	}
	return id, nil
}

// Close closes a notification by ID.
func (n *DBusNotifier) Close(id uint32) error {
	obj := n.conn.Object(notifyDest, notifyPath)
	call := obj.Call(notifyInterface+".CloseNotification", 0, id)
	if call.Err != nil {
		return fmt.Errorf("close notification: %w", call.Err)
	}
	return nil
}

// Handler receives approval events and shows desktop notifications.
type Handler struct {
	notifier Notifier

	mu            sync.Mutex
	notifications map[string]uint32 // request ID -> notification ID
}

// NewHandler creates a notification handler.
func NewHandler(notifier Notifier) *Handler {
	return &Handler{
		notifier:      notifier,
		notifications: make(map[string]uint32),
	}
}

// OnEvent implements approval.Observer.
func (h *Handler) OnEvent(event approval.Event) {
	switch event.Type {
	case approval.EventRequestCreated:
		h.handleCreated(event.Request)
	case approval.EventRequestApproved, approval.EventRequestDenied,
		approval.EventRequestExpired, approval.EventRequestCancelled:
		h.handleResolved(event.Request.ID)
	}
}

// notificationMeta returns the summary title and icon for a request based on its type.
func (h *Handler) notificationMeta(req *approval.Request) (summary, icon string) {
	switch req.Type {
	case approval.RequestTypeGPGSign:
		return "Commit Signing Request", "emblem-important"
	default:
		return "Secret Request", "dialog-password"
	}
}

func (h *Handler) handleCreated(req *approval.Request) {
	summary, icon := h.notificationMeta(req)
	body := h.formatBody(req)

	id, err := h.notifier.Notify(summary, body, icon)
	if err != nil {
		slog.Error("failed to send notification", "error", err, "request_id", req.ID)
		return
	}

	h.mu.Lock()
	h.notifications[req.ID] = id
	h.mu.Unlock()

	slog.Debug("sent desktop notification", "request_id", req.ID, "notification_id", id)
}

func (h *Handler) handleResolved(requestID string) {
	h.mu.Lock()
	notifID, ok := h.notifications[requestID]
	if ok {
		delete(h.notifications, requestID)
	}
	h.mu.Unlock()

	if !ok {
		return
	}

	if err := h.notifier.Close(notifID); err != nil {
		slog.Debug("failed to close notification", "error", err, "notification_id", notifID)
		return
	}

	slog.Debug("closed desktop notification", "request_id", requestID, "notification_id", notifID)
}

// commitSubject returns the first line of a commit message.
func commitSubject(msg string) string {
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		return msg[:i]
	}
	return msg
}

func (h *Handler) formatBody(req *approval.Request) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("Client: %s\n", req.Client))

	if req.SenderInfo.UnitName != "" {
		b.WriteString(fmt.Sprintf("Process: %s (PID %d)\n", req.SenderInfo.UnitName, req.SenderInfo.PID))
	} else if req.SenderInfo.PID != 0 {
		b.WriteString(fmt.Sprintf("PID: %d\n", req.SenderInfo.PID))
	}

	switch req.Type {
	case approval.RequestTypeGPGSign:
		if req.GPGSignInfo != nil {
			b.WriteString(fmt.Sprintf("Repo: %s\n", req.GPGSignInfo.RepoName))
			b.WriteString(commitSubject(req.GPGSignInfo.CommitMsg))
		}
	case approval.RequestTypeGetSecret:
		if len(req.Items) == 1 {
			b.WriteString(fmt.Sprintf("Secret: %s", req.Items[0].Label))
		} else {
			b.WriteString(fmt.Sprintf("Secrets: %d items", len(req.Items)))
		}
	case approval.RequestTypeSearch:
		b.WriteString("Type: search")
		if len(req.SearchAttributes) > 0 {
			attrs := make([]string, 0, len(req.SearchAttributes))
			for k, v := range req.SearchAttributes {
				attrs = append(attrs, fmt.Sprintf("%s=%s", k, v))
			}
			b.WriteString(fmt.Sprintf("\nQuery: %s", strings.Join(attrs, ", ")))
		}
	}

	return b.String()
}
