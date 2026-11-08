// Package wsmsg names the kinds of message the daemon's WebSocket sends.
//
// The api package writes these messages and the gpgsign thin client reads
// them. api imports gpgsign, so the names live here where both can reach them.
package wsmsg

// The kind of a daemon WebSocket message.
type Type string

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
