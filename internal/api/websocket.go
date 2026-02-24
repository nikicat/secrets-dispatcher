package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/proxy"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Send pings to peer with this period.
	pingPeriod = 30 * time.Second

	// Maximum message size allowed from peer.
	maxMessageSize = 512
)

// WSMessage represents a message sent over the WebSocket.
type WSMessage struct {
	Type    string `json:"type"`
	Version string `json:"version,omitempty"`

	// For snapshot - no omitempty to ensure arrays are always present in JSON
	Requests []PendingRequest   `json:"requests"`
	Clients  []proxy.ClientInfo `json:"clients"`
	History  []HistoryEntry     `json:"history"`

	// For request_created
	Request *PendingRequest `json:"request,omitempty"`

	// For request_resolved
	ID     string `json:"id,omitempty"`
	Result string `json:"result,omitempty"`
	// Signature carries base64-encoded signature bytes for gpg_sign request_resolved events.
	// Empty for all other event types.
	Signature string `json:"signature,omitempty"`
	// GPGStatus carries the raw [GNUPG:] status lines from gpg --status-fd=2.
	// Written to thin client's stderr so git can parse gpg status.
	GPGStatus string `json:"gpg_status,omitempty"`
	// ExitCode carries gpg's exit code for failed signing attempts.
	// Non-zero only when real gpg exited with an error.
	ExitCode int `json:"exit_code,omitempty"`

	// For client_connected/client_disconnected
	Client *proxy.ClientInfo `json:"client,omitempty"`

	// For history_entry
	HistoryEntry *HistoryEntry `json:"history_entry,omitempty"`
}

// WSHandler handles WebSocket connections for real-time updates.
type WSHandler struct {
	manager        *approval.Manager
	clientProvider ClientProvider
	auth           *Auth
	remoteSocket   string
	clientName     string

	// Active connections
	connsMu sync.RWMutex
	conns   map[*wsConnection]struct{}
}

// NewWSHandler creates a new WebSocket handler.
func NewWSHandler(manager *approval.Manager, clientProvider ClientProvider, auth *Auth, remoteSocket, clientName string) *WSHandler {
	return &WSHandler{
		manager:        manager,
		clientProvider: clientProvider,
		auth:           auth,
		remoteSocket:   remoteSocket,
		clientName:     clientName,
		conns:          make(map[*wsConnection]struct{}),
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

// HandleWS handles WebSocket upgrade requests.
func (h *WSHandler) HandleWS(w http.ResponseWriter, r *http.Request) {
	// Validate session cookie or Bearer token before accepting upgrade.
	if !h.auth.ValidateRequest(r) {
		http.Error(w, `{"error": "unauthorized"}`, http.StatusUnauthorized)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Allow same-origin connections only
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		slog.Error("WebSocket accept failed", "error", err)
		return
	}

	conn.SetReadLimit(maxMessageSize)

	// Use background context - the WebSocket connection lives beyond the HTTP request
	ctx, cancel := context.WithCancel(context.Background())
	wsc := &wsConnection{
		handler: h,
		conn:    conn,
		send:    make(chan []byte, 256),
		ctx:     ctx,
		cancel:  cancel,
	}

	// Register connection
	h.connsMu.Lock()
	h.conns[wsc] = struct{}{}
	h.connsMu.Unlock()

	// Subscribe to manager events
	h.manager.Subscribe(wsc)

	// Send initial snapshot
	if err := wsc.sendSnapshot(); err != nil {
		slog.Error("Failed to send snapshot", "error", err)
		wsc.close()
		return
	}

	// Start reader and writer goroutines
	go wsc.writePump()
	go wsc.readPump()
}

// OnEvent implements approval.Observer.
func (wsc *wsConnection) OnEvent(event approval.Event) {
	var msgs []WSMessage

	switch event.Type {
	case approval.EventRequestCreated:
		msgs = append(msgs, WSMessage{
			Type:    "request_created",
			Request: convertRequest(event.Request),
		})
	case approval.EventRequestApproved:
		msg := WSMessage{
			Type:   "request_resolved",
			ID:     event.Request.ID,
			Result: "approved",
		}
		if event.Request.Type == approval.RequestTypeGPGSign {
			msg.Signature = base64.StdEncoding.EncodeToString(event.Request.Signature)
			msg.GPGStatus = string(event.Request.GPGStatus)
			if event.Request.GPGExitCode != 0 {
				msg.ExitCode = event.Request.GPGExitCode
			}
		}
		msgs = append(msgs, msg)
		entry := makeHistoryEntry(event.Request, "approved")
		msgs = append(msgs, WSMessage{
			Type:         "history_entry",
			HistoryEntry: &entry,
		})
	case approval.EventRequestDenied:
		msgs = append(msgs, WSMessage{
			Type:   "request_resolved",
			ID:     event.Request.ID,
			Result: "denied",
		})
		entry := makeHistoryEntry(event.Request, "denied")
		msgs = append(msgs, WSMessage{
			Type:         "history_entry",
			HistoryEntry: &entry,
		})
	case approval.EventRequestExpired:
		msgs = append(msgs, WSMessage{
			Type: "request_expired",
			ID:   event.Request.ID,
		})
		entry := makeHistoryEntry(event.Request, "expired")
		msgs = append(msgs, WSMessage{
			Type:         "history_entry",
			HistoryEntry: &entry,
		})
	case approval.EventRequestCancelled:
		msgs = append(msgs, WSMessage{
			Type: "request_cancelled",
			ID:   event.Request.ID,
		})
		entry := makeHistoryEntry(event.Request, "cancelled")
		msgs = append(msgs, WSMessage{
			Type:         "history_entry",
			HistoryEntry: &entry,
		})
	default:
		return
	}

	for _, msg := range msgs {
		data, err := json.Marshal(msg)
		if err != nil {
			slog.Error("Failed to marshal WebSocket message", "error", err)
			continue
		}

		// Non-blocking send - drop message if client is slow
		select {
		case wsc.send <- data:
		default:
			slog.Warn("WebSocket send buffer full, dropping message")
		}
	}
}

// makeHistoryEntry creates a HistoryEntry for WebSocket messages.
func makeHistoryEntry(req *approval.Request, resolution string) HistoryEntry {
	items := make([]ItemInfo, len(req.Items))
	for i, item := range req.Items {
		items[i] = ItemInfo{
			Path:       item.Path,
			Label:      item.Label,
			Attributes: item.Attributes,
		}
	}
	return HistoryEntry{
		Request: PendingRequest{
			ID:               req.ID,
			Client:           req.Client,
			Items:            items,
			Session:          req.Session,
			CreatedAt:        req.CreatedAt,
			ExpiresAt:        req.ExpiresAt,
			Type:             string(req.Type),
			SearchAttributes: req.SearchAttributes,
			SenderInfo: SenderInfo{
				Sender:   req.SenderInfo.Sender,
				PID:      req.SenderInfo.PID,
				UID:      req.SenderInfo.UID,
				UserName: req.SenderInfo.UserName,
				UnitName: req.SenderInfo.UnitName,
			},
			GPGSignInfo: req.GPGSignInfo,
		},
		Resolution: resolution,
		ResolvedAt: time.Now(),
	}
}

// sendSnapshot sends the current state to the client.
func (wsc *wsConnection) sendSnapshot() error {
	h := wsc.handler

	// Get pending requests
	pending := h.manager.List()
	requests := make([]PendingRequest, len(pending))
	for i, req := range pending {
		requests[i] = *convertRequest(req)
	}

	// Get clients
	var clients []proxy.ClientInfo
	if h.clientProvider != nil {
		clients = h.clientProvider.Clients()
	} else {
		clients = []proxy.ClientInfo{
			{Name: h.clientName, SocketPath: h.remoteSocket},
		}
	}

	// Get history
	historyEntries := h.manager.History()
	history := make([]HistoryEntry, len(historyEntries))
	for i, entry := range historyEntries {
		history[i] = convertHistoryEntry(entry)
	}

	msg := WSMessage{
		Type:     "snapshot",
		Version:  BuildVersion,
		Requests: requests,
		Clients:  clients,
		History:  history,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}

	// Send directly (not through channel) for initial snapshot
	ctx, cancel := context.WithTimeout(wsc.ctx, writeWait)
	defer cancel()
	return wsc.conn.Write(ctx, websocket.MessageText, data)
}

// writePump pumps messages from the send channel to the WebSocket connection.
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

		case message, ok := <-wsc.send:
			if !ok {
				// Channel closed
				return
			}
			ctx, cancel := context.WithTimeout(wsc.ctx, writeWait)
			err := wsc.conn.Write(ctx, websocket.MessageText, message)
			cancel()
			if err != nil {
				slog.Debug("WebSocket write failed", "error", err)
				return
			}

		case <-ticker.C:
			// Send ping
			ctx, cancel := context.WithTimeout(wsc.ctx, writeWait)
			err := wsc.conn.Ping(ctx)
			cancel()
			if err != nil {
				slog.Debug("WebSocket ping failed", "error", err)
				return
			}
		}
	}
}

// readPump reads messages from the WebSocket connection.
// We don't expect any messages from the client, this is just for close detection.
func (wsc *wsConnection) readPump() {
	defer wsc.close()

	for {
		_, _, err := wsc.conn.Read(wsc.ctx)
		if err != nil {
			// Connection closed
			return
		}
		// Ignore any messages from client
	}
}

// close cleans up the connection.
func (wsc *wsConnection) close() {
	wsc.cancel()

	// Unsubscribe from manager
	wsc.handler.manager.Unsubscribe(wsc)

	// Unregister connection
	wsc.handler.connsMu.Lock()
	delete(wsc.handler.conns, wsc)
	wsc.handler.connsMu.Unlock()

	// Close the WebSocket
	wsc.conn.Close(websocket.StatusNormalClosure, "")
}

// convertRequest converts an approval.Request to an API PendingRequest.
func convertRequest(req *approval.Request) *PendingRequest {
	items := make([]ItemInfo, len(req.Items))
	for i, item := range req.Items {
		items[i] = ItemInfo{
			Path:       item.Path,
			Label:      item.Label,
			Attributes: item.Attributes,
		}
	}
	return &PendingRequest{
		ID:               req.ID,
		Client:           req.Client,
		Items:            items,
		Session:          req.Session,
		CreatedAt:        req.CreatedAt,
		ExpiresAt:        req.ExpiresAt,
		Type:             string(req.Type),
		SearchAttributes: req.SearchAttributes,
		SenderInfo: SenderInfo{
			Sender:   req.SenderInfo.Sender,
			PID:      req.SenderInfo.PID,
			UID:      req.SenderInfo.UID,
			UserName: req.SenderInfo.UserName,
			UnitName: req.SenderInfo.UnitName,
		},
		GPGSignInfo: req.GPGSignInfo,
	}
}

// BroadcastClientConnected sends a client_connected message to all connections.
func (h *WSHandler) BroadcastClientConnected(client proxy.ClientInfo) {
	h.broadcast(WSMessage{
		Type:   "client_connected",
		Client: &client,
	})
}

// BroadcastClientDisconnected sends a client_disconnected message to all connections.
func (h *WSHandler) BroadcastClientDisconnected(client proxy.ClientInfo) {
	h.broadcast(WSMessage{
		Type:   "client_disconnected",
		Client: &client,
	})
}

// OnClientConnected implements proxy.ClientObserver.
func (h *WSHandler) OnClientConnected(client proxy.ClientInfo) {
	h.BroadcastClientConnected(client)
}

// OnClientDisconnected implements proxy.ClientObserver.
func (h *WSHandler) OnClientDisconnected(client proxy.ClientInfo) {
	h.BroadcastClientDisconnected(client)
}

// broadcast sends a message to all connected clients.
func (h *WSHandler) broadcast(msg WSMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		slog.Error("Failed to marshal broadcast message", "error", err)
		return
	}

	h.connsMu.RLock()
	defer h.connsMu.RUnlock()

	for wsc := range h.conns {
		select {
		case wsc.send <- data:
		default:
			// Drop if buffer full
		}
	}
}
