package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/gpgsign"
	"github.com/nikicat/secrets-dispatcher/internal/proxy"
	"github.com/nikicat/secrets-dispatcher/internal/wsmsg"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// How often to ping the peer.
	pingPeriod = 30 * time.Second

	// Maximum message size allowed from peer.
	maxMessageSize = 512
)

// A message the daemon's WebSocket sends. It encodes as one flat JSON object,
// "type" and then the fields of its payload. The type is the payload's, so a
// message holds only the fields of its own type.
type WSMessage struct {
	payload wsPayload
}

// The fields of one type of WebSocket message.
type wsPayload interface {
	wsType() wsmsg.Type
}

func (m WSMessage) Type() wsmsg.Type { return m.payload.wsType() }

func (m WSMessage) MarshalJSON() ([]byte, error) {
	header, err := json.Marshal(wsmsg.Header{Type: m.Type()})
	if err != nil {
		return nil, err
	}
	fields, err := json.Marshal(m.payload)
	if err != nil {
		return nil, err
	}
	if len(fields) == len("{}") {
		return header, nil
	}
	out := append(header[:len(header)-1], ',')
	return append(out, fields[1:]...), nil
}

// The state a WebSocket peer starts from. The lists have no omitempty, so they
// are always present in the JSON.
type snapshotMsg struct {
	Version                    string                     `json:"version,omitempty"`
	Requests                   []PendingRequest           `json:"requests"`
	Clients                    []proxy.ClientInfo         `json:"clients"`
	History                    []HistoryEntry             `json:"history"`
	AutoApproveRules           []approval.AutoApproveRule `json:"auto_approve_rules"`
	TrustedSigners             []approval.TrustedSigner   `json:"trusted_signers"`
	TrustRules                 []TrustRule                `json:"trust_rules"`
	AutoApproveDurationSeconds int                        `json:"auto_approve_duration_seconds,omitempty"`
	NotificationDelayMS        int                        `json:"notification_delay_ms,omitempty"`
}

type requestCreatedMsg struct {
	Request PendingRequest `json:"request"`
}

type requestResolvedMsg struct{ gpgsign.RequestEnd }

type requestExpiredMsg struct{ gpgsign.RequestEnd }

type requestCancelledMsg struct{ gpgsign.RequestEnd }

type historyEntryMsg struct {
	HistoryEntry HistoryEntry `json:"history_entry"`
}

type autoApproveRuleAddedMsg struct {
	AutoApproveRule approval.AutoApproveRule `json:"auto_approve_rule"`
}

type autoApproveRuleRemovedMsg struct {
	ID approval.AutoApproveRuleID `json:"id"`
}

type clientConnectedMsg struct {
	Client proxy.ClientInfo `json:"client"`
}

type clientDisconnectedMsg struct {
	Client proxy.ClientInfo `json:"client"`
}

func (snapshotMsg) wsType() wsmsg.Type               { return wsmsg.Snapshot }
func (requestCreatedMsg) wsType() wsmsg.Type         { return wsmsg.RequestCreated }
func (requestResolvedMsg) wsType() wsmsg.Type        { return wsmsg.RequestResolved }
func (requestExpiredMsg) wsType() wsmsg.Type         { return wsmsg.RequestExpired }
func (requestCancelledMsg) wsType() wsmsg.Type       { return wsmsg.RequestCancelled }
func (historyEntryMsg) wsType() wsmsg.Type           { return wsmsg.HistoryEntry }
func (autoApproveRuleAddedMsg) wsType() wsmsg.Type   { return wsmsg.AutoApproveRuleAdded }
func (autoApproveRuleRemovedMsg) wsType() wsmsg.Type { return wsmsg.AutoApproveRuleRemoved }
func (clientConnectedMsg) wsType() wsmsg.Type        { return wsmsg.ClientConnected }
func (clientDisconnectedMsg) wsType() wsmsg.Type     { return wsmsg.ClientDisconnected }

// WSHandler handles WebSocket connections for real-time updates.
type WSHandler struct {
	manager           *approval.Manager
	clientProvider    ClientProvider
	auth              *Auth
	notificationDelay time.Duration

	// Active connections
	connsMu sync.RWMutex
	conns   map[*wsConnection]struct{}
}

func NewWSHandler(manager *approval.Manager, clientProvider ClientProvider, auth *Auth, notificationDelay time.Duration) *WSHandler {
	return &WSHandler{
		manager:           manager,
		clientProvider:    clientProvider,
		auth:              auth,
		notificationDelay: notificationDelay,
		conns:             make(map[*wsConnection]struct{}),
	}
}

// wsConnection represents a single WebSocket connection.
type wsConnection struct {
	handler *WSHandler
	conn    *websocket.Conn
	send    chan []byte
	ctx     context.Context
	cancel  context.CancelFunc
}

var _ approval.Observer = (*wsConnection)(nil)

func (h *WSHandler) HandleWS(w http.ResponseWriter, r *http.Request) {
	if !h.auth.ValidateRequest(r) {
		writeError(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// websocket.Accept's default Origin check keeps out pages on other
	// localhost ports (ADR 0006).
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		slog.Error("WebSocket accept failed", "error", err)
		return
	}

	conn.SetReadLimit(maxMessageSize)

	// The connection outlives the HTTP request, so it gets its own context.
	ctx, cancel := context.WithCancel(context.Background())
	wsc := &wsConnection{
		handler: h,
		conn:    conn,
		send:    make(chan []byte, 256),
		ctx:     ctx,
		cancel:  cancel,
	}

	h.attach(wsc)

	if err := wsc.sendSnapshot(); err != nil {
		slog.Error("Failed to send snapshot", "error", err)
		wsc.close()
		return
	}

	go wsc.writePump()
	go wsc.readPump()
}

// Registers the connection for broadcasts and approval events.
func (h *WSHandler) attach(wsc *wsConnection) {
	h.connsMu.Lock()
	h.conns[wsc] = struct{}{}
	h.connsMu.Unlock()

	h.manager.Subscribe(wsc)
}

// Undoes WSHandler.attach.
func (h *WSHandler) detach(wsc *wsConnection) {
	h.manager.Unsubscribe(wsc)

	h.connsMu.Lock()
	delete(h.conns, wsc)
	h.connsMu.Unlock()
}

// Returns the messages that tell the WebSocket peer a request ended, in
// sending order. The last one carries the request's History entry.
func endedMessages(entry approval.HistoryEntry) []WSMessage {
	var msgs []WSMessage
	end := gpgsign.RequestEndOf(entry)
	switch entry.Resolution {
	case approval.ResolutionApproved, approval.ResolutionDenied:
		msgs = append(msgs, WSMessage{requestResolvedMsg{end}})
	case approval.ResolutionExpired:
		msgs = append(msgs, WSMessage{requestExpiredMsg{end}})
	case approval.ResolutionCancelled:
		msgs = append(msgs, WSMessage{requestCancelledMsg{end}})
	case approval.ResolutionAutoApproved:
		// A gpg_sign auto-approval carries the signature that the thin client
		// waits for.
		if entry.GPGOutput() != nil {
			msgs = append(msgs, WSMessage{requestResolvedMsg{end}})
		}
	}
	return append(msgs, WSMessage{historyEntryMsg{convertHistoryEntry(entry)}})
}

func (wsc *wsConnection) RequestCreated(p approval.Pending) {
	wsc.sendAll(WSMessage{requestCreatedMsg{convertPending(p)}})
}

func (wsc *wsConnection) RequestEnded(entry approval.HistoryEntry) {
	wsc.sendAll(endedMessages(entry)...)
}

func (wsc *wsConnection) RuleAdded(rule approval.AutoApproveRule) {
	wsc.sendAll(WSMessage{autoApproveRuleAddedMsg{rule}})
}

func (wsc *wsConnection) RuleRemoved(rule approval.AutoApproveRule) {
	wsc.sendAll(WSMessage{autoApproveRuleRemovedMsg{ID: rule.ID}})
}

// Queues the messages in order, as wsConnection.queue does.
func (wsc *wsConnection) sendAll(msgs ...WSMessage) {
	for _, msg := range msgs {
		data, err := json.Marshal(msg)
		if err != nil {
			slog.Error("Failed to marshal WebSocket message", "error", err)
			continue
		}
		wsc.queue(data, msg.Type())
	}
}

// Queues an encoded message of the given type, or drops it with a warning when
// the peer is too slow to take it.
func (wsc *wsConnection) queue(data []byte, typ wsmsg.Type) {
	select {
	case wsc.send <- data:
		slog.Debug("ws send", "type", typ)
	default:
		slog.Warn("WebSocket send buffer full, dropping message", "type", typ)
	}
}

// Sends the current state to the WebSocket peer.
func (wsc *wsConnection) sendSnapshot() error {
	h := wsc.handler

	requests := pendingRequests(h.manager)

	clients := h.clientProvider.Clients()

	history := historyEntries(h.manager)

	autoApproveRules := h.manager.ListAutoApproveRules()
	if autoApproveRules == nil {
		autoApproveRules = []approval.AutoApproveRule{}
	}

	trustedSigners := h.manager.ListTrustedSigners()
	if trustedSigners == nil {
		trustedSigners = []approval.TrustedSigner{}
	}

	msg := WSMessage{snapshotMsg{
		Version:                    BuildVersion,
		Requests:                   requests,
		Clients:                    clients,
		History:                    history,
		AutoApproveRules:           autoApproveRules,
		TrustedSigners:             trustedSigners,
		TrustRules:                 trustRules(h.manager),
		AutoApproveDurationSeconds: int(h.manager.AutoApproveDuration().Seconds()),
		NotificationDelayMS:        int(h.notificationDelay.Milliseconds()),
	}}

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	// Written before writePump starts, so it bypasses the send channel and
	// always arrives first.
	slog.Debug("ws send snapshot", "requests", len(requests), "clients", len(clients), "history", len(history))
	return wsc.writeText(data, writeSnapshot)
}

// The kind of a write to the WebSocket peer, which the log of a failed write
// names.
type writeKind string

const (
	writeSnapshot writeKind = "snapshot"
	writeMessage  writeKind = "message"
	writePing     writeKind = "ping"
)

// Runs the given write to the WebSocket peer under the write deadline. Logs a
// failed write with the given kind, unless the connection was torn down.
func (wsc *wsConnection) write(op func(ctx context.Context) error, kind writeKind) error {
	ctx, cancel := context.WithTimeout(wsc.ctx, writeWait)
	defer cancel()
	err := op(ctx)
	if err != nil && !isConnClosedErr(err) {
		slog.Debug("WebSocket write failed", "op", kind, "error", err)
	}
	return err
}

// Writes the given encoded message to the WebSocket peer as one text frame.
func (wsc *wsConnection) writeText(data []byte, kind writeKind) error {
	return wsc.write(func(ctx context.Context) error {
		return wsc.conn.Write(ctx, websocket.MessageText, data)
	}, kind)
}

func (wsc *wsConnection) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		wsc.close()
	}()

	for {
		select {
		case <-wsc.ctx.Done():
			return

		case message := <-wsc.send:
			if wsc.writeText(message, writeMessage) != nil {
				return
			}

		case <-ticker.C:
			if wsc.write(wsc.conn.Ping, writePing) != nil {
				return
			}
		}
	}
}

// Reads only to notice that the WebSocket peer closed the connection. The peer
// sends no messages.
func (wsc *wsConnection) readPump() {
	defer wsc.close()

	for {
		if _, _, err := wsc.conn.Read(wsc.ctx); err != nil {
			return
		}
	}
}

func (wsc *wsConnection) close() {
	wsc.cancel()
	wsc.handler.detach(wsc)
	wsc.conn.Close(websocket.StatusNormalClosure, "")
}

func pendingRequests(m *approval.Manager) []PendingRequest {
	pending := m.Pending()
	requests := make([]PendingRequest, len(pending))
	for i, p := range pending {
		requests[i] = convertPending(p)
	}
	return requests
}

func historyEntries(m *approval.Manager) []HistoryEntry {
	history := m.History()
	entries := make([]HistoryEntry, len(history))
	for i, entry := range history {
		entries[i] = convertHistoryEntry(entry)
	}
	return entries
}

func trustRules(m *approval.Manager) []TrustRule {
	rules := m.ListTrustRules()
	out := make([]TrustRule, len(rules))
	for i, rule := range rules {
		out[i] = convertTrustRule(rule)
	}
	return out
}

// Checks whether err is expected teardown of the connection, which is not
// worth logging.
func isConnClosedErr(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, net.ErrClosed) {
		return true
	}
	if _, ok := errors.AsType[*net.OpError](err); ok {
		// The WebSocket peer broke the pipe or reset the connection.
		return true
	}
	return false
}

// BroadcastClientConnected sends a client_connected message to all connections.
func (h *WSHandler) BroadcastClientConnected(client proxy.ClientInfo) {
	h.broadcast(WSMessage{clientConnectedMsg{client}})
}

// BroadcastClientDisconnected sends a client_disconnected message to all connections.
func (h *WSHandler) BroadcastClientDisconnected(client proxy.ClientInfo) {
	h.broadcast(WSMessage{clientDisconnectedMsg{client}})
}

// OnClientConnected implements proxy.ClientObserver.
func (h *WSHandler) OnClientConnected(client proxy.ClientInfo) {
	h.BroadcastClientConnected(client)
}

// OnClientDisconnected implements proxy.ClientObserver.
func (h *WSHandler) OnClientDisconnected(client proxy.ClientInfo) {
	h.BroadcastClientDisconnected(client)
}

// Queues the given message on every connection, as wsConnection.queue does.
func (h *WSHandler) broadcast(msg WSMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		slog.Error("Failed to marshal broadcast message", "error", err)
		return
	}

	h.connsMu.RLock()
	defer h.connsMu.RUnlock()

	for wsc := range h.conns {
		wsc.queue(data, msg.Type())
	}
}
