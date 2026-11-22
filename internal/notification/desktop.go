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
	"github.com/nikicat/secrets-dispatcher/internal/config"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/delaygroup"
	"github.com/nikicat/secrets-dispatcher/internal/procutil"
)

const (
	notifyDest                          = "org.freedesktop.Notifications"
	notifyPath                          = "/org/freedesktop/Notifications"
	notifyInterface dbustypes.Interface = "org.freedesktop.Notifications"
)

// The capacity of a notifier's signal, action and closed channels.
const signalBuffer = 16

// The ID a notification server assigns to a notification it shows.
type NotificationID uint32

// Notifier defines the interface for sending desktop notifications.
type Notifier interface {
	// Notify sends a notification and returns its ID.
	Notify(summary, body, icon string, buttons []Button) (NotificationID, error)
	// Close closes a notification by ID.
	Close(id NotificationID) error
}

// Approver resolves approval requests.
type Approver interface {
	Approve(id approval.RequestID) error
	Deny(id approval.RequestID) error
	AutoApprove(requestID approval.RequestID) error
	ApproveAndAutoApprove(id approval.RequestID) error
}

// An action's identifier on a notification. The notification server sends it
// back in ActionInvoked when the user picks the action (ADR 0007).
type actionKey string

const (
	// Invoked when the user clicks the notification body, per the freedesktop
	// notification spec.
	actionDefault               actionKey = "default"
	actionApprove               actionKey = "approve"
	actionApproveAndAutoApprove actionKey = "approve_and_auto_approve"
	actionDeny                  actionKey = "deny"
	actionAutoApprove           actionKey = "auto_approve"
	actionDismiss               actionKey = "dismiss"
)

type Button struct {
	Key   actionKey
	Label string
}

// Action represents a user interaction with a notification button.
type Action struct {
	NotificationID NotificationID
	ActionKey      actionKey
}

// Closed reports a NotificationClosed signal for a notification we sent.
// The server directs these at the sending connection only, so this is the
// one place they can be observed (third-party monitors never see them).
type Closed struct {
	NotificationID NotificationID
	Reason         uint32 // 1=expired, 2=dismissed by user, 3=CloseNotification, 4=undefined
}

// DBusNotifier sends notifications via D-Bus and listens for action button clicks.
// It automatically reconnects if the session bus connection drops.
type DBusNotifier struct {
	mu      sync.Mutex
	conn    *dbus.Conn
	signals chan *dbus.Signal
	actions chan Action
	closed  chan Closed
	done    chan struct{}
}

// NewDBusNotifier creates a notifier using a private session bus connection and
// starts listening for ActionInvoked and NotificationClosed signals.
func NewDBusNotifier() (*DBusNotifier, error) {
	n := &DBusNotifier{
		signals: make(chan *dbus.Signal, signalBuffer),
		actions: make(chan Action, signalBuffer),
		closed:  make(chan Closed, signalBuffer),
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
		dbus.WithMatchInterface(string(notifyInterface)),
		dbus.WithMatchMember("ActionInvoked"),
	); err != nil {
		conn.Close()
		return fmt.Errorf("subscribe to ActionInvoked: %w", err)
	}
	if err := conn.AddMatchSignal(
		dbus.WithMatchInterface(string(notifyInterface)),
		dbus.WithMatchMember("NotificationClosed"),
	); err != nil {
		conn.Close()
		return fmt.Errorf("subscribe to NotificationClosed: %w", err)
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
	n.signals = make(chan *dbus.Signal, signalBuffer)
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

// ClosedEvents returns a channel that receives NotificationClosed signals for
// notifications sent on this connection. Reason 1 (expired) on an approval
// notification means the server took it down before the user acted on it,
// which ADR 0007 rules out.
//
// Only the e2e notifprobe consumes this; the daemon does not. A caller that
// never drains it will NOT stall the notifier — processSignals drops closed
// events when the buffer is full rather than blocking (see processSignals).
func (n *DBusNotifier) ClosedEvents() <-chan Closed {
	return n.closed
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
			if len(sig.Body) != 2 {
				continue
			}
			// The sends below must never block: this goroutine is the sole
			// reader of the godbus signal channel, so blocking here stops ALL
			// further ActionInvoked/NotificationClosed delivery for the life of
			// the connection. A wedged loop silently kills every approval button
			// (requests then hang until the client times out). Drop with a log
			// instead — a full buffer means a downstream consumer stalled, and
			// keeping the loop alive is more important than any single event.
			switch sig.Name {
			case notifyInterface.Member("ActionInvoked"):
				id, ok1 := sig.Body[0].(uint32)
				key, ok2 := sig.Body[1].(string)
				if !ok1 || !ok2 {
					continue
				}
				select {
				case n.actions <- Action{NotificationID: NotificationID(id), ActionKey: actionKey(key)}:
				case <-n.done:
					return
				default:
					slog.Warn("dropped notification action: buffer full", "notification_id", id, "action", key)
				}
			case notifyInterface.Member("NotificationClosed"):
				id, ok1 := sig.Body[0].(uint32)
				reason, ok2 := sig.Body[1].(uint32)
				if !ok1 || !ok2 {
					continue
				}
				// Only the e2e notifprobe drains ClosedEvents(); the daemon never
				// calls it, so in the daemon this send fills the buffer and (before
				// the default) wedged the loop after signalBuffer closes. Dropping
				// when full is safe — nothing in the daemon acts on these events.
				select {
				case n.closed <- Closed{NotificationID: NotificationID(id), Reason: reason}:
				case <-n.done:
					return
				default:
				}
			}
		}
	}
}

// Notify sends a desktop notification with optional action buttons.
// If the D-Bus connection is dead, it reconnects and retries once.
//
// expire_timeout must be 0 (never expire): spec-honoring daemons (dunst,
// mako, KDE) close a -1 ("server default") notification after their default
// timeout — the user would have to race the banner to approve anything.
// gnome-shell ignores the field entirely; there it is critical
// urgency + no transient hint that keep the notification alive (GNOME 46
// sources, docs/adr/0007-approval-surfaces.md). Approvals are closed
// explicitly via Close when resolved.
func (n *DBusNotifier) Notify(summary, body, icon string, buttons []Button) (NotificationID, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	id, err := n.doNotifyFull(summary, body, icon, buttons, 2, 0) // critical, never expire
	if err != nil && errors.Is(err, dbus.ErrClosed) {
		if reconnErr := n.reconnect(); reconnErr != nil {
			return 0, fmt.Errorf("notify call: %w (reconnect failed: %v)", err, reconnErr)
		}
		id, err = n.doNotifyFull(summary, body, icon, buttons, 2, 0)
	}
	return id, err
}

func (n *DBusNotifier) doNotifyFull(summary, body, icon string, buttons []Button, urgency byte, expireTimeout int32) (NotificationID, error) {
	// The freedesktop notification spec takes actions as a flat list of
	// alternating key, label pairs.
	actions := make([]string, 0, 2*len(buttons))
	for _, b := range buttons {
		actions = append(actions, string(b.Key), b.Label)
	}
	obj := n.conn.Object(notifyDest, notifyPath)
	call := obj.Call(
		notifyInterface.Member("Notify"),
		0,
		"secrets-dispatcher", // app_name
		uint32(0),            // replaces_id (0 = new notification)
		icon,                 // app_icon
		summary,
		body,
		actions,
		map[string]dbus.Variant{
			"urgency": dbus.MakeVariant(urgency),
		},
		expireTimeout,
	)
	if call.Err != nil {
		return 0, fmt.Errorf("notify call: %w", call.Err)
	}

	var id uint32
	if err := call.Store(&id); err != nil {
		return 0, fmt.Errorf("store notify result: %w", err)
	}
	return NotificationID(id), nil
}

// NotifyPersistent sends a critical notification that never auto-expires.
// Dismiss it with Close (CloseNotification).
func (n *DBusNotifier) NotifyPersistent(summary, body, icon string) (NotificationID, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	id, err := n.doNotifyFull(summary, body, icon, nil, 2, 0) // critical, never expire
	if err != nil && errors.Is(err, dbus.ErrClosed) {
		if reconnErr := n.reconnect(); reconnErr != nil {
			return 0, fmt.Errorf("notify call: %w (reconnect failed: %v)", err, reconnErr)
		}
		id, err = n.doNotifyFull(summary, body, icon, nil, 2, 0)
	}
	return id, err
}

// Close closes a notification by ID.
// If the D-Bus connection is dead, it reconnects and retries once.
func (n *DBusNotifier) Close(id NotificationID) error {
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

func (n *DBusNotifier) doClose(id NotificationID) error {
	obj := n.conn.Object(notifyDest, notifyPath)
	call := obj.Call(notifyInterface.Member("CloseNotification"), 0, uint32(id))
	if call.Err != nil {
		return fmt.Errorf("close notification: %w", call.Err)
	}
	return nil
}

// Handler receives approval events and shows desktop notifications.
// It also processes notification action button clicks (Approve/Deny).
type Handler struct {
	notifier   Notifier
	approver   Approver
	listenAddr config.ListenAddr
	showPIDs   bool
	// The auto-approve duration as notifications show it, such as "2m".
	autoApproveLabel  string
	notificationDelay time.Duration
	openURL           func(string) error

	mu            sync.Mutex
	notifications map[approval.RequestID]NotificationID
	requests      map[NotificationID]approval.RequestID
	pending       *delaygroup.Group[approval.RequestID] // notifications waiting for the grace period

	// cancelledRequests stores recently cancelled requests for auto-approve lookup.
	// Keys are request IDs, values expire after 5 minutes.
	cancelledRequests map[approval.RequestID]cancelledEntry
}

var _ approval.Observer = (*Handler)(nil)

type cancelledEntry struct {
	request   *approval.Request
	expiresAt time.Time
}

// Opens url in the user's browser with xdg-open, without waiting for it to exit.
// Errors when xdg-open cannot be started.
func OpenURL(url string) error {
	cmd := exec.Command("xdg-open", url)
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait() // Reaps the child, so it leaves no zombie.
	return nil
}

// Returns a handler that shows a request's notification only once the given
// notification delay has passed, so a request that ends sooner shows none.
func NewHandler(notifier Notifier, approver Approver, listenAddr config.ListenAddr, showPIDs bool, autoApproveDuration, notificationDelay time.Duration) *Handler {
	return &Handler{
		notifier:          notifier,
		approver:          approver,
		listenAddr:        listenAddr,
		showPIDs:          showPIDs,
		autoApproveLabel:  formatDurationShort(autoApproveDuration),
		notificationDelay: notificationDelay,
		openURL:           OpenURL,
		notifications:     make(map[approval.RequestID]NotificationID),
		requests:          make(map[NotificationID]approval.RequestID),
		pending:           delaygroup.New[approval.RequestID](),
		cancelledRequests: make(map[approval.RequestID]cancelledEntry),
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
		// The click already dismissed the notification, so forget it before
		// resolving: a Close from handleResolved would make the server send a
		// duplicate ActionInvoked.
		delete(h.requests, action.NotificationID)
		delete(h.notifications, reqID)
	}
	h.mu.Unlock()

	if !ok {
		return
	}

	var err error
	switch action.ActionKey {
	case actionDefault:
		if err := h.openURL(h.listenAddr.RequestURL(reqID)); err != nil {
			slog.Warn("failed to open the web UI from notification", "action", action.ActionKey, "request_id", reqID, "error", err)
		}
		return
	case actionApprove:
		err = h.approver.Approve(reqID)
	case actionApproveAndAutoApprove:
		err = h.approver.ApproveAndAutoApprove(reqID)
	case actionDeny:
		err = h.approver.Deny(reqID)
	case actionAutoApprove:
		err = h.approver.AutoApprove(reqID)
	case actionDismiss:
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

func (h *Handler) RequestCreated(p approval.Pending) {
	if h.notificationDelay <= 0 {
		h.sendNotification(p.Request)
		return
	}
	h.pending.Schedule(p.Request.ID, h.notificationDelay, func() {
		h.sendNotification(p.Request)
	})
}

func (h *Handler) RequestEnded(entry approval.HistoryEntry) {
	if entry.Resolution == approval.ResolutionCancelled {
		h.handleCancelled(entry.Request)
		return
	}
	h.handleResolved(entry.Request.ID)
}

// Rules show only in the web UI.
func (h *Handler) RuleAdded(approval.AutoApproveRule) {}

// Rules show only in the web UI.
func (h *Handler) RuleRemoved(approval.AutoApproveRule) {}

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
	case approval.RequestTypeSSHSign:
		return "SSH key requested", "dialog-password"
	default:
		return "Secret requested", "dialog-password"
	}
}

// formatDurationShort formats a duration as a compact human-readable string (e.g. "2m", "90s").
func formatDurationShort(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	m := int(d.Minutes())
	s := int(d.Seconds()) % 60
	if m > 0 && s == 0 {
		return fmt.Sprintf("%dm", m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

func (h *Handler) approvalButtons(req *approval.Request) []Button {
	buttons := []Button{{actionDefault, ""}, {actionApprove, "Approve"}}
	if req.AutoApprovable() {
		buttons = append(buttons, Button{actionApproveAndAutoApprove, "Approve " + h.autoApproveLabel})
	}
	return append(buttons, Button{actionDeny, "Deny"})
}

func (h *Handler) sendNotification(req *approval.Request) {
	summary, icon := h.notificationMeta(req)
	body := h.formatBody(req)
	id, err := h.notifier.Notify(summary, body, icon, h.approvalButtons(req))
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
	if h.pending.Cancel(req.ID) {
		slog.Debug("suppressed notification for quickly-cancelled request", "request_id", req.ID)
		return // no notification was shown, skip the follow-up too
	}

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

	// The follow-up only offers an auto-approve rule.
	if !req.AutoApprovable() {
		return
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
	invoker := req.SenderInfo.InvokerName
	if invoker == "" {
		invoker = "client"
	}
	summary := fmt.Sprintf("%s timed out", invoker)
	body := fmt.Sprintf("Auto-approve similar requests for %s?", h.autoApproveLabel)
	buttons := []Button{{actionAutoApprove, "Auto-approve"}, {actionDismiss, "Dismiss"}}

	newID, err := h.notifier.Notify(summary, body, "dialog-question", buttons)
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

func (h *Handler) handleResolved(requestID approval.RequestID) {
	if h.pending.Cancel(requestID) {
		return
	}

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

// Escapes &, < and >, the characters special in a notification body, and
// leaves quotes alone (ADR 0007).
var markupEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func escapeMarkup(s string) string {
	return markupEscaper.Replace(s)
}

// Returns the process names of chain joined by " ← ", escaped for a
// notification body (ADR 0007). Each name is followed by its PID in brackets
// when showPIDs is true.
func formatProcessChain(chain []procutil.Process, showPIDs bool) string {
	names := make([]string, len(chain))
	for i, p := range chain {
		names[i] = escapeMarkup(p.Name)
		if showPIDs {
			names[i] += fmt.Sprintf("[%d]", p.PID)
		}
	}
	return strings.Join(names, " ← ")
}

// commitSubject returns the first line of a commit message.
func commitSubject(msg string) string {
	if before, _, ok := strings.Cut(msg, "\n"); ok {
		return before
	}
	return msg
}

// Returns the markup header "<b>name</b>@client[pid]: " for s, with each value
// escaped. The name and the pid are left out when unknown. A request without a
// process chain is shown by the client it came from (ADR 0003).
func senderHeader(s approval.SenderInfo) string {
	client := escapeMarkup(string(s.Client))
	if name := s.DisplayName(); name != "" {
		return fmt.Sprintf("<b>%s</b>@%s[%d]: ", escapeMarkup(name), client, s.PID)
	}
	if s.PID != 0 {
		return fmt.Sprintf("<b>%s</b>[%d]: ", client, s.PID)
	}
	return fmt.Sprintf("<b>%s</b>: ", client)
}

func (h *Handler) formatBody(req *approval.Request) string {
	var b strings.Builder

	// Every value that a client controls is escaped (ADR 0007).
	esc := escapeMarkup

	writeChain := func(chain []procutil.Process) {
		if len(chain) > 0 {
			b.WriteByte('\n')
			b.WriteString(formatProcessChain(chain, h.showPIDs))
		}
	}

	search, _ := req.Search()
	// escapedSearchAttrs renders "k=v, k=v" with keys and values escaped.
	escapedSearchAttrs := func() string {
		attrs := make([]string, 0, len(search.Attributes))
		for k, v := range search.Attributes {
			attrs = append(attrs, fmt.Sprintf("%s=%s", esc(k), esc(v)))
		}
		return strings.Join(attrs, ", ")
	}

	switch req.Type {
	case approval.RequestTypeGPGSign:
		if sign, ok := req.GPGSign(); ok {
			fmt.Fprintf(&b, "<b>%s</b>: <i>%s</i>", esc(string(sign.RepoName)), esc(commitSubject(sign.Signed.Message)))
			writeChain(req.SenderInfo.ProcessChain)
		}
	case approval.RequestTypeSSHSign:
		if len(req.Items) > 0 {
			fmt.Fprintf(&b, "<b>%s</b>", esc(req.Items[0].DisplayLabel()))
			if dest := req.Items[0].SSHDestination(); dest != "" {
				fmt.Fprintf(&b, " → %s", esc(dest))
			}
		}
		writeChain(req.SenderInfo.ProcessChain)
	default:
		if len(req.SenderInfo.ProcessChain) > 0 {
			switch req.Type {
			case approval.RequestTypeGetSecret, approval.RequestTypeDelete, approval.RequestTypeWrite:
				if len(req.Items) == 1 {
					fmt.Fprintf(&b, "<b>%s</b>", esc(req.Items[0].DisplayLabel()))
				} else {
					fmt.Fprintf(&b, "<b>%d items</b>", len(req.Items))
				}
			case approval.RequestTypeSearch:
				if len(search.Attributes) > 0 {
					fmt.Fprintf(&b, "<b>%s</b>", escapedSearchAttrs())
				} else {
					b.WriteString("<b>all</b>")
				}
			}
			writeChain(req.SenderInfo.ProcessChain)
		} else {
			b.WriteString(senderHeader(req.SenderInfo))

			switch req.Type {
			case approval.RequestTypeGetSecret, approval.RequestTypeDelete, approval.RequestTypeWrite:
				if len(req.Items) == 1 {
					fmt.Fprintf(&b, "<i>%s</i>", esc(req.Items[0].DisplayLabel()))
				} else {
					fmt.Fprintf(&b, "<i>%d items</i>", len(req.Items))
				}
			case approval.RequestTypeSearch:
				if len(search.Attributes) > 0 {
					fmt.Fprintf(&b, "<i>%s</i>", escapedSearchAttrs())
				} else {
					b.WriteString("<i>all</i>")
				}
			}
		}
	}

	return b.String()
}
