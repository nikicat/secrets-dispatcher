// Package wsmsg names the kinds of message the daemon's WebSocket sends, and
// the JSON key that carries the kind.
package wsmsg

// The kind of a daemon WebSocket message.
type Type string

// The part of every daemon WebSocket message that names its kind.
type Header struct {
	Type Type `json:"type"`
}

const (
	Snapshot               Type = "snapshot"
	RequestCreated         Type = "request_created"
	RequestResolved        Type = "request_resolved"
	RequestExpired         Type = "request_expired"
	RequestCancelled       Type = "request_cancelled"
	HistoryEntry           Type = "history_entry"
	AutoApproveRuleAdded   Type = "auto_approve_rule_added"
	AutoApproveRuleRemoved Type = "auto_approve_rule_removed"
	ClientConnected        Type = "client_connected"
	ClientDisconnected     Type = "client_disconnected"
)
