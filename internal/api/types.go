package api

import (
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
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
	Path       string                    `json:"path"`
	Label      string                    `json:"label"`
	Attributes dbustypes.Attributes      `json:"attributes"`
	Collection *dbustypes.CollectionName `json:"collection,omitempty"`
}

// A request as the API sends it in a History entry. PendingRequest adds what
// only a pending request has.
type RequestRecord struct {
	ID               approval.RequestID    `json:"id"`
	Client           approval.ClientName   `json:"client"`
	Items            []ItemInfo            `json:"items"`
	Session          dbus.ObjectPath       `json:"session"`
	CreatedAt        time.Time             `json:"created_at"`
	Type             approval.RequestType  `json:"type"`
	SearchAttributes dbustypes.Attributes  `json:"search_attributes,omitempty"`
	SenderInfo       sender.Info           `json:"sender_info"`
	GPGSignInfo      *approval.GPGSignInfo `json:"gpg_sign_info,omitempty"`
	// The scope that an auto-approve rule made from the request gets, for a
	// request that approval.Request.AutoApprovable accepts.
	AutoApproveScope *approval.AutoApproveScope `json:"auto_approve_scope,omitempty"`
}

// Returns the approval payload for the record's type, or nil for a type that
// carries none.
func (r RequestRecord) payload() approval.Payload {
	switch r.Type {
	case approval.RequestTypeGPGSign:
		return r.GPGSignInfo
	case approval.RequestTypeSearch:
		return approval.SearchPayload{Attributes: r.SearchAttributes}
	}
	return nil
}

type PendingRequest struct {
	RequestRecord
	ExpiresAt time.Time `json:"expires_at"`
}

// A trust rule as the API sends it, with Client set to the glob the rule
// matches clients with, the default included.
type TrustRule struct {
	approval.TrustRule
	Client approval.Glob `json:"client"`
}

// The outcome that an endpoint acting on the daemon's state reports.
type ActionStatus string

const (
	ActionApproved      ActionStatus = "approved"
	ActionDenied        ActionStatus = "denied"
	ActionCancelled     ActionStatus = "cancelled"
	ActionCreated       ActionStatus = "created"
	ActionDeleted       ActionStatus = "deleted"
	ActionAuthenticated ActionStatus = "authenticated"
)

// The body that an endpoint acting on the daemon's state answers with.
type ActionResponse struct {
	Status ActionStatus `json:"status"`
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
	Request    RequestRecord       `json:"request"`
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
