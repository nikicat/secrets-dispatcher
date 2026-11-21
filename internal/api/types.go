package api

import (
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/proxy"
	"github.com/nikicat/secrets-dispatcher/internal/sender"
)

// StatusResponse is returned by GET /api/v1/status.
type StatusResponse struct {
	Running      bool               `json:"running"`
	Clients      []proxy.ClientInfo `json:"clients"`
	PendingCount int                `json:"pending_count"`
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

// PendingRequest represents a pending approval request in API responses.
type PendingRequest struct {
	ID               string                `json:"id"`
	Client           approval.ClientName   `json:"client"`
	Items            []ItemInfo            `json:"items"`
	Session          dbus.ObjectPath       `json:"session"`
	CreatedAt        time.Time             `json:"created_at"`
	ExpiresAt        time.Time             `json:"expires_at"`
	Type             approval.RequestType  `json:"type"`
	SearchAttributes map[string]string     `json:"search_attributes,omitempty"`
	SenderInfo       sender.Info           `json:"sender_info"`
	GPGSignInfo      *approval.GPGSignInfo `json:"gpg_sign_info,omitempty"`
	// True if approval.Request.AutoApprovable accepts the request. False
	// otherwise.
	AutoApprovable bool `json:"auto_approvable"`
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
	Time   time.Time `json:"time"`
	Client string    `json:"client"`
	Method string    `json:"method"`
	Items  []string  `json:"items,omitempty"`
	Result string    `json:"result"`
	Error  string    `json:"error,omitempty"`
}

// LogResponse is returned by GET /api/v1/log (deprecated, use HistoryResponse).
type LogResponse struct {
	Entries []LogEntry `json:"entries"`
}

// HistoryEntry represents a resolved approval request in API responses.
type HistoryEntry struct {
	Request    PendingRequest      `json:"request"`
	Resolution approval.Resolution `json:"resolution"`
	ResolvedAt time.Time           `json:"resolved_at"`
}

// HistoryResponse is returned by GET /api/v1/log.
type HistoryResponse struct {
	Entries []HistoryEntry `json:"entries"`
}

// AuthRequest is the request body for POST /api/v1/auth.
type AuthRequest struct {
	Token string `json:"token"`
}
