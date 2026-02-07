package api

import (
	"encoding/json"
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
}

// NewHandlers creates new API handlers for single-socket mode.
func NewHandlers(manager *approval.Manager, remoteSocket, clientName string, auth *Auth) *Handlers {
	return &Handlers{
		manager:      manager,
		remoteSocket: remoteSocket,
		clientName:   clientName,
		auth:         auth,
	}
}

// NewHandlersWithProvider creates new API handlers for multi-socket mode.
func NewHandlersWithProvider(manager *approval.Manager, provider ClientProvider, auth *Auth) *Handlers {
	return &Handlers{
		manager:        manager,
		clientProvider: provider,
		auth:           auth,
	}
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
			ID:        req.ID,
			Client:    req.Client,
			Items:     items,
			Session:   req.Session,
			CreatedAt: req.CreatedAt,
			ExpiresAt: req.ExpiresAt,
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
// Note: Log retrieval is not implemented in this phase - returns empty list.
func (h *Handlers) HandleLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Log retrieval would require storing logs in memory or reading from file.
	// For now, return empty list.
	resp := LogResponse{Entries: []LogEntry{}}
	writeJSON(w, resp)
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
