// Package notification provides desktop notifications for approval requests.
package notification

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"sort"
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
	portalDest      = "org.freedesktop.portal.Desktop"
	portalPath      = "/org/freedesktop/portal/desktop"
	portalOpenURI   = "org.freedesktop.portal.OpenURI.OpenURI"
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
	ApproveAndAutoApprove(id string) error
}

// Action represents a user interaction with a notification button.
type Action struct {
	NotificationID uint32
	ActionKey      string // "approve" or "deny"
}

// Closed reports a NotificationClosed signal for a notification we sent.
// The server directs these at the sending connection only, so this is the
// one place they can be observed (third-party monitors never see them).
type Closed struct {
	NotificationID uint32
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
		signals: make(chan *dbus.Signal, 16),
		actions: make(chan Action, 16),
		closed:  make(chan Closed, 16),
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
	if err := conn.AddMatchSignal(
		dbus.WithMatchInterface(notifyInterface),
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

// ClosedEvents returns a channel that receives NotificationClosed signals for
// notifications sent on this connection. Reason 1 (expired) on an approval
// notification means the server auto-dismissed it — the US-7 failure mode.
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
			case notifyInterface + ".ActionInvoked":
				id, ok1 := sig.Body[0].(uint32)
				key, ok2 := sig.Body[1].(string)
				if !ok1 || !ok2 {
					continue
				}
				select {
				case n.actions <- Action{NotificationID: id, ActionKey: key}:
				case <-n.done:
					return
				default:
					slog.Warn("dropped notification action: buffer full", "notification_id", id, "action", key)
				}
			case notifyInterface + ".NotificationClosed":
				id, ok1 := sig.Body[0].(uint32)
				reason, ok2 := sig.Body[1].(uint32)
				if !ok1 || !ok2 {
					continue
				}
				// Only the e2e notifprobe drains ClosedEvents(); the daemon never
				// calls it, so in the daemon this send fills the buffer and (before
				// the default) wedged the loop after 16 closes. Dropping when full
				// is safe — nothing in the daemon acts on these events.
				select {
				case n.closed <- Closed{NotificationID: id, Reason: reason}:
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
// timeout — the user would have to race the banner to approve anything
// (US-7). gnome-shell ignores the field entirely; there it is critical
// urgency + no transient hint that keep the notification alive (GNOME 46
// sources, docs/plans/onboarding-and-e2e.md). Approvals are closed
// explicitly via Close when resolved.
func (n *DBusNotifier) Notify(summary, body, icon string, actions []string) (uint32, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	id, err := n.doNotifyFull(summary, body, icon, actions, 2, 0) // critical, never expire
	if err != nil && errors.Is(err, dbus.ErrClosed) {
		if reconnErr := n.reconnect(); reconnErr != nil {
			return 0, fmt.Errorf("notify call: %w (reconnect failed: %v)", err, reconnErr)
		}
		id, err = n.doNotifyFull(summary, body, icon, actions, 2, 0)
	}
	return id, err
}

func (n *DBusNotifier) doNotifyFull(summary, body, icon string, actions []string, urgency byte, expireTimeout int32) (uint32, error) {
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
	return id, nil
}

// NotifyPersistent sends a critical notification that never auto-expires.
// Dismiss it with Close (CloseNotification).
func (n *DBusNotifier) NotifyPersistent(summary, body, icon string) (uint32, error) {
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

// delayGroup schedules callbacks by key with a grace period.
// If Cancel is called before the timer fires, the callback is suppressed.
type delayGroup struct {
	mu     sync.Mutex
	timers map[string]*time.Timer
}

func newDelayGroup() *delayGroup {
	return &delayGroup{timers: make(map[string]*time.Timer)}
}

// Schedule registers a callback to fire after delay. If a timer already exists
// for the key it is replaced. The callback is only invoked if the timer is
// still pending (not cancelled) when it fires.
func (g *delayGroup) Schedule(key string, delay time.Duration, fn func()) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if old, ok := g.timers[key]; ok {
		old.Stop()
	}
	g.timers[key] = time.AfterFunc(delay, func() {
		g.mu.Lock()
		_, stillPending := g.timers[key]
		if stillPending {
			delete(g.timers, key)
		}
		g.mu.Unlock()
		if stillPending {
			fn()
		}
	})
}

// Cancel stops a pending timer and returns true if one was pending.
func (g *delayGroup) Cancel(key string) bool {
	g.mu.Lock()
	timer, ok := g.timers[key]
	if ok {
		delete(g.timers, key)
	}
	g.mu.Unlock()
	if ok {
		timer.Stop()
	}
	return ok
}

// Handler receives approval events and shows desktop notifications.
// It also processes notification action button clicks (Approve/Deny).
type Handler struct {
	notifier            Notifier
	approver            Approver
	baseURL             string
	showPIDs            bool
	autoApproveDuration time.Duration
	notificationDelay   time.Duration
	openURL             func(string) // injectable for testing; defaults to xdg-open
	requestURL          func(string) (string, error)

	mu            sync.Mutex
	notifications map[string]uint32 // request ID -> notification ID
	requests      map[uint32]string // notification ID -> request ID (reverse)
	requestData   map[string]*approval.Request
	pending       *delayGroup // notifications waiting for the grace period

	// cancelledRequests stores recently cancelled requests for auto-approve lookup.
	// Keys are request IDs, values expire after 5 minutes.
	cancelledRequests map[string]cancelledEntry
}

// SetRequestURLBuilder configures authenticated URLs for notification actions.
// It must be called during startup, before ListenActions begins processing.
func (h *Handler) SetRequestURLBuilder(builder func(string) (string, error)) {
	h.requestURL = builder
}

type cancelledEntry struct {
	request   *approval.Request
	expiresAt time.Time
}

// NewHandler creates a notification handler.
// baseURL is the web UI URL opened when the user clicks the notification body.
// autoApproveDuration controls the label shown on the "Approve Nm" button and
// the body text of the post-timeout auto-approve notification.
// notificationDelay is the grace period before showing a desktop notification;
// requests cancelled within this window produce no notification at all.
func NewHandler(notifier Notifier, approver Approver, baseURL string, showPIDs bool, autoApproveDuration, notificationDelay time.Duration) *Handler {
	return &Handler{
		notifier:            notifier,
		approver:            approver,
		baseURL:             baseURL,
		showPIDs:            showPIDs,
		autoApproveDuration: autoApproveDuration,
		notificationDelay:   notificationDelay,
		openURL: func(u string) {
			go func() {
				if err := openDesktopURL(u); err != nil {
					slog.Error("failed to open notification admin URL", "error", err)
				}
			}()
		},
		notifications:     make(map[string]uint32),
		requests:          make(map[uint32]string),
		requestData:       make(map[string]*approval.Request),
		pending:           newDelayGroup(),
		cancelledRequests: make(map[string]cancelledEntry),
	}
}

var startDesktopURL = func(rawURL string) error {
	return exec.Command("xdg-open", rawURL).Start()
}

func openDesktopURL(rawURL string) error {
	// Match `secrets-dispatcher login`: xdg-open is the proven path from the
	// user-service environment. A portal OpenURI call is asynchronous, so a
	// successful method return only means the request was accepted, not that a
	// browser was actually opened.
	xdgErr := startDesktopURL(rawURL)
	if xdgErr == nil {
		return nil
	}

	conn, portalErr := dbus.ConnectSessionBus()
	if portalErr == nil {
		call := conn.Object(portalDest, dbus.ObjectPath(portalPath)).Call(
			portalOpenURI,
			0,
			"", // No parent window: the request originates from a notification action.
			rawURL,
			map[string]dbus.Variant{},
		)
		portalErr = call.Err
		conn.Close()
		if portalErr == nil {
			return nil
		}
	}

	return fmt.Errorf("xdg-open: %w; desktop portal: %v", xdgErr, portalErr)
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
	if action.ActionKey == "details" || action.ActionKey == "default" {
		h.handleDetails(action.NotificationID)
		return
	}

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
		delete(h.requestData, reqID)
	}
	h.mu.Unlock()

	if !ok {
		return
	}

	var err error
	switch action.ActionKey {
	case "approve":
		err = h.approver.Approve(reqID)
	case "approve_and_auto_approve":
		err = h.approver.ApproveAndAutoApprove(reqID)
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

func (h *Handler) handleDetails(notificationID uint32) {
	h.mu.Lock()
	reqID, ok := h.requests[notificationID]
	req := h.requestData[reqID]
	h.mu.Unlock()
	if !ok || req == nil {
		return
	}

	targetURL := h.baseURL + "?request=" + reqID
	if h.requestURL != nil {
		var err error
		targetURL, err = h.requestURL(reqID)
		if err != nil {
			slog.Error("failed to create authenticated request URL", "error", err, "request_id", reqID)
		} else {
			h.openURL(targetURL)
		}
	} else {
		h.openURL(targetURL)
	}

	// GNOME dismisses a notification after invoking any action. Reissue the
	// still-pending request after the action completes so approval remains
	// visible without granting or denying it.
	time.AfterFunc(150*time.Millisecond, func() {
		summary, icon := h.notificationMeta(req)
		newID, err := h.notifier.Notify(summary, h.formatBody(req)+detailsHint(), icon, h.approvalActions())
		if err != nil {
			slog.Error("failed to reissue notification after opening details", "error", err, "request_id", reqID)
			return
		}

		h.mu.Lock()
		currentID, pending := h.notifications[reqID]
		if !pending || currentID != notificationID {
			h.mu.Unlock()
			_ = h.notifier.Close(newID)
			return
		}
		delete(h.requests, notificationID)
		h.notifications[reqID] = newID
		h.requests[newID] = reqID
		h.mu.Unlock()
	})
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

func (h *Handler) approvalActions() []string {
	// GNOME Shell displays at most three explicit actions. Keep Admin in the
	// visible set rather than displacing it with the timed approval shortcut.
	return []string{
		"approve", "Approve once",
		"deny", "Deny",
		"details", "Admin...",
	}
}

func detailsHint() string {
	return " • Select Admin... for full request information"
}

func (h *Handler) handleCreated(req *approval.Request) {
	if h.notificationDelay <= 0 {
		h.sendNotification(req)
		return
	}
	h.pending.Schedule(req.ID, h.notificationDelay, func() {
		h.sendNotification(req)
	})
}

func (h *Handler) sendNotification(req *approval.Request) {
	summary, icon := h.notificationMeta(req)
	body := h.formatBody(req)
	actions := h.approvalActions()

	id, err := h.notifier.Notify(summary, body+detailsHint(), icon, actions)
	if err != nil {
		slog.Error("failed to send notification", "error", err, "request_id", req.ID)
		return
	}

	h.mu.Lock()
	h.notifications[req.ID] = id
	h.requests[id] = req.ID
	h.requestData[req.ID] = req
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
		delete(h.requestData, req.ID)
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
	invoker := req.SenderInfo.InvokerName
	if invoker == "" {
		invoker = "client"
	}
	summary := fmt.Sprintf("%s timed out", invoker)
	body := fmt.Sprintf("Auto-approve similar requests for %s?", formatDurationShort(h.autoApproveDuration))
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
	if h.pending.Cancel(requestID) {
		return
	}

	h.mu.Lock()
	notifID, ok := h.notifications[requestID]
	if ok {
		delete(h.notifications, requestID)
		delete(h.requests, notifID)
		delete(h.requestData, requestID)
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

// markupEscaper escapes the only characters that are special in the
// org.freedesktop.Notifications markup body: & < >. Quotes are deliberately
// left alone — body text never appears inside an attribute value, and
// html.EscapeString's &#39;/&#34; showed up literally on servers (dunst, mako,
// GNOME Shell) that don't decode numeric character references.
var markupEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

func escapeMarkup(s string) string {
	return markupEscaper.Replace(s)
}

// commitSubject returns the first line of a commit message.
func commitSubject(msg string) string {
	if before, _, ok := strings.Cut(msg, "\n"); ok {
		return before
	}
	return msg
}

func notificationApplication(req *approval.Request) string {
	if len(req.SenderInfo.ProcessChain) > 0 && req.SenderInfo.ProcessChain[0].Name != "" {
		return req.SenderInfo.ProcessChain[0].Name
	}
	if req.SenderInfo.InvokerName != "" {
		return strings.TrimSuffix(req.SenderInfo.InvokerName, ".service")
	}
	if req.Client != "" {
		return req.Client
	}
	return "Unknown application"
}

func requestActionLabel(t approval.RequestType) string {
	switch t {
	case approval.RequestTypeSearch:
		return "Search secrets"
	case approval.RequestTypeDelete:
		return "Delete secret"
	case approval.RequestTypeWrite:
		return "Write secret"
	case approval.RequestTypeSSHSign:
		return "Use SSH key"
	case approval.RequestTypeUnlock:
		return "Unlock collection"
	default:
		return "Read secret"
	}
}

func requestSecretLabel(req *approval.Request) string {
	if len(req.Items) == 1 {
		item := req.Items[0]
		if item.Label != "" && !strings.HasPrefix(item.Label, "org.freedesktop.Secret.") {
			return item.Label
		}
		service := firstAttribute(item.Attributes, "service", "application", "app", "xdg:schema")
		account := firstAttribute(item.Attributes, "account", "username", "user")
		purpose := ""
		if strings.HasSuffix(account, "_accessTokenKey") {
			account = strings.TrimSuffix(account, "_accessTokenKey")
			purpose = "access token"
		}
		accountRunes := []rune(account)
		if len(accountRunes) > 8 {
			account = string(accountRunes[:8]) + "…"
		}

		description := "Generic secret"
		switch {
		case service != "" && purpose != "":
			description = service + " — " + purpose
		case service != "":
			description = service
		case purpose != "":
			description = purpose
		}
		if account != "" {
			description += " (account " + account + ")"
		}
		return description
	}
	if len(req.Items) > 1 {
		return fmt.Sprintf("%d secrets", len(req.Items))
	}
	if len(req.SearchAttributes) > 0 {
		keys := make([]string, 0, len(req.SearchAttributes))
		for key := range req.SearchAttributes {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		attrs := make([]string, 0, len(keys))
		for _, key := range keys {
			attrs = append(attrs, key+"="+req.SearchAttributes[key])
		}
		return strings.Join(attrs, ", ")
	}
	return "Unspecified"
}

func firstAttribute(attrs map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(attrs[key]); value != "" {
			return value
		}
	}
	return ""
}

const maxNotificationProcesses = 2

func (h *Handler) formatProcessChain(b *strings.Builder, chain []approval.ProcessInfo) {
	writeProcess := func(p approval.ProcessInfo) {
		b.WriteString(escapeMarkup(p.Name))
		if h.showPIDs {
			fmt.Fprintf(b, "[%d]", p.PID)
		}
	}

	if len(chain) <= maxNotificationProcesses+1 {
		for i, process := range chain {
			if i > 0 {
				b.WriteString(" ← ")
			}
			writeProcess(process)
		}
		return
	}

	for i, process := range chain[:maxNotificationProcesses] {
		if i > 0 {
			b.WriteString(" ← ")
		}
		writeProcess(process)
	}
	fmt.Fprintf(b, " ← … (+%d) ← ", len(chain)-maxNotificationProcesses-1)
	writeProcess(chain[len(chain)-1])
}

func (h *Handler) formatBody(req *approval.Request) string {
	var b strings.Builder

	// esc escapes client-controlled substrings before they are interpolated into
	// the org.freedesktop.Notifications markup body. Every dynamic value here
	// (repo/commit/label/search attrs, and process comm which is itself
	// attacker-settable via prctl) must be escaped; only our own literal <b>/<i>
	// tags are emitted raw. The notification carries approve/deny action buttons,
	// so unescaped markup could forge reassuring content on the consent surface.
	esc := escapeMarkup

	switch req.Type {
	case approval.RequestTypeGPGSign:
		if req.GPGSignInfo != nil {
			fmt.Fprintf(&b, "<b>%s</b>: <i>%s</i>", esc(req.GPGSignInfo.RepoName), esc(commitSubject(req.GPGSignInfo.CommitMsg)))
			if len(req.SenderInfo.ProcessChain) > 0 {
				b.WriteString(" • <b>Process:</b> ")
				h.formatProcessChain(&b, req.SenderInfo.ProcessChain)
			}
		}
	case approval.RequestTypeSSHSign:
		// Show key label and destination
		if len(req.Items) > 0 {
			fmt.Fprintf(&b, "<b>%s</b>", esc(req.Items[0].Label))
			if dest, ok := req.Items[0].Attributes["destination"]; ok && dest != "" {
				fmt.Fprintf(&b, " → %s", esc(dest))
			}
		}
		if len(req.SenderInfo.ProcessChain) > 0 {
			b.WriteString(" • <b>Process:</b> ")
			h.formatProcessChain(&b, req.SenderInfo.ProcessChain)
		}
	default:
		fmt.Fprintf(&b, "<b>Application:</b> %s", esc(notificationApplication(req)))
		fmt.Fprintf(&b, " • <b>Request:</b> %s", esc(requestActionLabel(req.Type)))
		fmt.Fprintf(&b, " • <b>Secret:</b> %s", esc(requestSecretLabel(req)))
		if len(req.SenderInfo.ProcessChain) > 0 {
			b.WriteString(" • <b>Process:</b> ")
			h.formatProcessChain(&b, req.SenderInfo.ProcessChain)
		} else if req.SenderInfo.InvokerName != "" {
			fmt.Fprintf(&b, " • <b>Process:</b> %s[%d]", esc(req.SenderInfo.InvokerName), req.SenderInfo.PID)
		} else if req.SenderInfo.PID != 0 {
			fmt.Fprintf(&b, " • <b>Process:</b> %s[%d]", esc(req.Client), req.SenderInfo.PID)
		}
	}

	return b.String()
}
