// Package approval manages pending secret access requests requiring user approval.
package approval

import (
	"context"
	"errors"
	"log/slog"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
)

// ErrDenied is returned when a request is denied by the user.
var ErrDenied = errors.New("access denied by user")

// ErrTimeout is returned when a request times out waiting for approval.
var ErrTimeout = errors.New("approval request timed out")

// ErrNotFound is returned when a request ID doesn't exist.
var ErrNotFound = errors.New("request not found")

// ErrIgnored is returned when a request is silently dropped by a trust rule with action "ignore".
var ErrIgnored = errors.New("request ignored by trust rule")

// ErrDeniedByRule is returned when a request is denied by a trust rule with action "deny".
var ErrDeniedByRule = errors.New("access denied by trust rule")

// EventType represents the type of approval event.
type EventType int

const (
	EventRequestCreated EventType = iota
	EventRequestApproved
	EventRequestDenied
	EventRequestExpired
	EventRequestCancelled
	EventRequestAutoApproved
	EventRequestIgnored
	EventAutoApproveRuleAdded
	EventAutoApproveRuleRemoved
)

// Event represents an approval event for observers.
type Event struct {
	Type    EventType
	Request *Request
	Rule    *AutoApproveRule // For EventAutoApproveRuleAdded/Removed
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
	RequestTypeSSHSign   RequestType = "ssh_sign"
	RequestTypeUnlock    RequestType = "unlock"
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
	ResolutionIgnored      Resolution = "ignored"
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
	ExePath    string `json:"exe_path"`              // Required: absolute path to the executable
	RepoPath   string `json:"repo_path,omitempty"`   // Optional: repo basename; empty matches any repo
	FilePrefix string `json:"file_prefix,omitempty"` // Optional: all changed files must have this prefix; empty matches any
}

// AutoApproveRule defines a temporary rule that auto-approves matching requests.
//
// InvokerExe is the non-spoofable identity the rule matches on: the invoking
// process's /proc/PID/exe path. InvokerName holds the caller's comm and is
// retained for display/logging only — it is attacker-controllable
// (prctl(PR_SET_NAME)) and must never be the basis for a match.
type AutoApproveRule struct {
	ID          string            `json:"id"`
	InvokerName string            `json:"invoker_name"`
	InvokerExe  string            `json:"invoker_exe,omitempty"`
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
	autoApproveRules    []AutoApproveRule
	autoApproveDuration time.Duration
	trustedSigners      []TrustedSigner // exe+repo combos auto-approved for gpg_sign
	ignoreChromeDummy   bool
	trustRules          []TrustRule // persistent config-defined trust rules
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
	// IgnoreChromeDummy silently ignores Chrome's dummy secret writes.
	IgnoreChromeDummy bool
	// TrustRules are persistent config-defined rules for auto-approve/ignore.
	TrustRules []TrustRule
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
		ignoreChromeDummy:   cfg.IgnoreChromeDummy,
		trustRules:          cfg.TrustRules,
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
		o.OnEvent(event)
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
	case EventRequestIgnored:
		resolution = ResolutionIgnored
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
// Returns (autoApproved, nil) if approved — autoApproved is true when the request was
// resolved without showing a notification (cache hit, auto-approve rule, trust rule, or
// disabled manager). Returns (false, err) on denial, timeout, or cancellation.
func (m *Manager) RequireApproval(ctx context.Context, client string, items []ItemInfo,
	session string, reqType RequestType, searchAttrs map[string]string, senderInfo SenderInfo) (bool, error) {
	if m.disabled {
		return true, nil
	}

	// Check approval cache: if all items were recently approved for this sender, skip.
	// Delete and write requests always require explicit approval — never use cached approvals.
	if reqType != RequestTypeDelete && reqType != RequestTypeWrite && m.approvalWindow > 0 && len(items) > 0 {
		if m.checkApprovalCache(senderInfo.Sender, items) {
			return true, nil
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
		return true, nil
	}

	// Check persistent trust rules from config.
	if rule := m.CheckTrustRules(senderInfo, items, reqType, searchAttrs); rule != nil {
		action := rule.Action
		if action == "" {
			action = "approve"
		}
		slog.Info("trust rule matched",
			"rule_name", rule.Name,
			"action", action)
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
		if action == "ignore" {
			m.notify(Event{Type: EventRequestIgnored, Request: req})
			return true, ErrIgnored
		}
		if action == "deny" {
			m.notify(Event{Type: EventRequestDenied, Request: req})
			return true, ErrDeniedByRule
		}
		m.notify(Event{Type: EventRequestAutoApproved, Request: req})
		return true, nil
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
			return false, nil
		}
		return false, ErrDenied
	case <-timer.C:
		m.notify(Event{Type: EventRequestExpired, Request: req})
		return false, ErrTimeout
	case <-ctx.Done():
		m.notify(Event{Type: EventRequestCancelled, Request: req})
		return false, ctx.Err()
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

// ApproveAndAutoApprove approves a pending request and creates an auto-approve
// rule for similar future requests.
func (m *Manager) ApproveAndAutoApprove(id string) error {
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
	m.AddAutoApproveRule(req)
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

// AutoApproveDuration returns the configured auto-approve rule duration.
func (m *Manager) AutoApproveDuration() time.Duration {
	d := m.autoApproveDuration
	if d <= 0 {
		d = 2 * time.Minute
	}
	return d
}

// extractCollection extracts the collection name from a Secret Service item path.
var extractCollection = dbustypes.ExtractCollection

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
		InvokerExe:  invokerExePath(req.SenderInfo),
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
	// Dedup: if a matching rule already exists, refresh its expiry
	for i := range m.autoApproveRules {
		existing := &m.autoApproveRules[i]
		if existing.InvokerExe == rule.InvokerExe &&
			existing.RequestType == rule.RequestType &&
			existing.Collection == rule.Collection &&
			attributesEqual(existing.Attributes, rule.Attributes) {
			existing.ExpiresAt = rule.ExpiresAt
			m.autoApproveMu.Unlock()
			m.notify(Event{Type: EventAutoApproveRuleAdded, Rule: existing})
			slog.Info("auto-approve rule refreshed",
				"rule_id", existing.ID,
				"invoker", existing.InvokerName,
				"expires_at", existing.ExpiresAt)
			return existing.ID
		}
	}
	m.autoApproveRules = append(m.autoApproveRules, rule)
	m.autoApproveMu.Unlock()

	m.notify(Event{Type: EventAutoApproveRuleAdded, Rule: &rule})
	slog.Info("auto-approve rule added",
		"rule_id", rule.ID,
		"invoker", rule.InvokerName,
		"type", rule.RequestType,
		"collection", rule.Collection,
		"expires_at", rule.ExpiresAt)

	return rule.ID
}

// CheckAutoApproveRules exposes checkAutoApproveRules for callers outside the
// approval package (e.g. the gpg_sign HTTP handler) that drive their own request
// flow and need to consult ephemeral auto-approve rules before opening a
// notification. Same matching semantics as RequireApproval's auto-approve check.
func (m *Manager) CheckAutoApproveRules(senderInfo SenderInfo, items []ItemInfo, reqType RequestType) *AutoApproveRule {
	return m.checkAutoApproveRules(senderInfo, items, reqType)
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

		// Match on the non-spoofable invoker exe path, never the caller's comm
		// (UnitName), which is attacker-controllable. Fail closed when either the
		// rule or the caller lacks a resolved exe.
		callerExe := invokerExePath(senderInfo)
		if callerExe == "" || rule.InvokerExe != callerExe {
			continue
		}
		// Match request type
		if rule.RequestType != reqType {
			continue
		}
		// Match collection + attributes for EVERY item in the batch. An
		// auto-approve rule is permissive, so a single decision covers the whole
		// batch only when every item falls within the rule's scope; matching just
		// items[0] would let a batch smuggle an out-of-scope secret past a rule
		// scoped to a benign collection.
		if autoApproveCoversAll(rule, items) {
			matched := *rule
			match = &matched
		}
	}

	m.autoApproveRules = active
	return match
}

// autoApproveCoversAll reports whether an auto-approve rule covers every item in
// the batch (collection and attributes). An auto-approve rule is permissive, so
// it may only authorize a batch when all items are in scope. An empty batch is
// covered only when the rule imposes no secret-scope constraints — the case that
// carries the itemless gpg_sign path.
func autoApproveCoversAll(rule *AutoApproveRule, items []ItemInfo) bool {
	if len(items) == 0 {
		return rule.Collection == "" && len(rule.Attributes) == 0
	}
	for _, it := range items {
		if rule.Collection != "" && rule.Collection != extractCollection(it.Path) {
			return false
		}
		attrs := it.Attributes
		if attrs == nil {
			attrs = map[string]string{}
		}
		if !attributesMatch(rule.Attributes, attrs) {
			return false
		}
	}
	return true
}

// attributesEqual returns true if both maps have the same keys and values.
func attributesEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// attributesMatch returns true if all entries in ruleAttrs are present in reqAttrs.
func attributesMatch(ruleAttrs, reqAttrs map[string]string) bool {
	for k, v := range ruleAttrs {
		if ok, _ := path.Match(v, reqAttrs[k]); !ok {
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

// ListTrustedSigners returns the configured trusted signers.
func (m *Manager) ListTrustedSigners() []TrustedSigner {
	return m.trustedSigners
}

// RemoveAutoApproveRule removes an auto-approve rule by ID.
func (m *Manager) RemoveAutoApproveRule(id string) error {
	m.autoApproveMu.Lock()
	var removed AutoApproveRule
	found := false
	for i, rule := range m.autoApproveRules {
		if rule.ID == id {
			removed = rule
			m.autoApproveRules = append(m.autoApproveRules[:i], m.autoApproveRules[i+1:]...)
			found = true
			break
		}
	}
	m.autoApproveMu.Unlock()

	if !found {
		return ErrNotFound
	}

	m.notify(Event{Type: EventAutoApproveRuleRemoved, Rule: &removed})
	slog.Info("auto-approve rule removed", "rule_id", id)
	return nil
}

// CheckTrustedSigner reports whether a GPG sign request should be silently
// auto-signed under a trusted_signers entry.
//
// Matching is intentionally any-ancestor: a trusted_signers exe_path names the app
// that INITIATED the commit (e.g. an editor or deploy script), which sits above git
// in the chain — not git itself, which is the universal direct invoker. So the
// trusted exe may appear anywhere in the sender's process chain.
//
// The silent path is gated on senderInfo.PeerTrusted: the request must have arrived
// through our own gpg-sign thin client. That is the only case in which repoName and
// changedFiles are trustworthy — our helper computed them from the real repository.
// A process speaking the socket protocol directly can put any exe in its ancestry
// and forge repoName/changedFiles (neither is verifiable against the signed commit
// object), so it must never take the silent path; it falls through to interactive
// approval instead (which, per the WYSIWYS binding, shows the real committed bytes).
func (m *Manager) CheckTrustedSigner(senderInfo SenderInfo, repoName string, changedFiles []string) bool {
	if len(m.trustedSigners) == 0 {
		return false
	}
	if !senderInfo.PeerTrusted {
		return false
	}

	for _, proc := range senderInfo.ProcessChain {
		if proc.Exe == "" {
			continue
		}
		for _, ts := range m.trustedSigners {
			if proc.Exe != ts.ExePath {
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

// invokerExePath returns the non-spoofable executable path (/proc/PID/exe) of the
// invoking process — the chain entry identified by senderInfo.PID. Unlike
// UnitName (which normally holds the caller's spoofable comm), this cannot be
// forged with prctl(PR_SET_NAME). Returns "" when no exe can be resolved, in which
// case callers must fail closed rather than fall back to comm.
func invokerExePath(s SenderInfo) string {
	for _, p := range s.ProcessChain {
		if p.PID == s.PID && p.Exe != "" {
			return p.Exe
		}
	}
	return ""
}

// CheckTrustRules checks if the request matches any configured trust rule.
// Returns the first matching rule, or nil if no rules match.
func (m *Manager) CheckTrustRules(senderInfo SenderInfo, items []ItemInfo, reqType RequestType, searchAttrs map[string]string) *TrustRule {
	for i := range m.trustRules {
		rule := &m.trustRules[i]
		if !matchTrustRule(rule, senderInfo, items, reqType, searchAttrs) {
			continue
		}
		return rule
	}
	return nil
}

// matchTrustRule returns true if the request matches a single trust rule.
func matchTrustRule(rule *TrustRule, senderInfo SenderInfo, items []ItemInfo, reqType RequestType, searchAttrs map[string]string) bool {
	// Check request_types filter
	if len(rule.RequestTypes) > 0 {
		found := slices.Contains(rule.RequestTypes, string(reqType))
		if !found {
			return false
		}
	}

	// Check process matcher
	if rule.Process != nil {
		if !matchProcess(rule.Process, senderInfo) {
			return false
		}
	}

	// Check secret matcher. deny/ignore rules are restrictive (fire if ANY item
	// is in scope); approve rules are permissive (fire only if EVERY item is).
	if rule.Secret != nil {
		restrictive := rule.Action == "deny" || rule.Action == "ignore"
		if !matchSecret(rule.Secret, items, restrictive) {
			return false
		}
	}

	// Check search_attributes
	if len(rule.SearchAttributes) > 0 {
		if !attributesMatch(rule.SearchAttributes, searchAttrs) {
			return false
		}
	}

	return true
}

// matchProcess checks if the sender matches the process matcher.
// At least one non-empty field must be set, and all non-empty fields must match.
func matchProcess(pm *ProcessMatcher, senderInfo SenderInfo) bool {
	if pm.Exe != "" {
		matched := false
		for _, proc := range senderInfo.ProcessChain {
			if ok, _ := path.Match(pm.Exe, proc.Exe); ok {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	if pm.Name != "" {
		matched := false
		for _, proc := range senderInfo.ProcessChain {
			if ok, _ := path.Match(pm.Name, proc.Name); ok {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	if pm.CWD != "" {
		matched := false
		for _, proc := range senderInfo.ProcessChain {
			if ok, _ := path.Match(pm.CWD, proc.CWD); ok {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	if pm.Unit != "" {
		if ok, _ := path.Match(pm.Unit, senderInfo.UnitName); !ok {
			return false
		}
	}

	return true
}

// matchSecret checks whether a batch of items matches the secret matcher.
//
// A single approval decision covers the whole batch, so every item must be
// considered — never just items[0]. The quantifier depends on the rule's
// polarity so that both directions fail closed:
//
//   - restrictive rules (deny/ignore) match if ANY item is in scope, so a deny
//     scoped to a sensitive collection still fires when a batch smuggles that
//     item alongside benign ones.
//   - permissive rules (approve) match only if EVERY item is in scope, so an
//     approve scoped to a low-value collection cannot silently authorize a batch
//     that also pulls out-of-scope secrets.
//
// A matcher with no constraints matches any batch (including the empty batch);
// a constrained matcher never matches an empty batch, since no item is in scope.
func matchSecret(sm *SecretMatcher, items []ItemInfo, restrictive bool) bool {
	if sm.Collection == "" && sm.Label == "" && len(sm.Attributes) == 0 {
		return true
	}
	if restrictive {
		return slices.ContainsFunc(items, func(it ItemInfo) bool { return matchSecretItem(sm, it) })
	}
	if len(items) == 0 {
		return false
	}
	for _, it := range items {
		if !matchSecretItem(sm, it) {
			return false
		}
	}
	return true
}

// matchSecretItem checks whether a single item matches the secret matcher.
func matchSecretItem(sm *SecretMatcher, item ItemInfo) bool {
	if sm.Collection != "" {
		if ok, _ := path.Match(sm.Collection, extractCollection(item.Path)); !ok {
			return false
		}
	}
	if sm.Label != "" {
		if ok, _ := path.Match(sm.Label, item.Label); !ok {
			return false
		}
	}
	if len(sm.Attributes) > 0 {
		attrs := item.Attributes
		if attrs == nil {
			attrs = map[string]string{}
		}
		if !attributesMatch(sm.Attributes, attrs) {
			return false
		}
	}
	return true
}

// ListTrustRules returns the configured trust rules.
func (m *Manager) ListTrustRules() []TrustRule {
	return m.trustRules
}

// chromeDummySchema is the xdg:schema Chrome uses for dummy keyring-unlock probes.
const chromeDummySchema = "_chrome_dummy_schema_for_unlocking"

// ShouldIgnore returns true when the request is a Chrome dummy secret write
// that should be silently dropped.
func (m *Manager) ShouldIgnore(items []ItemInfo, reqType RequestType) bool {
	if !m.ignoreChromeDummy || reqType != RequestTypeWrite {
		return false
	}
	for _, item := range items {
		if item.Attributes["xdg:schema"] == chromeDummySchema {
			return true
		}
	}
	return false
}

// RecordPassthrough creates a history entry for a request that bypasses approval entirely.
// Used for methods like SearchItems and Unlock that are proxied directly to the upstream.
func (m *Manager) RecordPassthrough(client string, items []ItemInfo, session string,
	reqType RequestType, searchAttrs map[string]string, senderInfo SenderInfo) {
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
}

// RecordIgnored creates a history entry for an ignored request without blocking.
func (m *Manager) RecordIgnored(client string, items []ItemInfo, session string, senderInfo SenderInfo) {
	now := time.Now()
	req := &Request{
		ID:         uuid.New().String(),
		Client:     client,
		Items:      items,
		Session:    session,
		CreatedAt:  now,
		ExpiresAt:  now,
		Type:       RequestTypeWrite,
		SenderInfo: senderInfo,
	}
	m.notify(Event{Type: EventRequestIgnored, Request: req})
}

// RecordDenied creates a history entry for a request denied by a trust rule.
func (m *Manager) RecordDenied(client string, items []ItemInfo, session string,
	reqType RequestType, searchAttrs map[string]string, senderInfo SenderInfo) {
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
	m.notify(Event{Type: EventRequestDenied, Request: req})
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
