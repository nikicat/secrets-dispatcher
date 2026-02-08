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

// ItemInfo contains metadata about a secret item.
type ItemInfo struct {
	Path       string            `json:"path"`
	Label      string            `json:"label"`
	Attributes map[string]string `json:"attributes"`
}

// SenderInfo contains information about the D-Bus sender process.
type SenderInfo struct {
	Sender   string `json:"sender"`    // D-Bus unique name (":1.123")
	PID      uint32 `json:"pid"`       // Process ID
	UID      uint32 `json:"uid"`       // User ID
	UserName string `json:"user_name"` // Username (may be empty if lookup fails)
	UnitName string `json:"unit_name"` // Systemd unit (may be empty)
}

// PendingRequest represents a pending approval request in API responses.
type PendingRequest struct {
	ID               string            `json:"id"`
	Client           string            `json:"client"`
	Items            []ItemInfo        `json:"items"`
	Session          string            `json:"session"`
	CreatedAt        time.Time         `json:"created_at"`
	ExpiresAt        time.Time         `json:"expires_at"`
	Type             string            `json:"type"`
	SearchAttributes map[string]string `json:"search_attributes,omitempty"`
	SenderInfo       SenderInfo        `json:"sender_info"`
}

// ActionResponse is returned by approve/deny endpoints.
type ActionResponse struct {
	Status string `json:"status"`
}

// ErrorResponse is returned on errors.
type ErrorResponse struct {
	Error string `json:"error"`
}

// LogEntry represents an audit log entry (deprecated, use HistoryEntry).
type LogEntry struct {
	Time    time.Time `json:"time"`
	Client  string    `json:"client"`
	Method  string    `json:"method"`
	Items   []string  `json:"items,omitempty"`
	Result  string    `json:"result"`
	Error   string    `json:"error,omitempty"`
}

// LogResponse is returned by GET /api/v1/log (deprecated, use HistoryResponse).
type LogResponse struct {
	Entries []LogEntry `json:"entries"`
}

// HistoryEntry represents a resolved approval request in API responses.
type HistoryEntry struct {
	Request    PendingRequest `json:"request"`
	Resolution string         `json:"resolution"` // approved, denied, expired, cancelled
	ResolvedAt time.Time      `json:"resolved_at"`
}

// HistoryResponse is returned by GET /api/v1/log.
type HistoryResponse struct {
	Entries []HistoryEntry `json:"entries"`
}

// AuthRequest is the request body for POST /api/v1/auth.
type AuthRequest struct {
	Token string `json:"token"`
}
