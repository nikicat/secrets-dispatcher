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

	// SenderInfo contains information about the requesting process.
	SenderInfo SenderInfo `json:"sender_info"`

	// GPGSignInfo contains signing context for gpg_sign requests; nil for other types.
	GPGSignInfo *GPGSignInfo `json:"gpg_sign_info,omitempty"`

	// Signature holds the ASCII-armored PGP signature bytes produced by real gpg
	// on approval of a gpg_sign request. Set by ApproveWithSignature.
	Signature []byte `json:"-"`
	// GPGStatus holds the raw [GNUPG:] status lines from gpg --status-fd=2.
	// Set by ApproveWithSignature or ApproveGPGFailed.
	GPGStatus []byte `json:"-"`
	// GPGExitCode holds the gpg process exit code. Non-zero on failure.
	// Set by ApproveGPGFailed.
	GPGExitCode int `json:"-"`

	// Internal: channel signaled when request is approved/denied
	done   chan struct{}
	result bool // true = approved, false = denied
}

// Resolution represents how a request was resolved.
type Resolution string

const (
	ResolutionApproved  Resolution = "approved"
	ResolutionDenied    Resolution = "denied"
	ResolutionExpired   Resolution = "expired"
	ResolutionCancelled Resolution = "cancelled"
)

// HistoryEntry represents a resolved approval request.
type HistoryEntry struct {
	Request    *Request   `json:"request"`
	Resolution Resolution `json:"resolution"`
	ResolvedAt time.Time  `json:"resolved_at"`
}

// Manager tracks pending approval requests and handles blocking until decision.
type Manager struct {
	mu       sync.RWMutex
	pending  map[string]*Request
	timeout  time.Duration
	disabled bool // when true, auto-approve all requests

	observersMu sync.RWMutex
	observers   map[Observer]struct{}

	historyMu  sync.RWMutex
	history    []HistoryEntry
	historyMax int

	approvalWindow time.Duration
	cacheMu        sync.Mutex
	cache          map[string]time.Time // key = sender + "\x00" + itemPath
}

// NewManager creates a new approval manager.
// approvalWindow controls how long an approved (sender, item) pair is cached;
// a second request for the same pair within this window is auto-approved.
// Set to 0 to disable caching.
func NewManager(timeout time.Duration, historyMax int, approvalWindow time.Duration) *Manager {
	return &Manager{
		pending:        make(map[string]*Request),
		timeout:        timeout,
		observers:      make(map[Observer]struct{}),
		historyMax:     historyMax,
		approvalWindow: approvalWindow,
		cache:          make(map[string]time.Time),
	}
}

// NewDisabledManager creates a manager that auto-approves all requests.
func NewDisabledManager() *Manager {
	return &Manager{
		pending:    make(map[string]*Request),
		disabled:   true,
		observers:  make(map[Observer]struct{}),
		historyMax: 100,
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

	// Record history for terminal events
	if event.Type != EventRequestCreated {
		m.addHistory(event)
	}
}

// addHistory records a resolved request in history.
func (m *Manager) addHistory(event Event) {
	var resolution Resolution
	switch event.Type {
	case EventRequestApproved:
		resolution = ResolutionApproved
	case EventRequestDenied:
		resolution = ResolutionDenied
	case EventRequestExpired:
		resolution = ResolutionExpired
	case EventRequestCancelled:
		resolution = ResolutionCancelled
	default:
		return
	}

	entry := HistoryEntry{
		Request:    event.Request,
		Resolution: resolution,
		ResolvedAt: time.Now(),
	}

	m.historyMu.Lock()
	defer m.historyMu.Unlock()

	// Prepend to slice (newest first)
	m.history = append([]HistoryEntry{entry}, m.history...)

	// Trim to historyMax
	if len(m.history) > m.historyMax {
		m.history = m.history[:m.historyMax]
	}
}

// History returns a copy of the history entries, newest first.
func (m *Manager) History() []HistoryEntry {
	m.historyMu.RLock()
	defer m.historyMu.RUnlock()
	return append([]HistoryEntry{}, m.history...)
}

// AddHistoryEntry adds an entry directly to history. For testing only.
func (m *Manager) AddHistoryEntry(entry HistoryEntry) {
	m.historyMu.Lock()
	defer m.historyMu.Unlock()

	// Prepend to slice (newest first)
	m.history = append([]HistoryEntry{entry}, m.history...)

	// Trim to historyMax
	if len(m.history) > m.historyMax {
		m.history = m.history[:m.historyMax]
	}
}

// RequireApproval creates a pending request and blocks until approved, denied, or timeout.
// Returns nil if approved, ErrDenied if denied, ErrTimeout if timeout.
func (m *Manager) RequireApproval(ctx context.Context, client string, items []ItemInfo,
	session string, reqType RequestType, searchAttrs map[string]string, senderInfo SenderInfo) error {
	if m.disabled {
		return nil
	}

	// Check approval cache: if all items were recently approved for this sender, skip.
	if m.approvalWindow > 0 && len(items) > 0 {
		if m.checkApprovalCache(senderInfo.Sender, items) {
			return nil
		}
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
		SenderInfo:       senderInfo,
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
	delete(m.pending, id)
	close(req.done)
	m.mu.Unlock()

	m.notify(Event{Type: EventRequestApproved, Request: req})
	m.cacheApproval(req)
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
	delete(m.pending, id)
	close(req.done)
	m.mu.Unlock()

	m.notify(Event{Type: EventRequestDenied, Request: req})
	return nil
}

// Cancel cancels a pending request by ID. This is a system/client-driven
// cleanup (e.g. thin client interrupted), distinct from user-initiated Deny.
func (m *Manager) Cancel(id string) error {
	m.mu.Lock()
	req, ok := m.pending[id]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}

	req.result = false
	delete(m.pending, id)
	close(req.done)
	m.mu.Unlock()

	m.notify(Event{Type: EventRequestCancelled, Request: req})
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

// GetPending returns the pending request with the given ID, or nil if not found.
// Uses a read lock so it does not block concurrent approvals.
func (m *Manager) GetPending(id string) *Request {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.pending[id]
}

// ApproveWithSignature stores the real gpg signature and status on the request,
// then approves it. Used by HandleApprove for gpg_sign requests after real gpg succeeds.
func (m *Manager) ApproveWithSignature(id string, sig, status []byte) error {
	m.mu.Lock()
	req, ok := m.pending[id]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}

	req.Signature = sig
	req.GPGStatus = status
	req.result = true
	delete(m.pending, id)
	close(req.done)
	m.mu.Unlock()

	m.notify(Event{Type: EventRequestApproved, Request: req})
	m.cacheApproval(req)
	return nil
}

// ApproveGPGFailed stores the gpg failure status and exit code, then signals
// the request as approved so the done channel fires. The WebSocket message will
// carry the non-zero ExitCode; the thin client exits with it.
func (m *Manager) ApproveGPGFailed(id string, status []byte, exitCode int) error {
	m.mu.Lock()
	req, ok := m.pending[id]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}

	req.GPGStatus = status
	req.GPGExitCode = exitCode
	req.result = true
	delete(m.pending, id)
	close(req.done)
	m.mu.Unlock()

	m.notify(Event{Type: EventRequestApproved, Request: req})
	return nil
}

// approvalCacheKey returns the cache key for a (sender, itemPath) pair.
func approvalCacheKey(sender, itemPath string) string {
	return sender + "\x00" + itemPath
}

// checkApprovalCache returns true if ALL items have a valid cache entry for the sender.
// Expired entries encountered during the check are lazily deleted.
func (m *Manager) checkApprovalCache(sender string, items []ItemInfo) bool {
	if m.approvalWindow <= 0 {
		return false
	}
	now := time.Now()
	cutoff := now.Add(-m.approvalWindow)

	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()

	for _, item := range items {
		key := approvalCacheKey(sender, item.Path)
		ts, ok := m.cache[key]
		if !ok || ts.Before(cutoff) {
			if ok {
				delete(m.cache, key)
			}
			return false
		}
	}
	return true
}

// cacheApproval records approved (sender, item) pairs in the cache.
func (m *Manager) cacheApproval(req *Request) {
	if m.approvalWindow <= 0 {
		return
	}
	now := time.Now()

	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()

	for _, item := range req.Items {
		key := approvalCacheKey(req.SenderInfo.Sender, item.Path)
		m.cache[key] = now
	}
}
