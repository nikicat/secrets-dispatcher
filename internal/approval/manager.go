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

// EventType represents the type of approval event.
type EventType int

const (
	EventRequestCreated EventType = iota
	EventRequestApproved
	EventRequestDenied
	EventRequestExpired
	EventRequestCancelled
)

// Event represents an approval event for observers.
type Event struct {
	Type    EventType
	Request *Request
}

// Observer receives notifications about approval events.
type Observer interface {
	OnEvent(Event)
}

// ItemInfo contains metadata about a secret item.
type ItemInfo struct {
	Path       string            `json:"path"`
	Label      string            `json:"label"`
	Attributes map[string]string `json:"attributes"`
}

// RequestType indicates the type of secret access request.
type RequestType string

const (
	RequestTypeGetSecret RequestType = "get_secret"
	RequestTypeSearch    RequestType = "search"
)

// Request represents a secret access request awaiting approval.
type Request struct {
	ID        string     `json:"id"`
	Client    string     `json:"client"`
	Items     []ItemInfo `json:"items"`
	Session   string     `json:"session"`
	CreatedAt time.Time  `json:"created_at"`
	ExpiresAt time.Time  `json:"expires_at"`

	// Type indicates whether this is a get_secret or search request.
	Type RequestType `json:"type"`

	// SearchAttributes contains the search criteria for search requests.
	SearchAttributes map[string]string `json:"search_attributes,omitempty"`

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

	observersMu sync.RWMutex
	observers   map[Observer]struct{}
}

// NewManager creates a new approval manager.
func NewManager(timeout time.Duration) *Manager {
	return &Manager{
		pending:   make(map[string]*Request),
		timeout:   timeout,
		observers: make(map[Observer]struct{}),
	}
}

// NewDisabledManager creates a manager that auto-approves all requests.
func NewDisabledManager() *Manager {
	return &Manager{
		pending:   make(map[string]*Request),
		disabled:  true,
		observers: make(map[Observer]struct{}),
	}
}

// Subscribe registers an observer to receive approval events.
func (m *Manager) Subscribe(o Observer) {
	m.observersMu.Lock()
	defer m.observersMu.Unlock()
	m.observers[o] = struct{}{}
}

// Unsubscribe removes an observer from receiving approval events.
func (m *Manager) Unsubscribe(o Observer) {
	m.observersMu.Lock()
	defer m.observersMu.Unlock()
	delete(m.observers, o)
}

// notify sends an event to all observers asynchronously.
func (m *Manager) notify(event Event) {
	m.observersMu.RLock()
	defer m.observersMu.RUnlock()
	for o := range m.observers {
		go o.OnEvent(event)
	}
}

// RequireApproval creates a pending request and blocks until approved, denied, or timeout.
// Returns nil if approved, ErrDenied if denied, ErrTimeout if timeout.
func (m *Manager) RequireApproval(ctx context.Context, client string, items []ItemInfo,
	session string, reqType RequestType, searchAttrs map[string]string) error {
	if m.disabled {
		return nil
	}

	now := time.Now()
	req := &Request{
		ID:               uuid.New().String(),
		Client:           client,
		Items:            items,
		Session:          session,
		CreatedAt:        now,
		ExpiresAt:        now.Add(m.timeout),
		Type:             reqType,
		SearchAttributes: searchAttrs,
		done:             make(chan struct{}),
	}

	m.mu.Lock()
	m.pending[req.ID] = req
	m.mu.Unlock()

	// Notify observers of new request
	m.notify(Event{Type: EventRequestCreated, Request: req})

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
		m.notify(Event{Type: EventRequestExpired, Request: req})
		return ErrTimeout
	case <-ctx.Done():
		m.notify(Event{Type: EventRequestCancelled, Request: req})
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
	req, ok := m.pending[id]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}

	req.result = true
	close(req.done)
	m.mu.Unlock()

	m.notify(Event{Type: EventRequestApproved, Request: req})
	return nil
}

// Deny denies a pending request by ID.
func (m *Manager) Deny(id string) error {
	m.mu.Lock()
	req, ok := m.pending[id]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}

	req.result = false
	close(req.done)
	m.mu.Unlock()

	m.notify(Event{Type: EventRequestDenied, Request: req})
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
