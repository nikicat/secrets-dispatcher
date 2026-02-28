// Package notification provides desktop notifications for approval requests.
package notification

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

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
	AutoApprove(requestID string) error
}

// Action represents a user interaction with a notification button.
type Action struct {
	NotificationID uint32
	ActionKey      string // "approve" or "deny"
}

// DBusNotifier sends notifications via D-Bus and listens for action button clicks.
// It automatically reconnects if the session bus connection drops.
type DBusNotifier struct {
	mu      sync.Mutex
	conn    *dbus.Conn
	signals chan *dbus.Signal
	actions chan Action
	done    chan struct{}
}

// NewDBusNotifier creates a notifier using a private session bus connection and
// starts listening for ActionInvoked signals.
func NewDBusNotifier() (*DBusNotifier, error) {
	n := &DBusNotifier{
		signals: make(chan *dbus.Signal, 16),
		actions: make(chan Action, 16),
		done:    make(chan struct{}),
	}

	if err := n.connect(); err != nil {
		return nil, err
	}

	go n.processSignals(n.signals)

	return n, nil
}

// connect establishes a private session bus connection and subscribes to
// ActionInvoked signals. Must be called with n.mu held (or during construction).
func (n *DBusNotifier) connect() error {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return fmt.Errorf("connect to session bus: %w", err)
	}

	if err := conn.AddMatchSignal(
		dbus.WithMatchInterface(notifyInterface),
		dbus.WithMatchMember("ActionInvoked"),
	); err != nil {
		conn.Close()
		return fmt.Errorf("subscribe to ActionInvoked: %w", err)
	}

	conn.Signal(n.signals)
	n.conn = conn
	return nil
}

// reconnect closes the dead connection and establishes a new one.
// It creates a fresh signals channel and restarts the processSignals goroutine
// (the old one exits when godbus closes its channel via Terminate).
// Must be called with n.mu held.
func (n *DBusNotifier) reconnect() error {
	if n.conn != nil {
		n.conn.Close()
	}
	n.signals = make(chan *dbus.Signal, 16)
	if err := n.connect(); err != nil {
		return fmt.Errorf("reconnect: %w", err)
	}
	go n.processSignals(n.signals)
	slog.Info("reconnected to D-Bus session bus")
	return nil
}

// Actions returns a channel that receives action button clicks.
func (n *DBusNotifier) Actions() <-chan Action {
	return n.actions
}

// Stop stops the signal listener goroutine and closes the D-Bus connection.
func (n *DBusNotifier) Stop() {
	close(n.done)
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.conn != nil {
		n.conn.Close()
	}
}

func (n *DBusNotifier) processSignals(ch <-chan *dbus.Signal) {
	for {
		select {
		case <-n.done:
			return
		case sig, ok := <-ch:
			if !ok {
				return // channel closed (connection died)
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
// If the D-Bus connection is dead, it reconnects and retries once.
func (n *DBusNotifier) Notify(summary, body, icon string, actions []string) (uint32, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	id, err := n.doNotify(summary, body, icon, actions)
	if err != nil && errors.Is(err, dbus.ErrClosed) {
		if reconnErr := n.reconnect(); reconnErr != nil {
			return 0, fmt.Errorf("notify call: %w (reconnect failed: %v)", err, reconnErr)
		}
		id, err = n.doNotify(summary, body, icon, actions)
	}
	return id, err
}

func (n *DBusNotifier) doNotify(summary, body, icon string, actions []string) (uint32, error) {
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
// If the D-Bus connection is dead, it reconnects and retries once.
func (n *DBusNotifier) Close(id uint32) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	err := n.doClose(id)
	if err != nil && errors.Is(err, dbus.ErrClosed) {
		if reconnErr := n.reconnect(); reconnErr != nil {
			return fmt.Errorf("close notification: %w (reconnect failed: %v)", err, reconnErr)
		}
		err = n.doClose(id)
	}
	return err
}

func (n *DBusNotifier) doClose(id uint32) error {
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
	baseURL  string
	showPIDs bool
	openURL  func(string) // injectable for testing; defaults to xdg-open

	mu            sync.Mutex
	notifications map[string]uint32 // request ID -> notification ID
	requests      map[uint32]string // notification ID -> request ID (reverse)

	// cancelledRequests stores recently cancelled requests for auto-approve lookup.
	// Keys are request IDs, values expire after 5 minutes.
	cancelledRequests map[string]cancelledEntry
}

type cancelledEntry struct {
	request   *approval.Request
	expiresAt time.Time
}

// NewHandler creates a notification handler.
// baseURL is the web UI URL opened when the user clicks the notification body.
func NewHandler(notifier Notifier, approver Approver, baseURL string, showPIDs bool) *Handler {
	return &Handler{
		notifier:          notifier,
		approver:          approver,
		baseURL:           baseURL,
		showPIDs:          showPIDs,
		openURL:           func(u string) { exec.Command("xdg-open", u).Start() },
		notifications:     make(map[string]uint32),
		requests:          make(map[uint32]string),
		cancelledRequests: make(map[string]cancelledEntry),
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
	case "default":
		h.openURL(h.baseURL + "?request=" + reqID)
		return
	case "approve":
		err = h.approver.Approve(reqID)
	case "deny":
		err = h.approver.Deny(reqID)
	case "auto_approve":
		err = h.approver.AutoApprove(reqID)
	case "dismiss":
		return // just close the notification, do nothing
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
	case approval.EventRequestCancelled:
		h.handleCancelled(event.Request)
	case approval.EventRequestApproved, approval.EventRequestDenied,
		approval.EventRequestExpired, approval.EventRequestAutoApproved:
		h.handleResolved(event.Request.ID)
	}
}

// notificationMeta returns the summary title and icon for a request based on its type.
func (h *Handler) notificationMeta(req *approval.Request) (summary, icon string) {
	switch req.Type {
	case approval.RequestTypeGPGSign:
		return "Sign commit", "emblem-important"
	case approval.RequestTypeSearch:
		return "Secrets searched", "dialog-password"
	case approval.RequestTypeDelete:
		return "Deletion requested", "dialog-warning"
	case approval.RequestTypeWrite:
		return "Secret write requested", "dialog-warning"
	default:
		return "Secret requested", "dialog-password"
	}
}

func (h *Handler) handleCreated(req *approval.Request) {
	summary, icon := h.notificationMeta(req)
	body := h.formatBody(req)
	actions := []string{"default", "", "approve", "Approve", "deny", "Deny"}

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

func (h *Handler) handleCancelled(req *approval.Request) {
	// Close the original approval notification
	h.mu.Lock()
	notifID, ok := h.notifications[req.ID]
	if ok {
		delete(h.notifications, req.ID)
		delete(h.requests, notifID)
	}
	h.mu.Unlock()

	if ok {
		if err := h.notifier.Close(notifID); err != nil {
			slog.Debug("failed to close notification", "error", err, "notification_id", notifID)
		}
	}

	// Store the cancelled request for auto-approve lookup
	h.mu.Lock()
	h.cancelledRequests[req.ID] = cancelledEntry{
		request:   req,
		expiresAt: time.Now().Add(5 * time.Minute),
	}
	// Clean expired entries
	now := time.Now()
	for id, entry := range h.cancelledRequests {
		if entry.expiresAt.Before(now) {
			delete(h.cancelledRequests, id)
		}
	}
	h.mu.Unlock()

	// Send a follow-up "Auto-approve?" notification
	invoker := req.SenderInfo.UnitName
	if invoker == "" {
		invoker = "client"
	}
	summary := fmt.Sprintf("%s timed out", invoker)
	body := "Auto-approve similar requests for 2 min?"
	actions := []string{"auto_approve", "Auto-approve", "dismiss", "Dismiss"}

	newID, err := h.notifier.Notify(summary, body, "dialog-question", actions)
	if err != nil {
		slog.Error("failed to send auto-approve notification", "error", err, "request_id", req.ID)
		return
	}

	h.mu.Lock()
	h.notifications[req.ID] = newID
	h.requests[newID] = req.ID
	h.mu.Unlock()

	slog.Debug("sent auto-approve notification", "request_id", req.ID, "notification_id", newID)
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

	switch req.Type {
	case approval.RequestTypeGPGSign:
		if req.GPGSignInfo != nil {
			fmt.Fprintf(&b, "<b>%s</b>: <i>%s</i>", req.GPGSignInfo.RepoName, commitSubject(req.GPGSignInfo.CommitMsg))
			if len(req.SenderInfo.ProcessChain) > 0 {
				for i, p := range req.SenderInfo.ProcessChain {
					if i == 0 {
						b.WriteString("\n")
					} else {
						b.WriteString(" ← ")
					}
					b.WriteString(p.Name)
					if h.showPIDs {
						fmt.Fprintf(&b, "[%d]", p.PID)
					}
				}
			}
		}
	default:
		if len(req.SenderInfo.ProcessChain) > 0 {
			// New format: item label, then process chain (parent → child order)
			switch req.Type {
			case approval.RequestTypeGetSecret, approval.RequestTypeDelete, approval.RequestTypeWrite:
				if len(req.Items) == 1 {
					fmt.Fprintf(&b, "<b>%s</b>", req.Items[0].Label)
				} else {
					fmt.Fprintf(&b, "<b>%d items</b>", len(req.Items))
				}
			case approval.RequestTypeSearch:
				if len(req.SearchAttributes) > 0 {
					attrs := make([]string, 0, len(req.SearchAttributes))
					for k, v := range req.SearchAttributes {
						attrs = append(attrs, fmt.Sprintf("%s=%s", k, v))
					}
					fmt.Fprintf(&b, "<b>%s</b>", strings.Join(attrs, ", "))
				} else {
					b.WriteString("<b>all</b>")
				}
			}
			chain := req.SenderInfo.ProcessChain
			for i, p := range chain {
				if i == 0 {
					b.WriteString("\n")
				} else {
					b.WriteString(" ← ")
				}
				b.WriteString(p.Name)
				if h.showPIDs {
					fmt.Fprintf(&b, "[%d]", p.PID)
				}
			}
		} else {
			// Fallback: old format for remote requests without process chain
			if req.SenderInfo.UnitName != "" {
				fmt.Fprintf(&b, "<b>%s</b>@%s[%d]: ", req.SenderInfo.UnitName, req.Client, req.SenderInfo.PID)
			} else if req.SenderInfo.PID != 0 {
				fmt.Fprintf(&b, "<b>%s</b>[%d]: ", req.Client, req.SenderInfo.PID)
			} else {
				fmt.Fprintf(&b, "<b>%s</b>: ", req.Client)
			}

			switch req.Type {
			case approval.RequestTypeGetSecret, approval.RequestTypeDelete, approval.RequestTypeWrite:
				if len(req.Items) == 1 {
					fmt.Fprintf(&b, "<i>%s</i>", req.Items[0].Label)
				} else {
					fmt.Fprintf(&b, "<i>%d items</i>", len(req.Items))
				}
			case approval.RequestTypeSearch:
				if len(req.SearchAttributes) > 0 {
					attrs := make([]string, 0, len(req.SearchAttributes))
					for k, v := range req.SearchAttributes {
						attrs = append(attrs, fmt.Sprintf("%s=%s", k, v))
					}
					fmt.Fprintf(&b, "<i>%s</i>", strings.Join(attrs, ", "))
				} else {
					b.WriteString("<i>all</i>")
				}
			}
		}
	}

	return b.String()
}
