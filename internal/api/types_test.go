package api

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/config"
)

// The web UI reads client names as JSON strings.
func TestPendingRequestClientWireFormat(t *testing.T) {
	data, err := json.Marshal(PendingRequest{Client: config.RemoteName("laptop").Client()})
	require.NoError(t, err)
	var wire map[string]any
	require.NoError(t, json.Unmarshal(data, &wire))
	assert.Equal(t, "remote:laptop", wire["client"])
}
