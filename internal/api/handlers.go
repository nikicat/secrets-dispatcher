package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

// Handlers provides HTTP handlers for the REST API.
type Handlers struct {
	manager      *approval.Manager
	remoteSocket string
}

// NewHandlers creates new API handlers.
func NewHandlers(manager *approval.Manager, remoteSocket string) *Handlers {
	return &Handlers{
		manager:      manager,
		remoteSocket: remoteSocket,
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
		Client:       h.manager.Client(),
		PendingCount: h.manager.PendingCount(),
		RemoteSocket: h.remoteSocket,
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
		requests[i] = PendingRequest{
			ID:        req.ID,
			Client:    req.Client,
			Items:     req.Items,
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
