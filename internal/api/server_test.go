package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
)

func TestServer_Integration(t *testing.T) {
	tempDir := t.TempDir()

	mgr := approval.NewManager(5 * time.Minute)
	auth, err := NewAuth(tempDir)
	if err != nil {
		t.Fatalf("NewAuth failed: %v", err)
	}

	// Use port 0 to get a random available port
	server, err := NewServer("127.0.0.1:0", mgr, "/remote/socket", "test-client", auth)
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
		req.Header.Set("Authorization", "Bearer "+auth.Token())

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
		// Start a pending request
		done := make(chan error, 1)
		go func() {
			done <- mgr.RequireApproval(context.Background(), "test-client", []approval.ItemInfo{{Path: "/test/item"}}, "/session/1")
		}()

		// Wait for request to appear
		var reqID string
		for i := 0; i < 100; i++ {
			reqs := mgr.List()
			if len(reqs) > 0 {
				reqID = reqs[0].ID
				break
			}
			time.Sleep(10 * time.Millisecond)
		}

		if reqID == "" {
			t.Fatal("request did not appear")
		}

		// Get pending list via API
		req, _ := http.NewRequest(http.MethodGet, baseURL+"/api/v1/pending", nil)
		req.Header.Set("Authorization", "Bearer "+auth.Token())

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
		req, _ = http.NewRequest(http.MethodPost, baseURL+"/api/v1/pending/"+reqID+"/approve", nil)
		req.Header.Set("Authorization", "Bearer "+auth.Token())

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
		case err := <-done:
			if err != nil {
				t.Errorf("RequireApproval should return nil, got: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("RequireApproval did not unblock")
		}
	})
}

func TestServer_CookieFilePath(t *testing.T) {
	tempDir := t.TempDir()

	mgr := approval.NewManager(5 * time.Minute)
	auth, err := NewAuth(tempDir)
	if err != nil {
		t.Fatalf("NewAuth failed: %v", err)
	}

	server, err := NewServer("127.0.0.1:0", mgr, "/socket", "test-client", auth)
	if err != nil {
		t.Fatalf("NewServer failed: %v", err)
	}

	if server.CookieFilePath() != auth.FilePath() {
		t.Errorf("expected %s, got %s", auth.FilePath(), server.CookieFilePath())
	}
}
