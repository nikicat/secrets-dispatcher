package api

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/config"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
)

// The web UI reads client names as JSON strings.
func TestPendingRequestClientWireFormat(t *testing.T) {
	data, err := json.Marshal(PendingRequest{Client: config.RemoteName("laptop").Client()})
	require.NoError(t, err)
	var wire map[string]any
	require.NoError(t, json.Unmarshal(data, &wire))
	assert.Equal(t, "remote:laptop", wire["client"])
}

func TestHistoryEntryRoundTrip(t *testing.T) {
	sender := approval.RemoteSenderForTest("gh")
	item := approval.ItemInfo{Path: "/org/freedesktop/secrets/collection/default/i1", Label: "Token", Attributes: dbustypes.Attributes{"service": "gh"}}
	for _, info := range []approval.RequestInfo{
		{Type: approval.RequestTypeGetSecret, Items: []approval.ItemInfo{item}, Session: "/session/1", SenderInfo: sender},
		{Type: approval.RequestTypeSearch, Items: []approval.ItemInfo{}, Payload: approval.SearchPayload{Attributes: dbustypes.Attributes{"service": "gh"}}, SenderInfo: sender},
		approval.CommitSignRequestForTest(sender, "repo", "a.go"),
	} {
		t.Run(string(info.Type), func(t *testing.T) {
			entry := approval.HistoryEntry{
				Request:    &approval.Request{ID: "id", RequestInfo: info, CreatedAt: time.Unix(1, 0)},
				Resolution: approval.ResolutionApproved,
				ResolvedAt: time.Unix(2, 0),
			}
			assert.Equal(t, entry, convertHistoryEntry(entry).entry())
		})
	}
}
