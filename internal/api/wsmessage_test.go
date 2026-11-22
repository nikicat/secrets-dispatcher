package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/proxy"
)

var goldenTime = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

// Returns a get_secret request from the client laptop, created at goldenTime.
func goldenRequest() *approval.Request {
	return &approval.Request{
		ID:         "req-1",
		Items:      []approval.ItemInfo{{Path: "/org/freedesktop/secrets/collection/default/1", Label: "Token"}},
		Session:    "/session/1",
		CreatedAt:  goldenTime,
		Type:       approval.RequestTypeGetSecret,
		SenderInfo: approval.SenderInfo{Client: "laptop"},
	}
}

// Returns the messages that tell the WebSocket peer the golden request ended
// with res.
func goldenEnd(res approval.Resolution) []WSMessage {
	return endedMessages(approval.HistoryEntry{Request: goldenRequest(), Resolution: res, ResolvedAt: goldenTime})
}

// Pins the JSON of every WebSocket message type, which the web UI and the
// gpgsign thin client decode.
func TestWSMessage_JSON(t *testing.T) {
	pending := approval.Pending{Request: goldenRequest(), ExpiresAt: goldenTime.Add(time.Minute)}
	entry := convertHistoryEntry(approval.HistoryEntry{Request: goldenRequest(), Resolution: approval.ResolutionApproved, ResolvedAt: goldenTime})
	rule := approval.AutoApproveRule{ID: "rule-1", InvokerName: "gh", InvokerExe: "/usr/bin/gh", RequestType: approval.RequestTypeGetSecret, Collection: "default", ExpiresAt: goldenTime}
	client := proxy.ClientInfo{Name: "laptop", SocketPath: "/run/laptop.sock"}
	signed := goldenRequest()
	signed.Type = approval.RequestTypeGPGSign
	signed.GPGResult = approval.GPGResult{Signature: []byte("SIG"), Status: []byte("[GNUPG:] SIG_CREATED")}
	signedEnd := endedMessages(approval.HistoryEntry{Request: signed, Resolution: approval.ResolutionAutoApproved, ResolvedAt: goldenTime})

	const (
		// The golden request's object without its closing brace.
		recordJSON  = `{"id":"req-1","client":"laptop","items":[{"path":"/org/freedesktop/secrets/collection/default/1","label":"Token","attributes":null}],"session":"/session/1","created_at":"2026-11-21T12:00:00Z","type":"get_secret","sender_info":{"pid":0,"invoker_name":""},"auto_approvable":false`
		pendingJSON = recordJSON + `,"expires_at":"2026-11-21T12:01:00Z"}`
		entryJSON   = `{"request":` + recordJSON + `},"resolution":"approved","resolved_at":"2026-11-21T12:00:00Z"}`
		ruleJSON    = `{"id":"rule-1","invoker_name":"gh","invoker_exe":"/usr/bin/gh","request_type":"get_secret","collection":"default","expires_at":"2026-11-21T12:00:00Z"}`
		clientJSON  = `{"name":"laptop","socket_path":"/run/laptop.sock"}`
	)
	tests := []struct {
		name string
		msg  WSMessage
		want string
	}{
		{
			name: "snapshot",
			msg: WSMessage{snapshotMsg{
				Version: "v1", Requests: []PendingRequest{convertPending(pending)}, Clients: []proxy.ClientInfo{client},
				History: []HistoryEntry{entry}, AutoApproveRules: []approval.AutoApproveRule{rule},
				TrustedSigners: []approval.TrustedSigner{}, TrustRules: []approval.TrustRule{},
				AutoApproveDurationSeconds: 120, NotificationDelayMS: 500,
			}},
			want: `{"type":"snapshot","version":"v1","requests":[` + pendingJSON + `],"clients":[` + clientJSON + `],"history":[` + entryJSON + `],"auto_approve_rules":[` + ruleJSON + `],"trusted_signers":[],"trust_rules":[],"auto_approve_duration_seconds":120,"notification_delay_ms":500}`,
		},
		{
			name: "request_created",
			msg:  WSMessage{requestCreatedMsg{convertPending(pending)}},
			want: `{"type":"request_created","request":` + pendingJSON + `}`,
		},
		{
			name: "request_resolved",
			msg:  goldenEnd(approval.ResolutionDenied)[0],
			want: `{"type":"request_resolved","id":"req-1","result":"denied"}`,
		},
		{
			name: "request_resolved with a signature",
			msg:  signedEnd[0],
			want: `{"type":"request_resolved","id":"req-1","result":"auto_approved","signature":"U0lH","gpg_status":"W0dOVVBHOl0gU0lHX0NSRUFURUQ=","exit_code":0}`,
		},
		{
			name: "request_expired",
			msg:  goldenEnd(approval.ResolutionExpired)[0],
			want: `{"type":"request_expired","id":"req-1"}`,
		},
		{
			name: "request_cancelled",
			msg:  goldenEnd(approval.ResolutionCancelled)[0],
			want: `{"type":"request_cancelled","id":"req-1"}`,
		},
		{
			name: "history_entry",
			msg:  goldenEnd(approval.ResolutionApproved)[1],
			want: `{"type":"history_entry","history_entry":` + entryJSON + `}`,
		},
		{
			name: "auto_approve_rule_added",
			msg:  WSMessage{autoApproveRuleAddedMsg{rule}},
			want: `{"type":"auto_approve_rule_added","auto_approve_rule":` + ruleJSON + `}`,
		},
		{
			name: "auto_approve_rule_removed",
			msg:  WSMessage{autoApproveRuleRemovedMsg{ID: rule.ID}},
			want: `{"type":"auto_approve_rule_removed","id":"rule-1"}`,
		},
		{
			name: "client_connected",
			msg:  WSMessage{clientConnectedMsg{client}},
			want: `{"type":"client_connected","client":` + clientJSON + `}`,
		},
		{
			name: "client_disconnected",
			msg:  WSMessage{clientDisconnectedMsg{client}},
			want: `{"type":"client_disconnected","client":` + clientJSON + `}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.msg)
			require.NoError(t, err)
			assert.Equal(t, tt.want, string(got))
		})
	}
}

// An ignored request and an auto-approval without gpg output send only their
// History entry.
func TestEndedMessages_HistoryEntryOnly(t *testing.T) {
	for _, res := range []approval.Resolution{approval.ResolutionIgnored, approval.ResolutionAutoApproved} {
		msgs := goldenEnd(res)
		require.Len(t, msgs, 1, res)
		assert.IsType(t, historyEntryMsg{}, msgs[0].payload, res)
	}
}
