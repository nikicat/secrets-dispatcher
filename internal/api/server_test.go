package api

import (
	"context"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/apiclient"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/config"
	"github.com/nikicat/secrets-dispatcher/internal/proxy"
	"github.com/nikicat/secrets-dispatcher/internal/testutil"
)

func TestServer_Integration(t *testing.T) {
	mgr := approval.NewManagerForTest(approval.ManagerConfig{})
	auth := newTestAuth(t)

	// Use port 0 to get a random available port
	server, err := NewServer(ServerConfig{Addr: "127.0.0.1:0", Resolver: NewResolver(mgr, proxy.SlowUpstream{}), Auth: auth}, oneClient(testClient, "/remote/socket"))
	require.NoError(t, err, "NewServer")
	require.NoError(t, server.Start(), "Start")
	defer server.Shutdown(context.Background())

	baseURL := server.Addr().BaseURL()
	httpClient := &http.Client{Timeout: 5 * time.Second}
	client := apiclient.New(baseURL, auth.Token(), httpClient)

	// Test without auth
	t.Run("no auth returns 401", func(t *testing.T) {
		resp, err := httpClient.Get(baseURL.URL("/api/v1/status"))
		require.NoError(t, err, "request")
		defer resp.Body.Close()

		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	})

	// Test with valid auth
	t.Run("valid auth works", func(t *testing.T) {
		status, err := apiclient.GetJSON[StatusResponse](t.Context(), client, "/api/v1/status")
		require.NoError(t, err)

		assert.True(t, status.Running)
		require.Len(t, status.Clients, 1)
		assert.Equal(t, testClient, status.Clients[0].Name)
	})

	// Test full approval flow
	t.Run("approval flow", func(t *testing.T) {
		started := startPending(t, mgr, approval.ItemInfo{Path: "/test/item"})

		pending, err := apiclient.GetJSON[PendingListResponse](t.Context(), client, "/api/v1/pending")
		require.NoError(t, err)
		require.Len(t, pending.Requests, 1)

		require.NoError(t, client.PostAction(t.Context(), started.ID, "approve"))

		assert.NoError(t, started.wait(t), "RequireApproval")
	})
}

// Checks that shutting down a server serving TCP, the API socket and a remote
// socket logs no error: a clean stop must not look like a failure in the journal.
func TestServer_ShutdownLogsNoError(t *testing.T) {
	logs := testutil.CaptureLogs(t, slog.LevelError)

	server, err := NewServer(ServerConfig{Addr: "127.0.0.1:0", Resolver: NewResolver(approval.NewManagerForTest(approval.ManagerConfig{}), proxy.SlowUpstream{}), Auth: newTestAuth(t)}, oneClient(testClient, "/remote/socket"))
	require.NoError(t, err)
	require.NoError(t, server.Start())
	require.NoError(t, server.ServeAPISocket(config.RuntimeDir(t.TempDir())))
	_, err = server.ServeRemote("nb-home")
	require.NoError(t, err)

	require.NoError(t, server.Shutdown(context.Background()))

	assert.Empty(t, logs.String())
}
