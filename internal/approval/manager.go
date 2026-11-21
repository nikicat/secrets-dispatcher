// Package approval manages pending secret access requests requiring user approval.
package approval

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/google/uuid"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/procutil"
	"github.com/nikicat/secrets-dispatcher/internal/sender"
)

// Reports a request that the user denied, or that Manager.Cancel ended.
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
	// Set for EventAutoApproveRuleAdded and EventAutoApproveRuleRemoved only.
	Rule *AutoApproveRule
	// The History entry of the request that this event ends. It is nil when the
	// event ends no request.
	History *HistoryEntry
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

// Returns the collection that the item's path names. The name may be an alias
// such as "default". Returns "" when the path names no collection.
func (i ItemInfo) Collection() string {
	return dbustypes.ExtractCollection(i.Path)
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

// Checks whether a recent approval of the same items may answer a request of
// type t. Delete and write requests always ask, since each one changes the
// keyring.
func (t RequestType) cacheable() bool {
	return t != RequestTypeDelete && t != RequestTypeWrite
}

// What a caller knows about one secret access request before the manager
// decides on it.
type RequestInfo struct {
	Items   []ItemInfo
	Session dbus.ObjectPath
	Type    RequestType
	// Set for search requests only.
	SearchAttributes map[string]string
	SenderInfo       SenderInfo
	// Set for gpg_sign requests only.
	GPGSignInfo *GPGSignInfo
}

// Returns a new record of the request. The record is for a request that was
// settled as soon as it arrived, so its ExpiresAt equals its CreatedAt.
func (info RequestInfo) newRecord() *Request {
	now := time.Now()
	return &Request{
		ID:               uuid.New().String(),
		Items:            info.Items,
		Session:          info.Session,
		CreatedAt:        now,
		ExpiresAt:        now,
		Type:             info.Type,
		SearchAttributes: info.SearchAttributes,
		SenderInfo:       info.SenderInfo,
		GPGSignInfo:      info.GPGSignInfo,
	}
}

// A secret access request. The Manager holds it as pending while it waits for
// the user, and History keeps it once it is settled.
type Request struct {
	ID        string          `json:"id"`
	Items     []ItemInfo      `json:"items"`
	Session   dbus.ObjectPath `json:"session"`
	CreatedAt time.Time       `json:"created_at"`
	ExpiresAt time.Time       `json:"expires_at"`

	Type RequestType `json:"type"`

	// SearchAttributes contains the search criteria for search requests.
	SearchAttributes map[string]string `json:"search_attributes,omitempty"`

	// SenderInfo contains information about the requesting process.
	SenderInfo SenderInfo `json:"sender_info"`

	// GPGSignInfo contains signing context for gpg_sign requests; nil for other types.
	GPGSignInfo *GPGSignInfo `json:"gpg_sign_info,omitempty"`

	// What gpg produced for a gpg_sign request. The thin client receives it once
	// the request is approved.
	GPGResult GPGResult `json:"-"`
}

// A request that waits for the user's decision. It ends exactly once, through
// Manager.resolve.
type pendingRequest struct {
	req *Request
	// Closed when the request ends, after the outcome field is set.
	done    chan struct{}
	outcome HistoryEntry
	// Ends the request with ResolutionExpired at req.ExpiresAt.
	expiry *time.Timer
}

// Checks whether an auto-approve rule made from this request could match a
// later one.
// Rules match only on the invoker's executable (ADR 0005). A request that came
// over a tunnel, or whose invoker was not resolved, has none.
func (r *Request) AutoApprovable() bool {
	_, ok := r.SenderInfo.invokerExe()
	return ok
}

// Returns a temporary rule that auto-approves requests like this one (ADR 0004)
// once Manager.AddAutoApproveRule installs it. Errors with ErrNotAutoApprovable
// when the request is not AutoApprovable.
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
		rule.Collection = r.Items[0].Collection()
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

var resolutions = []Resolution{
	ResolutionApproved, ResolutionDenied, ResolutionExpired,
	ResolutionCancelled, ResolutionAutoApproved, ResolutionIgnored,
}

// Checks whether the request was approved, by the user or by a rule.
func (r Resolution) Approves() bool {
	return r == ResolutionApproved || r == ResolutionAutoApproved
}

// Returns nil when the resolution approves the request, ErrTimeout when the
// request expired, and ErrDenied otherwise.
func (r Resolution) err() error {
	switch {
	case r.Approves():
		return nil
	case r == ResolutionExpired:
		return ErrTimeout
	}
	return ErrDenied
}

// Returns the type of the event that tells observers a request ended with this
// resolution. Panics when the resolution is not one of the Resolution constants.
func (r Resolution) event() EventType {
	switch r {
	case ResolutionApproved:
		return EventRequestApproved
	case ResolutionDenied:
		return EventRequestDenied
	case ResolutionExpired:
		return EventRequestExpired
	case ResolutionCancelled:
		return EventRequestCancelled
	case ResolutionAutoApproved:
		return EventRequestAutoApproved
	case ResolutionIgnored:
		return EventRequestIgnored
	}
	panic(fmt.Sprintf("approval: unknown resolution %q", r))
}

// Errors when the text is not one of the Resolution constants.
func (r *Resolution) UnmarshalText(text []byte) error {
	v := Resolution(text)
	if !slices.Contains(resolutions, v) {
		return fmt.Errorf("unknown resolution %q", text)
	}
	*r = v
	return nil
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

func (r AutoApproveRule) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("rule_id", r.ID),
		slog.String("invoker", r.InvokerName),
		slog.Any("type", r.RequestType),
		slog.String("collection", r.Collection),
		slog.Time("expires_at", r.ExpiresAt),
	)
}

// Checks whether the rule has expired by the given time.
func (r AutoApproveRule) expired(now time.Time) bool {
	return !r.ExpiresAt.After(now)
}

// Checks whether the rule approves the given request. Like an approve trust
// rule, it must cover every item of the request (ADR 0005). A request with no
// items matches only a rule with no collection and no attributes.
func (r AutoApproveRule) matches(info RequestInfo) bool {
	exe, ok := info.SenderInfo.invokerExe()
	if !ok || r.InvokerExe != exe || r.RequestType != info.Type {
		return false
	}
	if len(info.Items) == 0 {
		return r.Collection == "" && len(r.Attributes) == 0
	}
	for _, it := range info.Items {
		if r.Collection != "" && r.Collection != it.Collection() {
			return false
		}
		if !attributesContain(it.Attributes, r.Attributes) {
			return false
		}
	}
	return true
}

// The default lifetime of an auto-approve rule (ADR 0004).
const DefaultAutoApproveDuration = 2 * time.Minute

// Manager tracks pending approval requests and handles blocking until decision.
type Manager struct {
	mu      sync.RWMutex
	pending map[string]*pendingRequest
	timeout time.Duration
	// True if every request is approved without asking. False otherwise.
	disabled bool

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
	trustRules          []TrustRule
}

// ManagerConfig holds configuration for the approval Manager.
type ManagerConfig struct {
	// Timeout is how long a request waits for user approval before expiring.
	Timeout time.Duration
	// HistoryMax is the maximum number of resolved requests kept in history.
	HistoryMax int
	// How long the user's approval of an item also approves later requests from
	// the same sender for that item. Zero turns this off.
	ApprovalWindow time.Duration
	// How long an auto-approve rule lasts after it is added. Zero means
	// DefaultAutoApproveDuration.
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
		pending:             make(map[string]*pendingRequest),
		timeout:             cfg.Timeout,
		observers:           make(map[Observer]struct{}),
		historyMax:          cfg.HistoryMax,
		approvalWindow:      cfg.ApprovalWindow,
		cache:               make(map[approvalCacheKey]time.Time),
		autoApproveDuration: cmp.Or(cfg.AutoApproveDuration, DefaultAutoApproveDuration),
		trustedSigners:      cfg.TrustedSigners,
		ignoreChromeDummy:   cfg.IgnoreChromeDummy,
		trustRules:          cfg.TrustRules,
	}
}

// NewDisabledManager creates a manager that auto-approves all requests.
func NewDisabledManager() *Manager {
	return &Manager{
		pending:             make(map[string]*pendingRequest),
		disabled:            true,
		observers:           make(map[Observer]struct{}),
		historyMax:          100,
		autoApproveDuration: DefaultAutoApproveDuration,
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

// Returns the event that tells observers the given request ended with the
// given resolution. The event carries the History entry that records the end.
func endEvent(res Resolution, req *Request) Event {
	return Event{
		Type:    res.event(),
		Request: req,
		History: &HistoryEntry{Request: req, Resolution: res, ResolvedAt: time.Now()},
	}
}

// Records the History entry that the given event carries, if any, and delivers
// the event to every observer in the caller's goroutine.
func (m *Manager) notify(event Event) {
	if event.History != nil {
		m.pushHistory(*event.History)
	}

	m.observersMu.RLock()
	defer m.observersMu.RUnlock()
	for o := range m.observers {
		o.OnEvent(event)
	}
}

// Puts the given entry first in History, and keeps at most historyMax entries
// by dropping the oldest.
func (m *Manager) pushHistory(entry HistoryEntry) {
	m.historyMu.Lock()
	defer m.historyMu.Unlock()
	m.history = append([]HistoryEntry{entry}, m.history...)
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
	i := slices.IndexFunc(m.history, func(e HistoryEntry) bool { return e.Request.ID == requestID })
	if i < 0 {
		return nil
	}
	return &m.history[i]
}

// AddHistoryEntry adds an entry directly to history. For testing only.
func (m *Manager) AddHistoryEntry(entry HistoryEntry) {
	m.pushHistory(entry)
}

// Blocks until the user decides, unless the approval cache or a rule decides
// first (ADR 0004). The prompted result is true when the user approved at a
// prompt. Errors with ErrDenied, ErrTimeout, ErrDeniedByRule or ErrIgnored,
// and with the context's error when the context ends.
func (m *Manager) RequireApproval(ctx context.Context, info RequestInfo) (prompted bool, err error) {
	if m.disabled {
		return false, nil
	}

	if m.cachedApproval(info) {
		return false, nil
	}

	if rule := m.CheckAutoApproveRules(info); rule != nil {
		slog.Info("auto-approve rule matched", "rule", *rule)
		m.record(ResolutionAutoApproved, info)
		return false, nil
	}

	if rule := m.CheckTrustRules(info); rule != nil {
		slog.Info("trust rule matched",
			"rule_name", rule.Name,
			"action", rule.Action)
		res, err := rule.Action.outcome()
		m.record(res, info)
		return false, err
	}

	entry := m.await(ctx, info)
	if entry.Resolution == ResolutionCancelled && ctx.Err() != nil {
		return false, ctx.Err()
	}
	return entry.Resolution.Approves(), entry.Resolution.err()
}

// Adds the request as pending, waits until it ends, and returns the History
// entry of its end. Ending the context cancels the request. The entry is then
// ResolutionCancelled, unless the request ended another way first.
func (m *Manager) await(ctx context.Context, info RequestInfo) HistoryEntry {
	p := m.addPending(info)
	select {
	case <-p.done:
	case <-ctx.Done():
		m.Cancel(p.req.ID) //nolint:errcheck // resolved meanwhile is fine
		<-p.done
	}
	return p.outcome
}

// Adds the request as pending, tells observers it was created, and returns it
// without waiting. It expires after the manager's timeout unless it is resolved
// first (ADR 0004).
func (m *Manager) addPending(info RequestInfo) *pendingRequest {
	req := info.newRecord()
	req.ExpiresAt = req.CreatedAt.Add(m.timeout)
	p := &pendingRequest{req: req, done: make(chan struct{})}

	m.mu.Lock()
	m.pending[req.ID] = p
	m.mu.Unlock()

	m.notify(Event{Type: EventRequestCreated, Request: req})

	// Armed after the created event, so that observers never see the request
	// expire before they see it created.
	m.mu.Lock()
	if _, ok := m.pending[req.ID]; ok {
		p.expiry = time.AfterFunc(time.Until(req.ExpiresAt), func() {
			m.resolve(req.ID, ResolutionExpired, nil) //nolint:errcheck // resolved meanwhile is fine
		})
	}
	m.mu.Unlock()
	return p
}

// Ends the pending request with the given id and resolution, and tells
// observers. Errors with ErrNotFound when no such request is pending, and with
// the admit function's error, which leaves the request pending.
func (m *Manager) resolve(id string, res Resolution, admit func(*Request) error) (*Request, error) {
	m.mu.Lock()
	p, ok := m.pending[id]
	if !ok {
		m.mu.Unlock()
		return nil, ErrNotFound
	}
	if admit != nil {
		if err := admit(p.req); err != nil {
			m.mu.Unlock()
			return nil, err
		}
	}
	delete(m.pending, id)
	if p.expiry != nil {
		p.expiry.Stop()
	}
	event := endEvent(res, p.req)
	p.outcome = *event.History
	close(p.done)
	m.mu.Unlock()

	m.notify(event)
	return p.req, nil
}

// List returns all pending requests.
func (m *Manager) List() []*Request {
	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()
	result := make([]*Request, 0, len(m.pending))
	for _, p := range m.pending {
		if p.req.ExpiresAt.After(now) {
			result = append(result, p.req)
		}
	}
	return result
}

// Approves the pending request with the given id and caches the approval of its
// items. Errors with ErrNotFound when no such request is pending.
func (m *Manager) Approve(id string) error {
	req, err := m.resolve(id, ResolutionApproved, nil)
	if err != nil {
		return err
	}
	m.cacheApproval(req)
	return nil
}

// Approves the pending request with the given id, as Approve does, and adds an
// auto-approve rule for later requests like it (ADR 0004). Errors with
// ErrNotFound when no such request is pending, and with ErrNotAutoApprovable
// when it is not AutoApprovable. In that case the request stays pending.
func (m *Manager) ApproveAndAutoApprove(id string) error {
	var rule AutoApproveRule
	req, err := m.resolve(id, ResolutionApproved, func(req *Request) (err error) {
		rule, err = req.AutoApproveRule()
		return err
	})
	if err != nil {
		return err
	}
	m.cacheApproval(req)
	m.AddAutoApproveRule(rule)
	return nil
}

// Ends the pending request with the given id as denied by the user. Cancel ends
// it with no decision. Errors with ErrNotFound when no such request is pending.
func (m *Manager) Deny(id string) error {
	_, err := m.resolve(id, ResolutionDenied, nil)
	return err
}

// Ends the pending request with the given id as cancelled, recording no decision
// by the user. Errors with ErrNotFound when no such request is pending.
func (m *Manager) Cancel(id string) error {
	_, err := m.resolve(id, ResolutionCancelled, nil)
	return err
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
func (m *Manager) GetPending(id string) *Request {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if p, ok := m.pending[id]; ok {
		return p.req
	}
	return nil
}

// Approves a pending gpg_sign request with what gpg produced. A failed gpg run
// is approved too, and the thin client then exits with its ExitCode.
// Errors with ErrNotFound when no request with that id is pending.
func (m *Manager) ApproveGPGSign(id string, res GPGResult) error {
	_, err := m.resolve(id, ResolutionApproved, func(req *Request) error {
		req.GPGResult = res
		return nil
	})
	return err
}

// Identifies an item approved for a sender. Each bus numbers its own senders,
// so the client is part of the key.
type approvalCacheKey struct {
	client   ClientName
	sender   sender.UniqueName
	itemPath string
}

// Returns the cache key for an approval of the item at the given path for this
// sender. Reports false when the sender has no unique bus name, since another
// process could then reuse the approval (ADR 0004).
func (s SenderInfo) cacheKey(itemPath string) (approvalCacheKey, bool) {
	if s.Sender == nil {
		return approvalCacheKey{}, false
	}
	return approvalCacheKey{s.Client, *s.Sender, itemPath}, true
}

func (m *Manager) cachesApprovals() bool {
	return m.approvalWindow > 0
}

// Checks whether every item of the given request was recently approved for the
// same sender (ADR 0004). A request with no items, or of a type that is not
// cacheable, is never answered from the cache.
func (m *Manager) cachedApproval(info RequestInfo) bool {
	if !m.cachesApprovals() || !info.Type.cacheable() || len(info.Items) == 0 {
		return false
	}
	now := time.Now()
	cutoff := now.Add(-m.approvalWindow)

	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()

	for _, item := range info.Items {
		key, ok := info.SenderInfo.cacheKey(item.Path)
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

// Caches an approval of the item at the given path for the given sender, as if
// the user had approved it.
func (m *Manager) CacheItemForSender(senderInfo SenderInfo, itemPath string) {
	if !m.cachesApprovals() {
		return
	}

	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()

	if key, ok := senderInfo.cacheKey(itemPath); ok {
		m.cache[key] = time.Now()
	}
}

// Claims the given client name for a bus. Reports false when another bus
// already holds the name, so that one name never stands for two buses while
// approvals for it may be cached.
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

// Releases the given client name after its bus has stopped, and forgets the
// approvals cached for that name, because the next bus to claim it numbers its
// senders afresh.
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

func (m *Manager) AutoApproveDuration() time.Duration {
	return m.autoApproveDuration
}

func (r AutoApproveRule) sameScope(o AutoApproveRule) bool {
	return r.InvokerExe == o.InvokerExe &&
		r.RequestType == o.RequestType &&
		r.Collection == o.Collection &&
		maps.Equal(r.Attributes, o.Attributes)
}

// Adds the given rule for the duration that Manager.AutoApproveDuration
// returns, and returns its ID. When a rule with the same scope exists, extends
// that rule's expiry instead and returns its ID.
func (m *Manager) AddAutoApproveRule(rule AutoApproveRule) string {
	rule.ExpiresAt = time.Now().Add(m.AutoApproveDuration())

	msg := "auto-approve rule added"
	m.autoApproveMu.Lock()
	if i := slices.IndexFunc(m.autoApproveRules, rule.sameScope); i >= 0 {
		m.autoApproveRules[i].ExpiresAt = rule.ExpiresAt
		rule = m.autoApproveRules[i]
		msg = "auto-approve rule refreshed"
	} else {
		m.autoApproveRules = append(m.autoApproveRules, rule)
	}
	m.autoApproveMu.Unlock()

	m.notify(Event{Type: EventAutoApproveRuleAdded, Rule: &rule})
	slog.Info(msg, "rule", rule)
	return rule.ID
}

// Returns a copy of the first active auto-approve rule that matches the
// request, or nil.
func (m *Manager) CheckAutoApproveRules(info RequestInfo) *AutoApproveRule {
	m.autoApproveMu.Lock()
	defer m.autoApproveMu.Unlock()

	rules := m.activeRules()
	i := slices.IndexFunc(rules, func(r AutoApproveRule) bool { return r.matches(info) })
	if i < 0 {
		return nil
	}
	match := rules[i]
	return &match
}

// Drops the expired auto-approve rules and returns the ones still active. The
// caller holds autoApproveMu, and the result is valid only while it does.
func (m *Manager) activeRules() []AutoApproveRule {
	now := time.Now()
	m.autoApproveRules = slices.DeleteFunc(m.autoApproveRules, func(r AutoApproveRule) bool {
		return r.expired(now)
	})
	return m.autoApproveRules
}

// Checks whether the first map holds every key of the second with the same
// value. A missing key reads as the empty string.
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

	return slices.Clone(m.activeRules())
}

// ListTrustedSigners returns the configured trusted signers.
func (m *Manager) ListTrustedSigners() []TrustedSigner {
	return m.trustedSigners
}

// RemoveAutoApproveRule removes an auto-approve rule by ID.
func (m *Manager) RemoveAutoApproveRule(id string) error {
	m.autoApproveMu.Lock()
	i := slices.IndexFunc(m.autoApproveRules, func(r AutoApproveRule) bool { return r.ID == id })
	if i < 0 {
		m.autoApproveMu.Unlock()
		return ErrNotFound
	}
	removed := m.autoApproveRules[i]
	m.autoApproveRules = slices.Delete(m.autoApproveRules, i, i+1)
	m.autoApproveMu.Unlock()

	m.notify(Event{Type: EventAutoApproveRuleRemoved, Rule: &removed})
	slog.Info("auto-approve rule removed", "rule", removed)
	return nil
}

// Checks whether a trusted_signers entry lets a git signing request skip the
// prompt. A request that did not come through the thin client never skips it,
// since its repo name and changed files may be forged (ADR 0008).
func (m *Manager) CheckTrustedSigner(info RequestInfo) bool {
	if info.GPGSignInfo == nil || !info.SenderInfo.PeerTrusted {
		return false
	}
	return slices.ContainsFunc(m.trustedSigners, func(ts TrustedSigner) bool {
		return ts.matches(info.SenderInfo.ProcessChain, info.GPGSignInfo.RepoName, info.GPGSignInfo.ChangedFiles)
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
	i := slices.IndexFunc(m.trustRules, func(r TrustRule) bool { return r.matches(info) })
	if i < 0 {
		return nil
	}
	return &m.trustRules[i]
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
	return slices.ContainsFunc(info.Items, func(item ItemInfo) bool {
		return item.Attributes["xdg:schema"] == chromeDummySchema
	})
}

// Records a request that went upstream without approval, such as SearchItems
// or Unlock, as auto-approved.
func (m *Manager) RecordPassthrough(info RequestInfo) {
	m.record(ResolutionAutoApproved, info)
}

// Records a request that was dropped without asking the user.
func (m *Manager) RecordIgnored(info RequestInfo) {
	m.record(ResolutionIgnored, info)
}

// Records a request that a trust rule denied.
func (m *Manager) RecordDenied(info RequestInfo) {
	m.record(ResolutionDenied, info)
}

// Notifies subscribers of a request that was settled without waiting for the
// user.
func (m *Manager) record(res Resolution, info RequestInfo) {
	m.notify(endEvent(res, info.newRecord()))
}

// cacheApproval records approved (sender, item) pairs in the cache.
func (m *Manager) cacheApproval(req *Request) {
	if !m.cachesApprovals() {
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
