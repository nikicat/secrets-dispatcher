package api

import (
	"time"

	"github.com/nikicat/secrets-dispatcher/internal/proxy"
)

// StatusResponse is returned by GET /api/v1/status.
type StatusResponse struct {
	Running      bool               `json:"running"`
	Clients      []proxy.ClientInfo `json:"clients"`
	PendingCount int                `json:"pending_count"`
	// Deprecated: use Clients instead. Kept for backward compatibility.
	Client string `json:"client,omitempty"`
	// Deprecated: use Clients instead. Kept for backward compatibility.
	RemoteSocket string `json:"remote_socket,omitempty"`
}

// PendingListResponse is returned by GET /api/v1/pending.
type PendingListResponse struct {
	Requests []PendingRequest `json:"requests"`
}

// PendingRequest represents a pending approval request in API responses.
type PendingRequest struct {
	ID        string    `json:"id"`
	Client    string    `json:"client"`
	Items     []string  `json:"items"`
	Session   string    `json:"session"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// ActionResponse is returned by approve/deny endpoints.
type ActionResponse struct {
	Status string `json:"status"`
}

// ErrorResponse is returned on errors.
type ErrorResponse struct {
	Error string `json:"error"`
}

// LogEntry represents an audit log entry.
type LogEntry struct {
	Time    time.Time `json:"time"`
	Client  string    `json:"client"`
	Method  string    `json:"method"`
	Items   []string  `json:"items,omitempty"`
	Result  string    `json:"result"`
	Error   string    `json:"error,omitempty"`
}

// LogResponse is returned by GET /api/v1/log.
type LogResponse struct {
	Entries []LogEntry `json:"entries"`
}

// AuthRequest is the request body for POST /api/v1/auth.
type AuthRequest struct {
	Token string `json:"token"`
}
