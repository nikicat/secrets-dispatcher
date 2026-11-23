// Package approval manages pending secret access requests requiring user approval.
package approval

import (
	"cmp"
	"context"
	"encoding/json"
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
	"github.com/nikicat/secrets-dispatcher/internal/expiryset"
	"github.com/nikicat/secrets-dispatcher/internal/procutil"
	"github.com/nikicat/secrets-dispatcher/internal/sender"
)

// Reports a request that the user denied, or that Manager.Cancel ended.
var ErrDenied = errors.New("access denied by user")

// Reports a request that expired while it waited for approval.
var ErrTimeout = errors.New("approval request timed out")

// Reports an ID that names no pending request or rule.
var ErrNotFound = errors.New("not found")

// Reports a request dropped without asking the user, by an ignore trust rule
// or as a Chrome dummy secret write.
var ErrIgnored = errors.New("request ignored by trust rule")

// Reports a request that a trust rule with action "deny" denied.
var ErrDeniedByRule = errors.New("access denied by trust rule")

// Reports a request that Request.AutoApprovable rejects, so no auto-approve
// rule is made from it.
var ErrNotAutoApprovable = errors.New("request has no local executable for an auto-approve rule to match")

// Receives the manager's events, each in the goroutine that caused it.
type Observer interface {
	// Reports a request that now waits for the user.
	RequestCreated(Pending)
	// Reports a request that ended, with the entry that History records for it.
	RequestEnded(HistoryEntry)
	// Reports an auto-approve rule that was added, or whose expiry was extended.
	RuleAdded(AutoApproveRule)
	// Reports an auto-approve rule that was removed before it expired.
	RuleRemoved(AutoApproveRule)
}

type ItemInfo struct {
	Path       string
	Label      string
	Attributes dbustypes.Attributes
}

// Returns the item's label, or its path when it has no label.
func (i ItemInfo) DisplayLabel() string {
	if i.Label != "" {
		return i.Label
	}
	return i.Path
}

// Returns the collection that the item's path names, or false when it names
// none.
func (i ItemInfo) Collection() (dbustypes.CollectionRef, bool) {
	return dbustypes.CollectionOf(dbus.ObjectPath(i.Path))
}

// Returns the name of the collection that the item's path names, if any.
func (i ItemInfo) CollectionName() *dbustypes.CollectionName {
	ref, ok := i.Collection()
	if !ok {
		return nil
	}
	return &ref.Name
}

// The item attributes of an ssh_sign item: the key's fingerprint and the host
// the request is for. Trust rules can match them like any other item attribute.
const (
	sshFingerprintAttr = "fingerprint"
	sshDestinationAttr = "destination"
)

// Returns the item of an ssh_sign request for the key with the given
// fingerprint, labelled with the key's comment or else the fingerprint.
func SSHSignItem(fingerprint, comment string, destination *string) ItemInfo {
	label := comment
	if label == "" {
		label = fingerprint
	}
	attrs := dbustypes.Attributes{sshFingerprintAttr: fingerprint}
	if destination != nil {
		attrs[sshDestinationAttr] = *destination
	}
	return ItemInfo{Path: fingerprint, Label: label, Attributes: attrs}
}

// Returns the host an SSH sign request's item is for, or "" when the client
// did not say.
func (i ItemInfo) SSHDestination() string {
	return i.Attributes[sshDestinationAttr]
}

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
	Items      []ItemInfo
	Session    dbus.ObjectPath
	Type       RequestType
	SenderInfo SenderInfo
	// Nil for a type of request that carries no Payload.
	Payload Payload
}

// The data that only one type of request carries. A request's Type alone
// decides what the request is, and a payload of another type reads as no
// payload.
type Payload interface {
	// Returns the type of request that carries the payload.
	requestType() RequestType
}

// What a search request searches for.
type SearchPayload struct {
	Attributes dbustypes.Attributes
}

func (SearchPayload) requestType() RequestType { return RequestTypeSearch }

func (*GPGSignInfo) requestType() RequestType { return RequestTypeGPGSign }

// Returns the payload of type P that a request of type t carries, or false
// when it carries none.
func payloadAs[P Payload](t RequestType, p Payload) (P, bool) {
	if v, ok := p.(P); ok && v.requestType() == t {
		return v, true
	}
	var zero P
	return zero, false
}

// Returns the signing context of a gpg_sign request, or false for any other
// request.
func (info RequestInfo) GPGSign() (*GPGSignInfo, bool) {
	sign, ok := payloadAs[*GPGSignInfo](info.Type, info.Payload)
	return sign, ok && sign != nil
}

// Returns what a search request searches for, or false for any other request.
func (info RequestInfo) Search() (SearchPayload, bool) {
	return payloadAs[SearchPayload](info.Type, info.Payload)
}

// Returns a new record of the request, created now.
func (info RequestInfo) newRecord() *Request {
	return &Request{
		ID:          RequestID(uuid.New().String()),
		RequestInfo: info,
		CreatedAt:   time.Now(),
	}
}

// Identifies one approval request for its whole life.
type RequestID string

// A secret access request. The Manager holds it as pending while it waits for
// the user, and History keeps it once it is settled.
type Request struct {
	ID RequestID
	RequestInfo
	CreatedAt time.Time

	// What gpg produced. HistoryEntry.GPGOutput says when it is set.
	GPGResult GPGResult
}

// A request that waits for the user's decision.
type Pending struct {
	*Request
	// When the request ends with ResolutionExpired, unless it ends otherwise
	// first (ADR 0004).
	ExpiresAt time.Time
}

// Checks whether the request has expired by the given time.
func (p Pending) expired(now time.Time) bool {
	return !p.ExpiresAt.After(now)
}

// A pending request as the Manager holds it. It ends exactly once, through
// Manager.resolve.
type pendingRequest struct {
	Pending
	// Closed when the request ends, after the outcome field is set.
	done    chan struct{}
	outcome HistoryEntry
	// Ends the request at ExpiresAt.
	expiry *time.Timer
}

// Checks whether an auto-approve rule made from this request could match a
// later one. Rules match only on the invoker's executable (ADR 0005), which a
// tunnelled request and one with an unresolved invoker lack.
func (r *Request) AutoApprovable() bool {
	_, ok := r.SenderInfo.invokerExe()
	return ok
}

// Returns the scope of an auto-approve rule that covers requests like this one
// (ADR 0004). Errors with ErrNotAutoApprovable when the request is not
// AutoApprovable.
func (r *Request) AutoApproveScope() (AutoApproveScope, error) {
	exe, ok := r.SenderInfo.invokerExe()
	if !ok {
		return AutoApproveScope{}, ErrNotAutoApprovable
	}
	scope := AutoApproveScope{InvokerExe: exe, RequestType: r.Type}

	if len(r.Items) > 0 {
		if ref, ok := r.Items[0].Collection(); ok {
			scope.Collection = OnlyCollection(ref.Name)
		}
		scope.Attributes = r.Items[0].Attributes
	}
	if search, ok := r.Search(); ok && len(search.Attributes) > 0 {
		scope.Attributes = search.Attributes
	}
	return scope, nil
}

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

// Errors when the text is not one of the Resolution constants.
func (r *Resolution) UnmarshalText(text []byte) error {
	v := Resolution(text)
	if !slices.Contains(resolutions, v) {
		return fmt.Errorf("unknown resolution %q", text)
	}
	*r = v
	return nil
}

type HistoryEntry struct {
	Request    *Request
	Resolution Resolution
	ResolvedAt time.Time
}

// Returns the History entry that records the request as ending now with the
// given resolution.
func (r *Request) historyEntry(res Resolution) HistoryEntry {
	return HistoryEntry{Request: r, Resolution: res, ResolvedAt: time.Now()}
}

// Identifies one auto-approve rule for its whole life.
type AutoApproveRuleID string

// A temporary rule that auto-approves requests like one the user approved
// (ADR 0004). InvokerName is for display only.
type AutoApproveRule struct {
	ID          AutoApproveRuleID `json:"id"`
	InvokerName string            `json:"invoker_name"`
	AutoApproveScope
	ExpiresAt time.Time `json:"expires_at"`
}

// The requests that an auto-approve rule covers.
type AutoApproveScope struct {
	InvokerExe  procutil.Path        `json:"invoker_exe,omitempty"`
	RequestType RequestType          `json:"request_type"`
	Collection  CollectionScope      `json:"collection,omitzero"`
	Attributes  dbustypes.Attributes `json:"attributes,omitempty"`
}

func (s AutoApproveScope) Equal(o AutoApproveScope) bool {
	return s.InvokerExe == o.InvokerExe &&
		s.RequestType == o.RequestType &&
		s.Collection == o.Collection &&
		maps.Equal(s.Attributes, o.Attributes)
}

func (r AutoApproveRule) LogValue() slog.Value {
	attrs := []slog.Attr{
		slog.Any("rule_id", r.ID),
		slog.String("invoker", r.InvokerName),
		slog.Any("type", r.RequestType),
		slog.Any("collection", r.Collection),
		slog.Time("expires_at", r.ExpiresAt),
	}
	return slog.GroupValue(attrs...)
}

// Checks whether the rule has expired by the given time.
func (r AutoApproveRule) expired(now time.Time) bool {
	return !r.ExpiresAt.After(now)
}

// Checks whether the scope covers the given request. Like an approve trust
// rule, it must cover every item of the request (ADR 0005). A request with no
// items matches only a scope with no collection and no attributes.
func (s AutoApproveScope) matches(info RequestInfo) bool {
	exe, ok := info.SenderInfo.invokerExe()
	if !ok || s.InvokerExe != exe || s.RequestType != info.Type {
		return false
	}
	if len(info.Items) == 0 {
		return s.Collection == AnyCollection && len(s.Attributes) == 0
	}
	for _, it := range info.Items {
		if !s.Collection.covers(it) {
			return false
		}
		if !it.Attributes.Contain(s.Attributes) {
			return false
		}
	}
	return true
}

// The collections whose items an auto-approve rule covers. AnyCollection covers
// every item, including an item in no collection. OnlyCollection covers only
// the items of the named collection.
type CollectionScope struct {
	name dbustypes.CollectionName
	only bool
}

var AnyCollection CollectionScope

func OnlyCollection(name dbustypes.CollectionName) CollectionScope {
	return CollectionScope{name: name, only: true}
}

// Checks whether the given item lies in a collection that the scope covers.
func (c CollectionScope) covers(item ItemInfo) bool {
	if !c.only {
		return true
	}
	ref, ok := item.Collection()
	return ok && ref.Name == c.name
}

// Returns the collection name, or for AnyCollection an empty group, which slog
// leaves out.
func (c CollectionScope) LogValue() slog.Value {
	if !c.only {
		return slog.GroupValue()
	}
	return slog.StringValue(string(c.name))
}

func (c CollectionScope) MarshalJSON() ([]byte, error) {
	if !c.only {
		return []byte("null"), nil
	}
	return json.Marshal(c.name)
}

func (c *CollectionScope) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*c = AnyCollection
		return nil
	}
	var name dbustypes.CollectionName
	if err := json.Unmarshal(data, &name); err != nil {
		return err
	}
	*c = OnlyCollection(name)
	return nil
}

// The default lifetime of an auto-approve rule (ADR 0004).
const DefaultAutoApproveDuration = 2 * time.Minute

// The one approval manager that every approval surface decides through
// (ADR 0007). Each access is a separate request, apart from the short
// approval cache (ADR 0004).
type Manager struct {
	mu      sync.RWMutex
	pending map[RequestID]*pendingRequest
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
	cache          expiryset.Set[approvalCacheKey]
	clients        map[ClientName]bool // names held by running buses

	autoApproveMu       sync.Mutex
	autoApproveRules    []AutoApproveRule
	autoApproveDuration time.Duration
	trustedSigners      []TrustedSigner
	ignoreChromeDummy   bool
	trustRules          []TrustRule
}

type ManagerConfig struct {
	// How long a request waits for user approval before it expires.
	Timeout time.Duration
	// The maximum number of ended requests that History keeps.
	HistoryMax int
	// How long the user's approval of an item also approves later requests from
	// the same sender for that item. Zero turns this off.
	ApprovalWindow time.Duration
	// How long an auto-approve rule lasts after it is added. Zero means
	// DefaultAutoApproveDuration.
	AutoApproveDuration time.Duration
	TrustedSigners      []TrustedSigner
	// True if the manager drops Chrome's dummy secret writes without asking. False
	// otherwise.
	IgnoreChromeDummy bool
	// The persistent rules from the config that approve, deny or ignore requests.
	TrustRules []TrustRule
}

func NewManager(cfg ManagerConfig) *Manager {
	return &Manager{
		pending:             make(map[RequestID]*pendingRequest),
		timeout:             cfg.Timeout,
		observers:           make(map[Observer]struct{}),
		historyMax:          cfg.HistoryMax,
		approvalWindow:      cfg.ApprovalWindow,
		autoApproveDuration: cmp.Or(cfg.AutoApproveDuration, DefaultAutoApproveDuration),
		trustedSigners:      cfg.TrustedSigners,
		ignoreChromeDummy:   cfg.IgnoreChromeDummy,
		trustRules:          cfg.TrustRules,
	}
}

// Returns a manager that approves every request without asking.
func NewDisabledManager() *Manager {
	return &Manager{
		pending:             make(map[RequestID]*pendingRequest),
		disabled:            true,
		observers:           make(map[Observer]struct{}),
		historyMax:          100,
		autoApproveDuration: DefaultAutoApproveDuration,
	}
}

// Registers the given observer to receive the manager's events.
func (m *Manager) Subscribe(o Observer) {
	m.observersMu.Lock()
	defer m.observersMu.Unlock()
	m.observers[o] = struct{}{}
}

// Stops sending the manager's events to the given observer.
func (m *Manager) Unsubscribe(o Observer) {
	m.observersMu.Lock()
	defer m.observersMu.Unlock()
	delete(m.observers, o)
}

// Delivers an event to every observer in the caller's goroutine.
func (m *Manager) notify(deliver func(Observer)) {
	m.observersMu.RLock()
	defer m.observersMu.RUnlock()
	for o := range m.observers {
		deliver(o)
	}
}

// Records the entry in History, and tells observers that its request ended.
func (m *Manager) publishEnd(entry HistoryEntry) {
	m.pushHistory(entry)
	m.notify(func(o Observer) { o.RequestEnded(entry) })
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

// Returns a copy of the history entries, newest first.
func (m *Manager) History() []HistoryEntry {
	m.historyMu.RLock()
	defer m.historyMu.RUnlock()
	return append([]HistoryEntry{}, m.history...)
}

// Returns the history entry with the given request ID.
func (m *Manager) GetHistoryEntry(requestID RequestID) (HistoryEntry, bool) {
	m.historyMu.RLock()
	defer m.historyMu.RUnlock()
	i := slices.IndexFunc(m.history, func(e HistoryEntry) bool { return e.Request.ID == requestID })
	if i < 0 {
		return HistoryEntry{}, false
	}
	return m.history[i], true
}

// Adds the given entry directly to History. For testing only.
func (m *Manager) AddHistoryEntry(entry HistoryEntry) {
	m.pushHistory(entry)
}

// Blocks until the user decides, unless the approval cache or a rule decides
// first (ADR 0004). Reports whether the user approved at a prompt. Errors with
// ErrDenied, ErrTimeout, ErrDeniedByRule, ErrIgnored or the context's error.
func (m *Manager) RequireApproval(ctx context.Context, info RequestInfo) (prompted bool, err error) {
	if m.disabled {
		return false, nil
	}

	if m.dropsChromeDummyWrite(info) {
		m.record(ResolutionIgnored, info)
		return false, ErrIgnored
	}

	if m.cachedApproval(info) {
		return false, nil
	}

	if rule, ok := m.CheckAutoApproveRules(info); ok {
		slog.Info("auto-approve rule matched", "rule", rule)
		m.record(ResolutionAutoApproved, info)
		return false, nil
	}

	if rule, ok := m.CheckTrustRules(info); ok {
		return false, m.settleByRule(rule, info)
	}

	entry := m.Await(ctx, info)
	if entry.Resolution == ResolutionCancelled && ctx.Err() != nil {
		return false, ctx.Err()
	}
	return entry.Resolution.Approves(), entry.Resolution.err()
}

// Creates a pending request that expires after the manager's timeout
// (ADR 0004), and returns its ID without waiting, unlike Await.
func (m *Manager) Create(info RequestInfo) RequestID {
	return m.addPending(info).ID
}

// Adds the request as pending, waits until it ends, and returns the History
// entry of its end. Ending the context cancels the request. The entry is then
// ResolutionCancelled, unless the request ended another way first.
func (m *Manager) Await(ctx context.Context, info RequestInfo) HistoryEntry {
	p := m.addPending(info)
	select {
	case <-p.done:
	case <-ctx.Done():
		m.Cancel(p.ID) //nolint:errcheck // resolved meanwhile is fine
		<-p.done
	}
	return p.outcome
}

// Adds the request as pending, tells observers it was created, and returns it
// without waiting. It expires after the manager's timeout unless it is resolved
// first (ADR 0004).
func (m *Manager) addPending(info RequestInfo) *pendingRequest {
	req := info.newRecord()
	p := &pendingRequest{
		Request:   req,
		ExpiresAt: req.CreatedAt.Add(m.timeout),
		done:      make(chan struct{}),
	}

	m.mu.Lock()
	m.pending[req.ID] = p
	m.mu.Unlock()

	m.notify(func(o Observer) { o.RequestCreated(p.Pending) })

	// Armed after the created event, so that observers never see the request
	// expire before they see it created.
	m.mu.Lock()
	if _, ok := m.pending[req.ID]; ok {
		p.expiry = time.AfterFunc(time.Until(p.ExpiresAt), func() {
			m.resolve(req.ID, ResolutionExpired, nil) //nolint:errcheck // resolved meanwhile is fine
		})
	}
	m.mu.Unlock()
	return p
}

// Ends the pending request with the given id and resolution, and tells
// observers. Errors with ErrNotFound when no such request is pending, and with
// the admit function's error, which leaves the request pending.
func (m *Manager) resolve(id RequestID, res Resolution, admit func(*Request) error) (*Request, error) {
	m.mu.Lock()
	p, ok := m.pending[id]
	if !ok {
		m.mu.Unlock()
		return nil, ErrNotFound
	}
	if admit != nil {
		if err := admit(p.Request); err != nil {
			m.mu.Unlock()
			return nil, err
		}
	}
	delete(m.pending, id)
	if p.expiry != nil {
		p.expiry.Stop()
	}
	p.outcome = p.Request.historyEntry(res)
	close(p.done)
	m.mu.Unlock()

	m.publishEnd(p.outcome)
	return p.Request, nil
}

// Returns the requests that wait for the user and have not expired.
func (m *Manager) Pending() []Pending {
	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()
	result := make([]Pending, 0, len(m.pending))
	for _, p := range m.pending {
		if !p.expired(now) {
			result = append(result, p.Pending)
		}
	}
	return result
}

// Approves the pending request with the given id and caches the approval of its
// items. Errors with ErrNotFound when no such request is pending.
func (m *Manager) Approve(id RequestID) error {
	req, err := m.resolve(id, ResolutionApproved, nil)
	if err != nil {
		return err
	}
	m.cacheApproval(req)
	return nil
}

// Approves the request as Approve does, errors included, and adds an
// auto-approve rule for requests like it (ADR 0004). Errors with
// ErrNotAutoApprovable, approving nothing, unless Request.AutoApprovable holds.
func (m *Manager) ApproveAndAutoApprove(id RequestID) error {
	var scope AutoApproveScope
	req, err := m.resolve(id, ResolutionApproved, func(req *Request) (err error) {
		scope, err = req.AutoApproveScope()
		return err
	})
	if err != nil {
		return err
	}
	m.cacheApproval(req)
	m.AddAutoApproveRule(scope, req.SenderInfo.InvokerName)
	return nil
}

// Ends the pending request with the given id as denied by the user. Cancel ends
// it with no decision. Errors with ErrNotFound when no such request is pending.
func (m *Manager) Deny(id RequestID) error {
	_, err := m.resolve(id, ResolutionDenied, nil)
	return err
}

// Ends the pending request with the given id as cancelled, recording no decision
// by the user. Errors with ErrNotFound when no such request is pending.
func (m *Manager) Cancel(id RequestID) error {
	_, err := m.resolve(id, ResolutionCancelled, nil)
	return err
}

// Returns the number of pending requests.
func (m *Manager) PendingCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.pending)
}

// Returns how long a request waits for the user before it expires.
func (m *Manager) Timeout() time.Duration {
	return m.timeout
}

// Returns the pending request with the given ID.
func (m *Manager) GetPending(id RequestID) (Pending, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.pending[id]
	if !ok {
		return Pending{}, false
	}
	return p.Pending, true
}

// Approves a pending gpg_sign request with what gpg produced. A failed gpg run
// is approved too, and the thin client then exits with its ExitCode.
// Errors with ErrNotFound when no request with that id is pending.
func (m *Manager) ApproveGPGSign(id RequestID, res GPGResult) error {
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
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()

	for _, item := range info.Items {
		key, ok := info.SenderInfo.cacheKey(item.Path)
		if !ok || !m.cache.Has(key) {
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
		m.cache.Put(key, time.Now().Add(m.approvalWindow))
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
// approvals cached for that name, for the reason approvalCacheKey gives.
func (m *Manager) ReleaseClient(name ClientName) {
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	delete(m.clients, name)
	m.cache.DeleteFunc(func(key approvalCacheKey) bool { return key.client == name })
}

func (m *Manager) AutoApproveDuration() time.Duration {
	return m.autoApproveDuration
}

// Adds a rule with the given scope for the duration that
// Manager.AutoApproveDuration returns, and returns its ID. When a rule with that
// scope exists, extends that rule's expiry instead and returns its ID.
func (m *Manager) AddAutoApproveRule(scope AutoApproveScope, invokerName string) AutoApproveRuleID {
	expiresAt := time.Now().Add(m.AutoApproveDuration())

	var rule AutoApproveRule
	msg := "auto-approve rule added"
	m.autoApproveMu.Lock()
	if i := slices.IndexFunc(m.autoApproveRules, func(r AutoApproveRule) bool { return r.AutoApproveScope.Equal(scope) }); i >= 0 {
		m.autoApproveRules[i].ExpiresAt = expiresAt
		rule = m.autoApproveRules[i]
		msg = "auto-approve rule refreshed"
	} else {
		rule = AutoApproveRule{
			ID:               AutoApproveRuleID(uuid.New().String()),
			InvokerName:      invokerName,
			AutoApproveScope: scope,
			ExpiresAt:        expiresAt,
		}
		m.autoApproveRules = append(m.autoApproveRules, rule)
	}
	m.autoApproveMu.Unlock()

	m.notify(func(o Observer) { o.RuleAdded(rule) })
	slog.Info(msg, "rule", rule)
	return rule.ID
}

// Returns the first active auto-approve rule that matches the request.
func (m *Manager) CheckAutoApproveRules(info RequestInfo) (AutoApproveRule, bool) {
	m.autoApproveMu.Lock()
	defer m.autoApproveMu.Unlock()

	rules := m.activeRules()
	i := slices.IndexFunc(rules, func(r AutoApproveRule) bool { return r.matches(info) })
	if i < 0 {
		return AutoApproveRule{}, false
	}
	return rules[i], true
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

// Returns the auto-approve rules that have not expired.
func (m *Manager) ListAutoApproveRules() []AutoApproveRule {
	m.autoApproveMu.Lock()
	defer m.autoApproveMu.Unlock()

	return slices.Clone(m.activeRules())
}

// Returns the configured trusted signers.
func (m *Manager) ListTrustedSigners() []TrustedSigner {
	return m.trustedSigners
}

// Adds an auto-approve rule made from the cancelled request with the given ID.
// Errors with ErrNotFound when no cancelled request has that ID, and as
// Request.AutoApproveScope does otherwise.
func (m *Manager) AddRuleFromCancelled(id RequestID) error {
	entry, ok := m.GetHistoryEntry(id)
	if !ok || entry.Resolution != ResolutionCancelled {
		return ErrNotFound
	}
	scope, err := entry.Request.AutoApproveScope()
	if err != nil {
		return err
	}
	m.AddAutoApproveRule(scope, entry.Request.SenderInfo.InvokerName)
	return nil
}

// Removes the auto-approve rule with the given ID. Errors with ErrNotFound when
// there is none.
func (m *Manager) RemoveAutoApproveRule(id AutoApproveRuleID) error {
	m.autoApproveMu.Lock()
	i := slices.IndexFunc(m.autoApproveRules, func(r AutoApproveRule) bool { return r.ID == id })
	if i < 0 {
		m.autoApproveMu.Unlock()
		return ErrNotFound
	}
	removed := m.autoApproveRules[i]
	m.autoApproveRules = slices.Delete(m.autoApproveRules, i, i+1)
	m.autoApproveMu.Unlock()

	m.notify(func(o Observer) { o.RuleRemoved(removed) })
	slog.Info("auto-approve rule removed", "rule", removed)
	return nil
}

// Checks whether a trusted_signers entry lets a git signing request skip the
// prompt. A request that did not come through the thin client never skips it,
// since its repo name and changed files may be forged (ADR 0008).
func (m *Manager) CheckTrustedSigner(info RequestInfo) bool {
	sign, ok := info.GPGSign()
	if !ok || !info.SenderInfo.PeerTrusted {
		return false
	}
	return slices.ContainsFunc(m.trustedSigners, func(ts TrustedSigner) bool {
		return ts.matches(info.SenderInfo.ProcessChain, sign.RepoName, sign.Signed().Details)
	})
}

// Returns the /proc/PID/exe path of the invoker, or false when it is unknown.
func (s SenderInfo) invokerExe() (procutil.Path, bool) {
	for _, p := range s.ProcessChain {
		if p.PID == s.PID {
			return p.ExePath()
		}
	}
	return "", false
}

// Returns the first configured trust rule that matches the request.
func (m *Manager) CheckTrustRules(info RequestInfo) (TrustRule, bool) {
	i := slices.IndexFunc(m.trustRules, func(r TrustRule) bool { return r.matches(info) })
	if i < 0 {
		return TrustRule{}, false
	}
	return m.trustRules[i], true
}

// Returns the configured trust rules.
func (m *Manager) ListTrustRules() []TrustRule {
	return m.trustRules
}

// The xdg:schema that Chrome uses for its dummy keyring-unlock probes.
const chromeDummySchema = "_chrome_dummy_schema_for_unlocking"

// Checks whether the manager drops the request as a Chrome dummy secret write.
func (m *Manager) dropsChromeDummyWrite(info RequestInfo) bool {
	if !m.ignoreChromeDummy || info.Type != RequestTypeWrite {
		return false
	}
	return slices.ContainsFunc(info.Items, func(item ItemInfo) bool {
		return item.Attributes["xdg:schema"] == chromeDummySchema
	})
}

// Records a request that is served without a prompt, as a matching trust rule
// decides it (ADR 0005). Errors with ErrDeniedByRule or ErrIgnored, wrapped with
// the rule's name, when a deny or ignore rule matches.
func (m *Manager) Passthrough(info RequestInfo) error {
	if rule, ok := m.CheckTrustRules(info); ok {
		return m.settleByRule(rule, info)
	}
	m.record(ResolutionAutoApproved, info)
	return nil
}

// Records the request as the given trust rule decides it, and returns the rule's
// error wrapped with the rule's name.
func (m *Manager) settleByRule(rule TrustRule, info RequestInfo) error {
	slog.Info("trust rule matched", "rule_name", rule.Name, "action", rule.Action)
	res, err := rule.Action.outcome()
	m.record(res, info)
	if err != nil {
		return fmt.Errorf("%w: %s", err, rule.Name)
	}
	return nil
}

// Records in History a request that was settled without waiting for the user,
// and tells observers that it ended.
func (m *Manager) record(res Resolution, info RequestInfo) {
	m.publishEnd(info.newRecord().historyEntry(res))
}

// Caches the approval of each of the request's items for its sender.
func (m *Manager) cacheApproval(req *Request) {
	for _, item := range req.Items {
		m.CacheItemForSender(req.SenderInfo, item.Path)
	}
}
