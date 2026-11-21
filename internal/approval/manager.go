// Package approval manages pending secret access requests requiring user approval.
package approval

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/google/uuid"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/procutil"
	"github.com/nikicat/secrets-dispatcher/internal/sender"
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

// Reports a request that Request.AutoApprovable rejects, so no auto-approve
// rule is made from it.
var ErrNotAutoApprovable = errors.New("request has no local executable for an auto-approve rule to match")

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

// What a caller knows about one secret access request before the manager
// decides on it.
type RequestInfo struct {
	Items   []ItemInfo
	Session dbus.ObjectPath
	Type    RequestType
	// Set for search requests only.
	SearchAttributes map[string]string
	SenderInfo       SenderInfo
}

// Returns a new Request for info, created now and expiring after ttl.
func (info RequestInfo) newRequest(ttl time.Duration) *Request {
	now := time.Now()
	return &Request{
		ID:               uuid.New().String(),
		Client:           info.SenderInfo.Client,
		Items:            info.Items,
		Session:          info.Session,
		CreatedAt:        now,
		ExpiresAt:        now.Add(ttl),
		Type:             info.Type,
		SearchAttributes: info.SearchAttributes,
		SenderInfo:       info.SenderInfo,
	}
}

// Request represents a secret access request awaiting approval.
type Request struct {
	ID        string          `json:"id"`
	Client    ClientName      `json:"client"`
	Items     []ItemInfo      `json:"items"`
	Session   dbus.ObjectPath `json:"session"`
	CreatedAt time.Time       `json:"created_at"`
	ExpiresAt time.Time       `json:"expires_at"`

	// Type indicates whether this is a get_secret or search request.
	Type RequestType `json:"type"`

	// SearchAttributes contains the search criteria for search requests.
	SearchAttributes map[string]string `json:"search_attributes,omitempty"`

	// SenderInfo contains information about the requesting process.
	SenderInfo SenderInfo `json:"sender_info"`

	// GPGSignInfo contains signing context for gpg_sign requests; nil for other types.
	GPGSignInfo *GPGSignInfo `json:"gpg_sign_info,omitempty"`

	// Handed back to the thin client once a gpg_sign request is approved. Set by
	// ApproveGPGSign and RecordAutoApprovedGPGSign.
	GPGResult GPGResult `json:"-"`

	// Internal: channel closed when the request is resolved
	done       chan struct{}
	resolution Resolution // set before done is closed
}

// Checks whether an auto-approve rule made from r could match a later request.
// Rules match only on the invoker's executable (ADR 0005), and a request that
// came over a tunnel, or whose invoker was not resolved, has none.
func (r *Request) AutoApprovable() bool {
	_, ok := r.SenderInfo.invokerExe()
	return ok
}

// Returns a temporary rule that auto-approves requests like r (ADR 0004) once
// Manager.AddAutoApproveRule installs it. Errors with ErrNotAutoApprovable when
// r is not AutoApprovable.
func (r *Request) AutoApproveRule() (AutoApproveRule, error) {
	exe, ok := r.SenderInfo.invokerExe()
	if !ok {
		return AutoApproveRule{}, ErrNotAutoApprovable
	}
	rule := AutoApproveRule{
		ID:          uuid.New().String(),
		InvokerName: r.SenderInfo.InvokerName,
		InvokerExe:  exe,
		RequestType: r.Type,
	}

	if len(r.Items) > 0 {
		rule.Collection = extractCollection(r.Items[0].Path)
		rule.Attributes = r.Items[0].Attributes
	}
	if r.Type == RequestTypeSearch && len(r.SearchAttributes) > 0 {
		rule.Attributes = r.SearchAttributes
	}
	return rule, nil
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

// Checks whether the request was approved, by the user or by a rule.
func (r Resolution) Approves() bool {
	return r == ResolutionApproved || r == ResolutionAutoApproved
}

// HistoryEntry represents a resolved approval request.
type HistoryEntry struct {
	Request    *Request   `json:"request"`
	Resolution Resolution `json:"resolution"`
	ResolvedAt time.Time  `json:"resolved_at"`
}

// A temporary rule that auto-approves requests like one the user approved
// (ADR 0004). InvokerName is for display only.
type AutoApproveRule struct {
	ID          string            `json:"id"`
	InvokerName string            `json:"invoker_name"`
	InvokerExe  procutil.Path     `json:"invoker_exe,omitempty"`
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
	cache          map[approvalCacheKey]time.Time
	clients        map[ClientName]bool // names held by running buses

	autoApproveMu       sync.Mutex
	autoApproveRules    []AutoApproveRule
	autoApproveDuration time.Duration
	trustedSigners      []TrustedSigner
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
	TrustedSigners      []TrustedSigner
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
		cache:               make(map[approvalCacheKey]time.Time),
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

// Returns how a request ends when this event fires, or false for an event that
// ends none.
func (t EventType) Resolution() (Resolution, bool) {
	switch t {
	case EventRequestApproved:
		return ResolutionApproved, true
	case EventRequestDenied:
		return ResolutionDenied, true
	case EventRequestExpired:
		return ResolutionExpired, true
	case EventRequestCancelled:
		return ResolutionCancelled, true
	case EventRequestAutoApproved:
		return ResolutionAutoApproved, true
	case EventRequestIgnored:
		return ResolutionIgnored, true
	}
	return "", false
}

// addHistory records a resolved request in history.
func (m *Manager) addHistory(event Event) {
	resolution, ok := event.Type.Resolution()
	if !ok {
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

// Blocks until the user decides, unless the approval cache or a rule decides
// first (ADR 0004). prompted is true when the user approved at a prompt.
// Errors with ErrDenied, ErrTimeout, ErrDeniedByRule or ErrIgnored.
func (m *Manager) RequireApproval(ctx context.Context, info RequestInfo) (prompted bool, err error) {
	if m.disabled {
		return false, nil
	}

	// Check approval cache: if all items were recently approved for this sender, skip.
	// Delete and write requests always require explicit approval — never use cached approvals.
	if info.Type != RequestTypeDelete && info.Type != RequestTypeWrite && m.approvalWindow > 0 && len(info.Items) > 0 {
		if m.checkApprovalCache(info.SenderInfo, info.Items) {
			return false, nil
		}
	}

	// Check auto-approve rules (for timed-out client retries).
	if rule := m.checkAutoApproveRules(info); rule != nil {
		slog.Info("auto-approve rule matched",
			"rule_id", rule.ID,
			"invoker", rule.InvokerName,
			"type", rule.RequestType)
		m.record(EventRequestAutoApproved, info)
		return false, nil
	}

	// Check persistent trust rules from config.
	if rule := m.CheckTrustRules(info); rule != nil {
		slog.Info("trust rule matched",
			"rule_name", rule.Name,
			"action", rule.Action)
		switch rule.Action {
		case RuleIgnore:
			m.record(EventRequestIgnored, info)
			return false, ErrIgnored
		case RuleDeny:
			m.record(EventRequestDenied, info)
			return false, ErrDeniedByRule
		}
		m.record(EventRequestAutoApproved, info)
		return false, nil
	}

	req := info.newRequest(m.timeout)
	req.done = make(chan struct{})

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
		if req.resolution.Approves() {
			return true, nil
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

	req.resolution = ResolutionApproved
	delete(m.pending, id)
	close(req.done)
	m.mu.Unlock()

	m.notify(Event{Type: EventRequestApproved, Request: req})
	m.cacheApproval(req)
	return nil
}

// Approves the pending request with that id, as Approve does, and adds an
// auto-approve rule for later requests like it (ADR 0004). Errors with
// ErrNotFound when no such request is pending, and with ErrNotAutoApprovable
// when it is not AutoApprovable. The request then stays pending.
func (m *Manager) ApproveAndAutoApprove(id string) error {
	m.mu.Lock()
	req, ok := m.pending[id]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	rule, err := req.AutoApproveRule()
	if err != nil {
		m.mu.Unlock()
		return err
	}

	req.resolution = ResolutionApproved
	delete(m.pending, id)
	close(req.done)
	m.mu.Unlock()

	m.notify(Event{Type: EventRequestApproved, Request: req})
	m.cacheApproval(req)
	m.AddAutoApproveRule(rule)
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

	req.resolution = ResolutionDenied
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

	req.resolution = ResolutionCancelled
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

// Approves a pending gpg_sign request with what gpg produced. A failed gpg run
// is approved too, and the thin client then exits with its ExitCode.
// Errors with ErrNotFound when no request with that id is pending.
func (m *Manager) ApproveGPGSign(id string, res GPGResult) error {
	m.mu.Lock()
	req, ok := m.pending[id]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}

	req.GPGResult = res
	req.resolution = ResolutionApproved
	delete(m.pending, id)
	close(req.done)
	m.mu.Unlock()

	m.notify(Event{Type: EventRequestApproved, Request: req})
	return nil
}

// Identifies an item approved for a sender. Each bus numbers its own senders,
// so the client is part of the key.
type approvalCacheKey struct {
	client   ClientName
	sender   sender.UniqueName
	itemPath string
}

// Returns the key that caches an approval of itemPath for s. Reports false
// when s has no sender, since another process could then reuse the approval
// (ADR 0004).
func (s SenderInfo) cacheKey(itemPath string) (approvalCacheKey, bool) {
	if s.Sender == nil {
		return approvalCacheKey{}, false
	}
	return approvalCacheKey{s.Client, *s.Sender, itemPath}, true
}

// checkApprovalCache returns true if ALL items have a valid cache entry for the sender.
// Expired entries encountered during the check are lazily deleted.
func (m *Manager) checkApprovalCache(senderInfo SenderInfo, items []ItemInfo) bool {
	if m.approvalWindow <= 0 {
		return false
	}
	now := time.Now()
	cutoff := now.Add(-m.approvalWindow)

	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()

	for _, item := range items {
		key, ok := senderInfo.cacheKey(item.Path)
		if !ok {
			return false
		}
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
func (m *Manager) CacheItemForSender(senderInfo SenderInfo, itemPath string) {
	if m.approvalWindow <= 0 || m.disabled {
		return
	}

	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()

	if key, ok := senderInfo.cacheKey(itemPath); ok {
		m.cache[key] = time.Now()
	}
}

// Takes name for a bus and reports whether no other bus held it. One name never
// stands for two buses while approvals for it can be cached.
func (m *Manager) ClaimClient(name ClientName) bool {
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	if m.clients[name] {
		return false
	}
	if m.clients == nil {
		m.clients = map[ClientName]bool{}
	}
	m.clients[name] = true
	return true
}

// Gives name back once its bus has stopped, and forgets the approvals cached
// for it, since the next bus to claim the name numbers its senders afresh.
func (m *Manager) ReleaseClient(name ClientName) {
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	delete(m.clients, name)
	for key := range m.cache {
		if key.client == name {
			delete(m.cache, key)
		}
	}
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

func (r AutoApproveRule) sameScope(o AutoApproveRule) bool {
	return r.InvokerExe == o.InvokerExe &&
		r.RequestType == o.RequestType &&
		r.Collection == o.Collection &&
		attributesEqual(r.Attributes, o.Attributes)
}

// Adds the given rule for the duration that Manager.AutoApproveDuration
// returns, and returns its ID. When a rule with the same scope exists, extends
// that rule's expiry instead and returns its ID.
func (m *Manager) AddAutoApproveRule(rule AutoApproveRule) string {
	rule.ExpiresAt = time.Now().Add(m.AutoApproveDuration())

	m.autoApproveMu.Lock()
	for i := range m.autoApproveRules {
		existing := &m.autoApproveRules[i]
		if existing.sameScope(rule) {
			existing.ExpiresAt = rule.ExpiresAt
			refreshed := *existing
			m.autoApproveMu.Unlock()
			m.notify(Event{Type: EventAutoApproveRuleAdded, Rule: &refreshed})
			slog.Info("auto-approve rule refreshed",
				"rule_id", refreshed.ID,
				"invoker", refreshed.InvokerName,
				"expires_at", refreshed.ExpiresAt)
			return refreshed.ID
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

// Returns the active auto-approve rule that matches the request, or nil. It is
// for callers that run their own request flow, such as the gpg_sign handler.
func (m *Manager) CheckAutoApproveRules(info RequestInfo) *AutoApproveRule {
	return m.checkAutoApproveRules(info)
}

// Returns the active auto-approve rule that matches the request, or nil, and
// drops the expired rules.
func (m *Manager) checkAutoApproveRules(info RequestInfo) *AutoApproveRule {
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

		callerExe, ok := info.SenderInfo.invokerExe()
		if !ok || rule.InvokerExe != callerExe {
			continue
		}
		// Match request type
		if rule.RequestType != info.Type {
			continue
		}
		// Match collection + attributes for EVERY item in the batch. An
		// auto-approve rule is permissive, so a single decision covers the whole
		// batch only when every item falls within the rule's scope; matching just
		// items[0] would let a batch smuggle an out-of-scope secret past a rule
		// scoped to a benign collection.
		if autoApproveCoversAll(rule, info.Items) {
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
		if !attributesContain(it.Attributes, rule.Attributes) {
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

// Checks whether have holds every key of want with the same value. A missing
// key reads as the empty string.
func attributesContain(have, want map[string]string) bool {
	for k, v := range want {
		if have[k] != v {
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

// Checks whether a trusted_signers entry lets a git signing request skip the
// prompt. A request that did not come through the thin client never skips it,
// since its repoName and changedFiles may be forged (ADR 0008).
func (m *Manager) CheckTrustedSigner(senderInfo SenderInfo, repoName RepoName, changedFiles []string) bool {
	if !senderInfo.PeerTrusted {
		return false
	}
	return slices.ContainsFunc(m.trustedSigners, func(ts TrustedSigner) bool {
		return ts.matches(senderInfo.ProcessChain, repoName, changedFiles)
	})
}

// Returns the /proc/PID/exe path of the invoker. Rules match on it instead of
// InvokerName (ADR 0005). Reports false when the path is unknown.
func (s SenderInfo) invokerExe() (procutil.Path, bool) {
	for _, p := range s.ProcessChain {
		if p.PID == s.PID && p.Exe != nil {
			return *p.Exe, true
		}
	}
	return "", false
}

// Returns the first configured trust rule that matches the request, or nil.
func (m *Manager) CheckTrustRules(info RequestInfo) *TrustRule {
	for i := range m.trustRules {
		rule := &m.trustRules[i]
		if !rule.matches(info) {
			continue
		}
		return rule
	}
	return nil
}

// ListTrustRules returns the configured trust rules.
func (m *Manager) ListTrustRules() []TrustRule {
	return m.trustRules
}

// chromeDummySchema is the xdg:schema Chrome uses for dummy keyring-unlock probes.
const chromeDummySchema = "_chrome_dummy_schema_for_unlocking"

// ShouldIgnore returns true when the request is a Chrome dummy secret write
// that should be silently dropped.
func (m *Manager) ShouldIgnore(info RequestInfo) bool {
	if !m.ignoreChromeDummy || info.Type != RequestTypeWrite {
		return false
	}
	for _, item := range info.Items {
		if item.Attributes["xdg:schema"] == chromeDummySchema {
			return true
		}
	}
	return false
}

// Records a request that went upstream without approval, such as SearchItems
// or Unlock, as auto-approved.
func (m *Manager) RecordPassthrough(info RequestInfo) {
	m.record(EventRequestAutoApproved, info)
}

// Records a request that was dropped without asking the user.
func (m *Manager) RecordIgnored(info RequestInfo) {
	m.record(EventRequestIgnored, info)
}

// Records a request that a trust rule denied.
func (m *Manager) RecordDenied(info RequestInfo) {
	m.record(EventRequestDenied, info)
}

// Notifies subscribers of a request that was settled without waiting for the
// user.
func (m *Manager) record(t EventType, info RequestInfo) {
	m.notify(Event{Type: t, Request: info.newRequest(0)})
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
		if key, ok := req.SenderInfo.cacheKey(item.Path); ok {
			m.cache[key] = now
		}
	}
}
