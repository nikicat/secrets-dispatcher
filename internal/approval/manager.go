// Package approval manages pending secret access requests requiring user approval.
package approval

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
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
	EventRequestAutoApproved
	EventAutoApproveRuleAdded
	EventAutoApproveRuleRemoved
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
	RequestTypeDelete    RequestType = "delete"
	RequestTypeWrite     RequestType = "write"
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
	ResolutionApproved     Resolution = "approved"
	ResolutionDenied       Resolution = "denied"
	ResolutionExpired      Resolution = "expired"
	ResolutionCancelled    Resolution = "cancelled"
	ResolutionAutoApproved Resolution = "auto_approved"
)

// HistoryEntry represents a resolved approval request.
type HistoryEntry struct {
	Request    *Request   `json:"request"`
	Resolution Resolution `json:"resolution"`
	ResolvedAt time.Time  `json:"resolved_at"`
}

// TrustedSigner defines a process auto-approved for GPG signing.
// All three fields must match. Empty optional fields match anything.
type TrustedSigner struct {
	ExePath    string // Required: absolute path to the executable
	RepoPath   string // Optional: repo basename; empty matches any repo
	FilePrefix string // Optional: all changed files must have this prefix; empty matches any
}

// AutoApproveRule defines a temporary rule that auto-approves matching requests.
type AutoApproveRule struct {
	ID          string            `json:"id"`
	InvokerName string            `json:"invoker_name"`
	RequestType RequestType       `json:"request_type"`
	Collection  string            `json:"collection"`
	Attributes  map[string]string `json:"attributes,omitempty"`
	ExpiresAt   time.Time         `json:"expires_at"`
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

	autoApproveMu       sync.Mutex
	autoApproveRules     []AutoApproveRule
	autoApproveDuration  time.Duration
	trustedSigners       []TrustedSigner // exe+repo combos auto-approved for gpg_sign
}

// ManagerConfig holds configuration for the approval Manager.
type ManagerConfig struct {
	// Timeout is how long a request waits for user approval before expiring.
	Timeout time.Duration
	// HistoryMax is the maximum number of resolved requests kept in history.
	HistoryMax int
	// ApprovalWindow controls how long an approved (sender, item) pair is cached;
	// a second request for the same pair within this window is auto-approved.
	// Set to 0 to disable caching.
	ApprovalWindow time.Duration
	// AutoApproveDuration controls how long an auto-approve rule lasts after creation.
	AutoApproveDuration time.Duration
	// TrustedSigners is a list of executable paths auto-approved for gpg_sign requests.
	TrustedSigners []TrustedSigner
}

// NewManager creates a new approval manager.
func NewManager(cfg ManagerConfig) *Manager {
	return &Manager{
		pending:             make(map[string]*Request),
		timeout:             cfg.Timeout,
		observers:           make(map[Observer]struct{}),
		historyMax:          cfg.HistoryMax,
		approvalWindow:      cfg.ApprovalWindow,
		cache:               make(map[string]time.Time),
		autoApproveDuration: cfg.AutoApproveDuration,
		trustedSigners:      cfg.TrustedSigners,
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

	// Record history for terminal request events (not rule-management events)
	if event.Type != EventRequestCreated &&
		event.Type != EventAutoApproveRuleAdded &&
		event.Type != EventAutoApproveRuleRemoved {
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
	case EventRequestAutoApproved:
		resolution = ResolutionAutoApproved
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

// GetHistoryEntry returns the history entry with the given request ID, or nil if not found.
func (m *Manager) GetHistoryEntry(requestID string) *HistoryEntry {
	m.historyMu.RLock()
	defer m.historyMu.RUnlock()
	for i := range m.history {
		if m.history[i].Request.ID == requestID {
			return &m.history[i]
		}
	}
	return nil
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
	// Delete and write requests always require explicit approval — never use cached approvals.
	if reqType != RequestTypeDelete && reqType != RequestTypeWrite && m.approvalWindow > 0 && len(items) > 0 {
		if m.checkApprovalCache(senderInfo.Sender, items) {
			return nil
		}
	}

	// Check auto-approve rules (for timed-out client retries).
	if rule := m.checkAutoApproveRules(senderInfo, items, reqType); rule != nil {
		slog.Info("auto-approve rule matched",
			"rule_id", rule.ID,
			"invoker", rule.InvokerName,
			"type", rule.RequestType)
		now := time.Now()
		req := &Request{
			ID:               uuid.New().String(),
			Client:           client,
			Items:            items,
			Session:          session,
			CreatedAt:        now,
			ExpiresAt:        now,
			Type:             reqType,
			SearchAttributes: searchAttrs,
			SenderInfo:       senderInfo,
		}
		m.notify(Event{Type: EventRequestAutoApproved, Request: req})
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

// CacheItemForSender records an item approval in the cache for a specific sender.
// This allows auto-approving a subsequent read of an item that was just written
// (e.g., gh verifies keyring storage by reading back a secret right after CreateItem).
func (m *Manager) CacheItemForSender(sender, itemPath string) {
	if m.approvalWindow <= 0 || m.disabled {
		return
	}

	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()

	key := approvalCacheKey(sender, itemPath)
	m.cache[key] = time.Now()
}

// extractCollection extracts the collection name from a Secret Service item path.
// E.g., "/org/freedesktop/secrets/collection/default/123" → "default".
// Returns "" if the path doesn't match the expected format.
func extractCollection(itemPath string) string {
	const prefix = "/org/freedesktop/secrets/collection/"
	if !strings.HasPrefix(itemPath, prefix) {
		return ""
	}
	rest := itemPath[len(prefix):]
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[:i]
	}
	return rest
}

// AddAutoApproveRule creates a temporary auto-approve rule from a cancelled request.
// Returns the rule ID.
func (m *Manager) AddAutoApproveRule(req *Request) string {
	duration := m.autoApproveDuration
	if duration <= 0 {
		duration = 2 * time.Minute
	}

	rule := AutoApproveRule{
		ID:          uuid.New().String(),
		InvokerName: req.SenderInfo.UnitName,
		RequestType: req.Type,
		ExpiresAt:   time.Now().Add(duration),
	}

	// Extract collection and attributes from first item
	if len(req.Items) > 0 {
		rule.Collection = extractCollection(req.Items[0].Path)
		rule.Attributes = req.Items[0].Attributes
	}

	// For search requests, use search attributes
	if req.Type == RequestTypeSearch && len(req.SearchAttributes) > 0 {
		rule.Attributes = req.SearchAttributes
	}

	m.autoApproveMu.Lock()
	m.autoApproveRules = append(m.autoApproveRules, rule)
	m.autoApproveMu.Unlock()

	m.notify(Event{Type: EventAutoApproveRuleAdded, Request: req})
	slog.Info("auto-approve rule added",
		"rule_id", rule.ID,
		"invoker", rule.InvokerName,
		"type", rule.RequestType,
		"collection", rule.Collection,
		"expires_at", rule.ExpiresAt)

	return rule.ID
}

// checkAutoApproveRules checks if the request matches any active auto-approve rule.
// Returns the matching rule or nil.
func (m *Manager) checkAutoApproveRules(senderInfo SenderInfo, items []ItemInfo, reqType RequestType) *AutoApproveRule {
	m.autoApproveMu.Lock()
	defer m.autoApproveMu.Unlock()

	now := time.Now()
	// Clean expired rules while iterating
	active := m.autoApproveRules[:0]
	var match *AutoApproveRule

	for i := range m.autoApproveRules {
		rule := &m.autoApproveRules[i]
		if rule.ExpiresAt.Before(now) {
			continue // expired, skip
		}
		active = append(active, *rule)

		if match != nil {
			continue // already found a match, just cleaning
		}

		// Match invoker name
		if rule.InvokerName != senderInfo.UnitName {
			continue
		}
		// Match request type
		if rule.RequestType != reqType {
			continue
		}
		// Match collection
		reqCollection := ""
		if len(items) > 0 {
			reqCollection = extractCollection(items[0].Path)
		}
		if rule.Collection != "" && rule.Collection != reqCollection {
			continue
		}
		// Match attributes (subset match: all rule attrs must be present in request)
		reqAttrs := map[string]string{}
		if len(items) > 0 && items[0].Attributes != nil {
			reqAttrs = items[0].Attributes
		}
		if attributesMatch(rule.Attributes, reqAttrs) {
			matched := *rule
			match = &matched
		}
	}

	m.autoApproveRules = active
	return match
}

// attributesMatch returns true if all entries in ruleAttrs are present in reqAttrs.
func attributesMatch(ruleAttrs, reqAttrs map[string]string) bool {
	for k, v := range ruleAttrs {
		if reqAttrs[k] != v {
			return false
		}
	}
	return true
}

// ListAutoApproveRules returns all active (non-expired) auto-approve rules.
func (m *Manager) ListAutoApproveRules() []AutoApproveRule {
	m.autoApproveMu.Lock()
	defer m.autoApproveMu.Unlock()

	now := time.Now()
	var active []AutoApproveRule
	for _, rule := range m.autoApproveRules {
		if rule.ExpiresAt.After(now) {
			active = append(active, rule)
		}
	}
	return active
}

// RemoveAutoApproveRule removes an auto-approve rule by ID.
func (m *Manager) RemoveAutoApproveRule(id string) error {
	m.autoApproveMu.Lock()
	defer m.autoApproveMu.Unlock()

	for i, rule := range m.autoApproveRules {
		if rule.ID == id {
			m.autoApproveRules = append(m.autoApproveRules[:i], m.autoApproveRules[i+1:]...)
			slog.Info("auto-approve rule removed", "rule_id", id)
			return nil
		}
	}
	return ErrNotFound
}

// CheckTrustedSigner checks if a GPG sign request comes from a trusted signer.
// Returns true if ANY process in the sender's process chain matches a
// trusted_signers entry (exe_path + repo_path + file_prefix all match).
func (m *Manager) CheckTrustedSigner(senderInfo SenderInfo, repoName string, changedFiles []string) bool {
	if len(m.trustedSigners) == 0 {
		return false
	}

	for _, proc := range senderInfo.ProcessChain {
		exePath := readExePath(proc.PID)
		if exePath == "" {
			continue
		}
		for _, ts := range m.trustedSigners {
			if exePath != ts.ExePath {
				continue
			}
			if ts.RepoPath != "" && repoName != ts.RepoPath {
				continue
			}
			if ts.FilePrefix != "" && !allFilesMatch(changedFiles, ts.FilePrefix) {
				continue
			}
			return true
		}
	}
	return false
}

// allFilesMatch returns true if every file starts with prefix.
func allFilesMatch(files []string, prefix string) bool {
	for _, f := range files {
		if !strings.HasPrefix(f, prefix) {
			return false
		}
	}
	return true
}

// readExePath reads the executable path from /proc/PID/exe.
func readExePath(pid uint32) string {
	if pid == 0 {
		return ""
	}
	target, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return ""
	}
	return target
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
