package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/proxy"
)

// ClientProvider is an interface for getting connected client information.
type ClientProvider interface {
	Clients() []proxy.ClientInfo
}

// Handlers provides HTTP handlers for the REST API.
type Handlers struct {
	manager        *approval.Manager
	clientProvider ClientProvider
	// For backward compatibility in single-socket mode
	remoteSocket string
	clientName   string
	auth         *Auth
	testMode     bool      // When true, enables test-only endpoints
	gpgRunner    GPGRunner // Executes the real gpg binary; injectable for tests
}

// NewHandlers creates new API handlers for single-socket mode.
func NewHandlers(manager *approval.Manager, remoteSocket, clientName string, auth *Auth) *Handlers {
	return &Handlers{
		manager:      manager,
		remoteSocket: remoteSocket,
		clientName:   clientName,
		auth:         auth,
		gpgRunner:    &defaultGPGRunner{},
	}
}

// NewHandlersWithProvider creates new API handlers for multi-socket mode.
func NewHandlersWithProvider(manager *approval.Manager, provider ClientProvider, auth *Auth) *Handlers {
	return &Handlers{
		manager:        manager,
		clientProvider: provider,
		auth:           auth,
		gpgRunner:      &defaultGPGRunner{},
	}
}

// SetTestMode enables test-only endpoints.
func (h *Handlers) SetTestMode(enabled bool) {
	h.testMode = enabled
}

// HandleStatus handles GET /api/v1/status.
func (h *Handlers) HandleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	resp := StatusResponse{
		Running:      true,
		PendingCount: h.manager.PendingCount(),
	}

	if h.clientProvider != nil {
		// Multi-socket mode: get clients from provider
		resp.Clients = h.clientProvider.Clients()
	} else {
		// Single-socket mode: use static client info
		resp.Clients = []proxy.ClientInfo{
			{Name: h.clientName, SocketPath: h.remoteSocket},
		}
		// Also set deprecated fields for backward compatibility
		resp.Client = h.clientName
		resp.RemoteSocket = h.remoteSocket
	}

	writeJSON(w, resp)
}

// HandlePendingList handles GET /api/v1/pending.
func (h *Handlers) HandlePendingList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	pending := h.manager.List()
	requests := make([]PendingRequest, len(pending))
	for i, req := range pending {
		// Convert approval.ItemInfo to api.ItemInfo
		items := make([]ItemInfo, len(req.Items))
		for j, item := range req.Items {
			items[j] = ItemInfo{
				Path:       item.Path,
				Label:      item.Label,
				Attributes: item.Attributes,
			}
		}
		requests[i] = PendingRequest{
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

	resp := PendingListResponse{Requests: requests}
	writeJSON(w, resp)
}

// HandleApprove handles POST /api/v1/pending/{id}/approve.
func (h *Handlers) HandleApprove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := extractRequestID(r.URL.Path, "/api/v1/pending/", "/approve")
	if id == "" {
		writeError(w, "invalid request path", http.StatusBadRequest)
		return
	}

	// Check if this is a gpg_sign request that needs real GPG invocation.
	req := h.manager.GetPending(id)
	if req == nil {
		writeError(w, "request not found or expired", http.StatusNotFound)
		return
	}

	if req.Type == approval.RequestTypeGPGSign && req.GPGSignInfo != nil {
		// Find real gpg binary.
		gpgPath, err := h.gpgRunner.FindGPG()
		if err != nil {
			slog.Error("failed to find real gpg", "error", err)
			// Approve with failure — thin client will see ExitCode != 0.
			if apErr := h.manager.ApproveGPGFailed(id, nil, 2); apErr != nil {
				writeError(w, "request not found or expired", http.StatusNotFound)
				return
			}
			writeJSON(w, ActionResponse{Status: "approved"})
			return
		}

		// Run real gpg with the stored commit object.
		sig, status, exitCode, err := h.gpgRunner.RunGPG(gpgPath, req.GPGSignInfo.KeyID, []byte(req.GPGSignInfo.CommitObject))
		if err != nil || exitCode != 0 {
			slog.Error("gpg signing failed", "error", err, "exit_code", exitCode)
			if apErr := h.manager.ApproveGPGFailed(id, status, exitCode); apErr != nil {
				writeError(w, "request not found or expired", http.StatusNotFound)
				return
			}
			writeJSON(w, ActionResponse{Status: "approved"})
			return
		}

		// Success — store signature and status, then approve.
		if err := h.manager.ApproveWithSignature(id, sig, status); err != nil {
			writeError(w, "request not found or expired", http.StatusNotFound)
			return
		}
		writeJSON(w, ActionResponse{Status: "approved"})
		return
	}

	// Non-gpg_sign requests: existing flow.
	if err := h.manager.Approve(id); err != nil {
		if err == approval.ErrNotFound {
			writeError(w, "request not found or expired", http.StatusNotFound)
			return
		}
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, ActionResponse{Status: "approved"})
}

// HandleDeny handles POST /api/v1/pending/{id}/deny.
func (h *Handlers) HandleDeny(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	id := extractRequestID(r.URL.Path, "/api/v1/pending/", "/deny")
	if id == "" {
		writeError(w, "invalid request path", http.StatusBadRequest)
		return
	}

	if err := h.manager.Deny(id); err != nil {
		if err == approval.ErrNotFound {
			writeError(w, "request not found or expired", http.StatusNotFound)
			return
		}
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, ActionResponse{Status: "denied"})
}

// HandleLog handles GET /api/v1/log.
func (h *Handlers) HandleLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	history := h.manager.History()
	entries := make([]HistoryEntry, len(history))
	for i, entry := range history {
		entries[i] = convertHistoryEntry(entry)
	}

	resp := HistoryResponse{Entries: entries}
	writeJSON(w, resp)
}

// convertHistoryEntry converts an approval.HistoryEntry to an API HistoryEntry.
func convertHistoryEntry(entry approval.HistoryEntry) HistoryEntry {
	items := make([]ItemInfo, len(entry.Request.Items))
	for i, item := range entry.Request.Items {
		items[i] = ItemInfo{
			Path:       item.Path,
			Label:      item.Label,
			Attributes: item.Attributes,
		}
	}
	return HistoryEntry{
		Request: PendingRequest{
			ID:               entry.Request.ID,
			Client:           entry.Request.Client,
			Items:            items,
			Session:          entry.Request.Session,
			CreatedAt:        entry.Request.CreatedAt,
			ExpiresAt:        entry.Request.ExpiresAt,
			Type:             string(entry.Request.Type),
			SearchAttributes: entry.Request.SearchAttributes,
			SenderInfo: SenderInfo{
				Sender:   entry.Request.SenderInfo.Sender,
				PID:      entry.Request.SenderInfo.PID,
				UID:      entry.Request.SenderInfo.UID,
				UserName: entry.Request.SenderInfo.UserName,
				UnitName: entry.Request.SenderInfo.UnitName,
			},
			GPGSignInfo: entry.Request.GPGSignInfo,
		},
		Resolution: string(entry.Resolution),
		ResolvedAt: entry.ResolvedAt,
	}
}

// extractRequestID extracts the request ID from a path like /api/v1/pending/{id}/action.
func extractRequestID(path, prefix, suffix string) string {
	if !strings.HasPrefix(path, prefix) || !strings.HasSuffix(path, suffix) {
		return ""
	}
	id := path[len(prefix) : len(path)-len(suffix)]
	// Basic validation - should be non-empty and not contain slashes
	if id == "" || strings.Contains(id, "/") {
		return ""
	}
	return id
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, `{"error": "failed to encode response"}`, http.StatusInternalServerError)
	}
}

func writeError(w http.ResponseWriter, message string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(ErrorResponse{Error: message})
}

// HandleAuth handles POST /api/v1/auth for JWT to session cookie exchange.
func (h *Handlers) HandleAuth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req AuthRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Token == "" {
		writeError(w, "missing token", http.StatusBadRequest)
		return
	}

	// Validate the JWT
	_, err := h.auth.ValidateJWT(req.Token)
	if err != nil {
		writeError(w, "invalid or expired token", http.StatusUnauthorized)
		return
	}

	// Set the session cookie
	h.auth.SetSessionCookie(w)

	writeJSON(w, ActionResponse{Status: "authenticated"})
}

// HandleTestInjectHistory handles POST /api/v1/test/history for injecting test history entries.
// Only available when test mode is enabled.
func (h *Handlers) HandleTestInjectHistory(w http.ResponseWriter, r *http.Request) {
	if !h.testMode {
		writeError(w, "not found", http.StatusNotFound)
		return
	}

	if r.Method != http.MethodPost {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var entry HistoryEntry
	if err := json.NewDecoder(r.Body).Decode(&entry); err != nil {
		writeError(w, "invalid request body", http.StatusBadRequest)
		return
	}

	// Convert API HistoryEntry to approval.HistoryEntry
	items := make([]approval.ItemInfo, len(entry.Request.Items))
	for i, item := range entry.Request.Items {
		items[i] = approval.ItemInfo{
			Path:       item.Path,
			Label:      item.Label,
			Attributes: item.Attributes,
		}
	}

	approvalEntry := approval.HistoryEntry{
		Request: &approval.Request{
			ID:               entry.Request.ID,
			Client:           entry.Request.Client,
			Items:            items,
			Session:          entry.Request.Session,
			CreatedAt:        entry.Request.CreatedAt,
			ExpiresAt:        entry.Request.ExpiresAt,
			Type:             approval.RequestType(entry.Request.Type),
			SearchAttributes: entry.Request.SearchAttributes,
			SenderInfo: approval.SenderInfo{
				Sender:   entry.Request.SenderInfo.Sender,
				PID:      entry.Request.SenderInfo.PID,
				UID:      entry.Request.SenderInfo.UID,
				UserName: entry.Request.SenderInfo.UserName,
				UnitName: entry.Request.SenderInfo.UnitName,
			},
		},
		Resolution: approval.Resolution(entry.Resolution),
		ResolvedAt: entry.ResolvedAt,
	}

	h.manager.AddHistoryEntry(approvalEntry)

	writeJSON(w, ActionResponse{Status: "created"})
}
