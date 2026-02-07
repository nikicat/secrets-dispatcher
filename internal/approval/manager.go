// Package approval manages pending secret access requests requiring user approval.
package approval

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ErrDenied is returned when a request is denied by the user.
var ErrDenied = errors.New("access denied by user")

// ErrTimeout is returned when a request times out waiting for approval.
var ErrTimeout = errors.New("approval request timed out")

// ErrNotFound is returned when a request ID doesn't exist.
var ErrNotFound = errors.New("request not found")

// Request represents a secret access request awaiting approval.
type Request struct {
	ID        string    `json:"id"`
	Client    string    `json:"client"`
	Items     []string  `json:"items"`
	Session   string    `json:"session"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`

	// Internal: channel signaled when request is approved/denied
	done   chan struct{}
	result bool // true = approved, false = denied
}

// Manager tracks pending approval requests and handles blocking until decision.
type Manager struct {
	mu       sync.RWMutex
	pending  map[string]*Request
	timeout  time.Duration
	disabled bool // when true, auto-approve all requests
}

// NewManager creates a new approval manager.
func NewManager(timeout time.Duration) *Manager {
	return &Manager{
		pending: make(map[string]*Request),
		timeout: timeout,
	}
}

// NewDisabledManager creates a manager that auto-approves all requests.
func NewDisabledManager() *Manager {
	return &Manager{
		pending:  make(map[string]*Request),
		disabled: true,
	}
}

// RequireApproval creates a pending request and blocks until approved, denied, or timeout.
// Returns nil if approved, ErrDenied if denied, ErrTimeout if timeout.
func (m *Manager) RequireApproval(ctx context.Context, client string, items []string, session string) error {
	if m.disabled {
		return nil
	}

	now := time.Now()
	req := &Request{
		ID:        uuid.New().String(),
		Client:    client,
		Items:     items,
		Session:   session,
		CreatedAt: now,
		ExpiresAt: now.Add(m.timeout),
		done:      make(chan struct{}),
	}

	m.mu.Lock()
	m.pending[req.ID] = req
	m.mu.Unlock()

	// Ensure cleanup when we exit
	defer func() {
		m.mu.Lock()
		delete(m.pending, req.ID)
		m.mu.Unlock()
	}()

	// Create timeout timer
	timer := time.NewTimer(m.timeout)
	defer timer.Stop()

	select {
	case <-req.done:
		if req.result {
			return nil
		}
		return ErrDenied
	case <-timer.C:
		return ErrTimeout
	case <-ctx.Done():
		return ctx.Err()
	}
}

// List returns all pending requests.
func (m *Manager) List() []*Request {
	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()
	result := make([]*Request, 0, len(m.pending))
	for _, req := range m.pending {
		// Only include non-expired requests
		if req.ExpiresAt.After(now) {
			result = append(result, req)
		}
	}
	return result
}

// Approve approves a pending request by ID.
func (m *Manager) Approve(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	req, ok := m.pending[id]
	if !ok {
		return ErrNotFound
	}

	req.result = true
	close(req.done)
	return nil
}

// Deny denies a pending request by ID.
func (m *Manager) Deny(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	req, ok := m.pending[id]
	if !ok {
		return ErrNotFound
	}

	req.result = false
	close(req.done)
	return nil
}

// PendingCount returns the number of pending requests.
func (m *Manager) PendingCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.pending)
}

// Timeout returns the configured timeout.
func (m *Manager) Timeout() time.Duration {
	return m.timeout
}
