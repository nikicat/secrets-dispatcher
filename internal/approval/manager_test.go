package approval

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestManager_RequireApproval_Approved(t *testing.T) {
	mgr := NewManager(5 * time.Second)

	var wg sync.WaitGroup
	var approvalErr error

	wg.Add(1)
	go func() {
		defer wg.Done()
		approvalErr = mgr.RequireApproval(context.Background(), "test-client", []string{"/test/item"}, "/session/1")
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
		t.Fatal("request did not appear in pending list")
	}

	// Approve the request
	if err := mgr.Approve(reqID); err != nil {
		t.Fatalf("Approve failed: %v", err)
	}

	wg.Wait()

	if approvalErr != nil {
		t.Errorf("RequireApproval returned error: %v", approvalErr)
	}
}

func TestManager_RequireApproval_Denied(t *testing.T) {
	mgr := NewManager(5 * time.Second)

	var wg sync.WaitGroup
	var approvalErr error

	wg.Add(1)
	go func() {
		defer wg.Done()
		approvalErr = mgr.RequireApproval(context.Background(), "test-client", []string{"/test/item"}, "/session/1")
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
		t.Fatal("request did not appear in pending list")
	}

	// Deny the request
	if err := mgr.Deny(reqID); err != nil {
		t.Fatalf("Deny failed: %v", err)
	}

	wg.Wait()

	if approvalErr != ErrDenied {
		t.Errorf("expected ErrDenied, got: %v", approvalErr)
	}
}

func TestManager_RequireApproval_Timeout(t *testing.T) {
	mgr := NewManager(100 * time.Millisecond)

	err := mgr.RequireApproval(context.Background(), "test-client", []string{"/test/item"}, "/session/1")

	if err != ErrTimeout {
		t.Errorf("expected ErrTimeout, got: %v", err)
	}
}

func TestManager_RequireApproval_ContextCanceled(t *testing.T) {
	mgr := NewManager(5 * time.Second)

	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	var approvalErr error

	wg.Add(1)
	go func() {
		defer wg.Done()
		approvalErr = mgr.RequireApproval(ctx, "test-client", []string{"/test/item"}, "/session/1")
	}()

	// Wait for request to appear
	for i := 0; i < 100; i++ {
		if mgr.PendingCount() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Cancel context
	cancel()

	wg.Wait()

	if approvalErr != context.Canceled {
		t.Errorf("expected context.Canceled, got: %v", approvalErr)
	}
}

func TestManager_List(t *testing.T) {
	mgr := NewManager(5 * time.Second)

	// Start multiple approval requests
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			mgr.RequireApproval(ctx, "test-client", []string{"/test/item"}, "/session/1")
		}(i)
	}

	// Wait for requests to appear
	for i := 0; i < 100; i++ {
		if mgr.PendingCount() >= 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	reqs := mgr.List()
	if len(reqs) != 3 {
		t.Errorf("expected 3 pending requests, got %d", len(reqs))
	}

	wg.Wait()
}

func TestManager_Approve_NotFound(t *testing.T) {
	mgr := NewManager(5 * time.Second)

	err := mgr.Approve("nonexistent-id")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestManager_Deny_NotFound(t *testing.T) {
	mgr := NewManager(5 * time.Second)

	err := mgr.Deny("nonexistent-id")
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got: %v", err)
	}
}

func TestManager_Concurrent(t *testing.T) {
	mgr := NewManager(5 * time.Second)

	const numRequests = 10
	var wg sync.WaitGroup

	// Track results
	results := make([]error, numRequests)

	// Start approval requests
	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			results[n] = mgr.RequireApproval(context.Background(), "test-client", []string{"/test/item"}, "/session/1")
		}(i)
	}

	// Wait for requests to appear
	for i := 0; i < 100; i++ {
		if mgr.PendingCount() >= numRequests {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Approve half, deny half
	reqs := mgr.List()
	for i, req := range reqs {
		if i%2 == 0 {
			mgr.Approve(req.ID)
		} else {
			mgr.Deny(req.ID)
		}
	}

	wg.Wait()

	// Verify all requests completed
	var approved, denied int
	for _, err := range results {
		if err == nil {
			approved++
		} else if err == ErrDenied {
			denied++
		} else {
			t.Errorf("unexpected error: %v", err)
		}
	}

	if approved+denied != numRequests {
		t.Errorf("expected %d total results, got %d approved + %d denied", numRequests, approved, denied)
	}
}

func TestManager_Disabled(t *testing.T) {
	mgr := NewDisabledManager()

	// Should auto-approve immediately
	err := mgr.RequireApproval(context.Background(), "test-client", []string{"/test/item"}, "/session/1")
	if err != nil {
		t.Errorf("disabled manager should auto-approve, got: %v", err)
	}

	// No pending requests
	if mgr.PendingCount() != 0 {
		t.Error("disabled manager should have no pending requests")
	}
}

func TestManager_RequestFields(t *testing.T) {
	mgr := NewManager(5 * time.Second)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		mgr.RequireApproval(ctx, "test-client", []string{"/test/item1", "/test/item2"}, "/session/42")
	}()

	// Wait for request to appear
	for i := 0; i < 100; i++ {
		if mgr.PendingCount() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	reqs := mgr.List()
	if len(reqs) != 1 {
		t.Fatalf("expected 1 request, got %d", len(reqs))
	}

	req := reqs[0]
	if req.Client != "test-client" {
		t.Errorf("expected client 'test-client', got '%s'", req.Client)
	}
	if len(req.Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(req.Items))
	}
	if req.Session != "/session/42" {
		t.Errorf("expected session '/session/42', got '%s'", req.Session)
	}
	if req.ID == "" {
		t.Error("expected non-empty ID")
	}
	if req.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}
	if req.ExpiresAt.IsZero() {
		t.Error("expected non-zero ExpiresAt")
	}
	if req.ExpiresAt.Before(req.CreatedAt) {
		t.Error("ExpiresAt should be after CreatedAt")
	}

	wg.Wait()
}

func TestManager_CleanupAfterApproval(t *testing.T) {
	mgr := NewManager(5 * time.Second)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		mgr.RequireApproval(context.Background(), "test-client", []string{"/test/item"}, "/session/1")
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

	// Approve
	mgr.Approve(reqID)
	wg.Wait()

	// Verify request is cleaned up
	if mgr.PendingCount() != 0 {
		t.Error("request should be cleaned up after approval")
	}

	// Trying to approve again should fail
	if err := mgr.Approve(reqID); err != ErrNotFound {
		t.Errorf("second approval should return ErrNotFound, got: %v", err)
	}
}
