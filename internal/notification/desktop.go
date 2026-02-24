// Package notification provides desktop notifications for approval requests.
package notification

import (
	"context"
	"errors"
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
	// The actions parameter takes alternating (id, label) pairs per the FreeDesktop spec.
	Notify(summary, body, icon string, actions []string) (uint32, error)
	// Close closes a notification by ID.
	Close(id uint32) error
}

// Approver resolves approval requests.
type Approver interface {
	Approve(id string) error
	Deny(id string) error
}

// Action represents a user interaction with a notification button.
type Action struct {
	NotificationID uint32
	ActionKey      string // "approve" or "deny"
}

// DBusNotifier sends notifications via D-Bus and listens for action button clicks.
type DBusNotifier struct {
	conn    *dbus.Conn
	signals chan *dbus.Signal
	actions chan Action
	done    chan struct{}
}

// NewDBusNotifier creates a notifier using the session bus and starts listening
// for ActionInvoked signals.
func NewDBusNotifier() (*DBusNotifier, error) {
	conn, err := dbus.SessionBus()
	if err != nil {
		return nil, fmt.Errorf("connect to session bus: %w", err)
	}

	n := &DBusNotifier{
		conn:    conn,
		signals: make(chan *dbus.Signal, 16),
		actions: make(chan Action, 16),
		done:    make(chan struct{}),
	}

	if err := conn.AddMatchSignal(
		dbus.WithMatchInterface(notifyInterface),
		dbus.WithMatchMember("ActionInvoked"),
	); err != nil {
		return nil, fmt.Errorf("subscribe to ActionInvoked: %w", err)
	}

	conn.Signal(n.signals)
	go n.processSignals()

	return n, nil
}

// Actions returns a channel that receives action button clicks.
func (n *DBusNotifier) Actions() <-chan Action {
	return n.actions
}

// Stop stops the signal listener goroutine.
func (n *DBusNotifier) Stop() {
	close(n.done)
}

func (n *DBusNotifier) processSignals() {
	defer close(n.actions)
	for {
		select {
		case <-n.done:
			return
		case sig, ok := <-n.signals:
			if !ok {
				return
			}
			if sig.Name != notifyInterface+".ActionInvoked" {
				continue
			}
			if len(sig.Body) != 2 {
				continue
			}
			id, ok1 := sig.Body[0].(uint32)
			key, ok2 := sig.Body[1].(string)
			if !ok1 || !ok2 {
				continue
			}
			select {
			case n.actions <- Action{NotificationID: id, ActionKey: key}:
			case <-n.done:
				return
			}
		}
	}
}

// Notify sends a desktop notification with optional action buttons.
func (n *DBusNotifier) Notify(summary, body, icon string, actions []string) (uint32, error) {
	obj := n.conn.Object(notifyDest, notifyPath)
	call := obj.Call(
		notifyInterface+".Notify",
		0,
		"secrets-dispatcher", // app_name
		uint32(0),            // replaces_id (0 = new notification)
		icon,                 // app_icon
		summary,              // summary
		body,                 // body
		actions,              // actions (alternating id, label pairs)
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
// It also processes notification action button clicks (Approve/Deny).
type Handler struct {
	notifier Notifier
	approver Approver

	mu            sync.Mutex
	notifications map[string]uint32 // request ID -> notification ID
	requests      map[uint32]string // notification ID -> request ID (reverse)
}

// NewHandler creates a notification handler.
func NewHandler(notifier Notifier, approver Approver) *Handler {
	return &Handler{
		notifier:      notifier,
		approver:      approver,
		notifications: make(map[string]uint32),
		requests:      make(map[uint32]string),
	}
}

// ListenActions reads from the actions channel and resolves requests.
// It blocks until the channel is closed or ctx is cancelled.
func (h *Handler) ListenActions(ctx context.Context, actions <-chan Action) {
	for {
		select {
		case <-ctx.Done():
			return
		case action, ok := <-actions:
			if !ok {
				return
			}
			h.handleAction(action)
		}
	}
}

func (h *Handler) handleAction(action Action) {
	h.mu.Lock()
	reqID, ok := h.requests[action.NotificationID]
	if ok {
		// Remove maps now so the handleResolved callback (fired synchronously
		// within Approve/Deny) won't call Close — clicking the action button
		// already dismisses the notification. This also prevents the daemon's
		// duplicate ActionInvoked signal (triggered by our Close call) from
		// reaching the approver.
		delete(h.requests, action.NotificationID)
		delete(h.notifications, reqID)
	}
	h.mu.Unlock()

	if !ok {
		return
	}

	var err error
	switch action.ActionKey {
	case "approve":
		err = h.approver.Approve(reqID)
	case "deny":
		err = h.approver.Deny(reqID)
	default:
		slog.Debug("unknown action key", "action", action.ActionKey, "request_id", reqID)
		return
	}

	if err != nil {
		if errors.Is(err, approval.ErrNotFound) {
			slog.Debug("request already resolved", "action", action.ActionKey, "request_id", reqID)
		} else {
			slog.Error("failed to resolve request from notification", "action", action.ActionKey, "request_id", reqID, "error", err)
		}
		return
	}

	slog.Info("resolved request from notification", "action", action.ActionKey, "request_id", reqID)
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
	actions := []string{"approve", "Approve", "deny", "Deny"}

	id, err := h.notifier.Notify(summary, body, icon, actions)
	if err != nil {
		slog.Error("failed to send notification", "error", err, "request_id", req.ID)
		return
	}

	h.mu.Lock()
	h.notifications[req.ID] = id
	h.requests[id] = req.ID
	h.mu.Unlock()

	slog.Debug("sent desktop notification", "request_id", req.ID, "notification_id", id)
}

func (h *Handler) handleResolved(requestID string) {
	h.mu.Lock()
	notifID, ok := h.notifications[requestID]
	if ok {
		delete(h.notifications, requestID)
		delete(h.requests, notifID)
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
