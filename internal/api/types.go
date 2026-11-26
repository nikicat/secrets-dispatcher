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

// Returns the item as the API sends it.
func convertItem(item approval.ItemInfo) ItemInfo {
	return ItemInfo{Path: item.Path, Label: item.Label, Attributes: item.Attributes, Collection: item.CollectionName()}
}

// Returns the item that convertItem was given. Collection is derived from Path,
// so it is dropped.
func (i ItemInfo) item() approval.ItemInfo {
	return approval.ItemInfo{Path: i.Path, Label: i.Label, Attributes: i.Attributes}
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

// Returns the request as the API sends it.
func convertRequest(req *approval.Request) RequestRecord {
	items := make([]ItemInfo, len(req.Items))
	for i, item := range req.Items {
		items[i] = convertItem(item)
	}
	sign, _ := req.GPGSign()
	search, _ := req.Search()
	var scope *approval.AutoApproveScope
	if s, err := req.AutoApproveScope(); err == nil {
		scope = &s
	}
	return RequestRecord{
		ID:               req.ID,
		Client:           req.SenderInfo.Client,
		Items:            items,
		Session:          req.Session,
		CreatedAt:        req.CreatedAt,
		Type:             req.Type,
		SearchAttributes: search.Attributes,
		SenderInfo:       req.SenderInfo.Info,
		GPGSignInfo:      sign,
		AutoApproveScope: scope,
	}
}

// Returns the request that convertRequest was given, except for
// SenderInfo.PeerTrusted and GPGResult, which the API does not send.
// AutoApproveScope is ignored, since convertRequest derives it.
func (r RequestRecord) request() *approval.Request {
	items := make([]approval.ItemInfo, len(r.Items))
	for i, item := range r.Items {
		items[i] = item.item()
	}
	return &approval.Request{
		ID:         r.ID,
		Items:      items,
		Session:    r.Session,
		CreatedAt:  r.CreatedAt,
		Type:       r.Type,
		Payload:    r.payload(),
		SenderInfo: approval.SenderInfo{Info: r.SenderInfo, Client: r.Client},
	}
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

// Returns the pending request as the API sends it.
func convertPending(p approval.Pending) PendingRequest {
	return PendingRequest{RequestRecord: convertRequest(p.Request), ExpiresAt: p.ExpiresAt}
}

// A trust rule as the API sends it, with Client set to the glob the rule
// matches clients with, the default included.
type TrustRule struct {
	approval.TrustRule
	Client approval.Glob `json:"client"`
}

func convertTrustRule(rule approval.TrustRule) TrustRule {
	return TrustRule{TrustRule: rule, Client: rule.ClientGlob()}
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

// HistoryEntry represents a resolved approval request in API responses.
type HistoryEntry struct {
	Request    RequestRecord       `json:"request"`
	Resolution approval.Resolution `json:"resolution"`
	ResolvedAt time.Time           `json:"resolved_at"`
}

// Returns the entry as the API sends it.
func convertHistoryEntry(entry approval.HistoryEntry) HistoryEntry {
	return HistoryEntry{
		Request:    convertRequest(entry.Request),
		Resolution: entry.Resolution,
		ResolvedAt: entry.ResolvedAt,
	}
}

// Returns the entry that convertHistoryEntry was given, as
// RequestRecord.request returns its request.
func (e HistoryEntry) entry() approval.HistoryEntry {
	return approval.HistoryEntry{Request: e.Request.request(), Resolution: e.Resolution, ResolvedAt: e.ResolvedAt}
}

// HistoryResponse is returned by GET /api/v1/log.
type HistoryResponse struct {
	Entries []HistoryEntry `json:"entries"`
}

// AuthRequest is the request body for POST /api/v1/auth.
type AuthRequest struct {
	Token string `json:"token"`
}
