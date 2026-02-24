package api

import (
	"encoding/json"
	"net/http"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

// GPGSignRequest is the POST body for /api/v1/gpg-sign/request.
type GPGSignRequest struct {
	Client      string               `json:"client"`
	GPGSignInfo *approval.GPGSignInfo `json:"gpg_sign_info"`
}

// GPGSignResponse is the response body for a successful POST to /api/v1/gpg-sign/request.
type GPGSignResponse struct {
	RequestID string `json:"request_id"`
}

// HandleGPGSignRequest handles POST /api/v1/gpg-sign/request.
// It creates a non-blocking gpg_sign approval request and returns the request ID.
// The result (signature or denial) is delivered via the WebSocket request_resolved event.
func (h *Handlers) HandleGPGSignRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req GPGSignRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.GPGSignInfo == nil {
		writeError(w, "gpg_sign_info is required", http.StatusBadRequest)
		return
	}
	if req.Client == "" {
		req.Client = "unknown"
	}
	id, err := h.manager.CreateGPGSignRequest(req.Client, req.GPGSignInfo)
	if err != nil {
		writeError(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, GPGSignResponse{RequestID: id})
}
