package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/config"
)

func TestServer_Integration(t *testing.T) {
	mgr := newTestManager()
	auth := newTestAuth(t)

	// Use port 0 to get a random available port
	server, err := NewServer("127.0.0.1:0", mgr, "/remote/socket", "test-client", auth, false, nil, 0)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if err := server.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer server.Shutdown(context.Background())

	baseURL := "http://" + server.Addr()
	client := &http.Client{Timeout: 5 * time.Second}

	// Test without auth
	t.Run("no auth returns 401", func(t *testing.T) {
		resp, err := client.Get(baseURL + "/api/v1/status")
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", resp.StatusCode)
		}
	})

	// Test with valid auth
	t.Run("valid auth works", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, baseURL+"/api/v1/status", nil)
		auth.Token().Authorize(req.Header)

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
		}

		var status StatusResponse
		if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
			t.Fatalf("decode failed: %v", err)
		}

		if !status.Running {
			t.Error("expected running=true")
		}
		// Check new clients field
		if len(status.Clients) != 1 {
			t.Fatalf("expected 1 client, got %d", len(status.Clients))
		}
		if status.Clients[0].Name != "test-client" {
			t.Errorf("expected client name 'test-client', got '%s'", status.Clients[0].Name)
		}
		// Check deprecated field
		if status.Client != "test-client" {
			t.Errorf("expected client 'test-client', got '%s'", status.Client)
		}
	})

	// Test full approval flow
	t.Run("approval flow", func(t *testing.T) {
		started := startPending(t, mgr, approval.ItemInfo{Path: "/test/item"})

		// Get pending list via API
		req, _ := http.NewRequest(http.MethodGet, baseURL+"/api/v1/pending", nil)
		auth.Token().Authorize(req.Header)

		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}

		var pending PendingListResponse
		json.NewDecoder(resp.Body).Decode(&pending)
		resp.Body.Close()

		if len(pending.Requests) != 1 {
			t.Fatalf("expected 1 pending request, got %d", len(pending.Requests))
		}

		// Approve via API
		req, _ = http.NewRequest(http.MethodPost, baseURL+"/api/v1/pending/"+started.ID+"/approve", nil)
		auth.Token().Authorize(req.Header)

		resp, err = client.Do(req)
		if err != nil {
			t.Fatalf("approve request failed: %v", err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}

		// Verify approval unblocked the request
		select {
		case err := <-started.Done:
			if err != nil {
				t.Errorf("RequireApproval should return nil, got: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("RequireApproval did not unblock")
		}
	})
}

// Checks that shutting down a server serving TCP, the API socket and a remote
// socket logs no error: a clean stop must not look like a failure in the journal.
func TestServer_ShutdownLogsNoError(t *testing.T) {
	var logs bytes.Buffer
	defer slog.SetDefault(slog.Default())
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelError})))

	server, err := NewServer("127.0.0.1:0", newTestManager(), "/remote/socket", "test-client", newTestAuth(t), false, nil, 0)
	require.NoError(t, err)
	require.NoError(t, server.Start())
	require.NoError(t, server.ServeAPISocket(config.RuntimeDir(t.TempDir())))
	_, err = server.ServeRemote("nb-home")
	require.NoError(t, err)

	require.NoError(t, server.Shutdown(context.Background()))

	assert.Empty(t, logs.String())
}
