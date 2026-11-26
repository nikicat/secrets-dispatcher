package approval

import (
	"sync"
	"time"

	"github.com/nikicat/secrets-dispatcher/internal/expiryset"
	"github.com/nikicat/secrets-dispatcher/internal/sender"
)

// The user's recent approvals of items, by sender, which approve the same
// sender's later requests for those items within the window (ADR 0004).
type approvalCache struct {
	// Zero turns the cache off.
	window  time.Duration
	mu      sync.Mutex
	entries expiryset.Set[approvalCacheKey]
	clients map[ClientName]ClientClaim // the claims of running buses
	claims  claimGen                   // the last claim made
}

// Numbers the claims of client names, from 1. A sender that no claim covers
// has claim 0.
type claimGen uint64

// A bus's hold on a client name.
type ClientClaim struct {
	Name ClientName
	gen  claimGen
}

// Returns the sender with the given unique name on the bus that holds c.
func (c ClientClaim) Sender(name sender.UniqueName) SenderInfo {
	return SenderInfo{Client: c.Name, Sender: &name, claim: c.gen}
}

// Identifies an item approved for a sender on one bus. An approval never covers
// a sender of the next bus that claims the same client name (ADR 0004).
type approvalCacheKey struct {
	client   ClientName
	claim    claimGen
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
	return approvalCacheKey{s.Client, s.claim, *s.Sender, itemPath}, true
}

// Checks whether every item of the given request was recently approved for the
// same sender. A request with no items, or of a type that is not cacheable, is
// never answered from the cache.
func (c *approvalCache) covers(info RequestInfo) bool {
	if c.window == 0 || !info.Type.cacheable() || len(info.Items) == 0 {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, item := range info.Items {
		key, ok := info.SenderInfo.cacheKey(item.Path)
		if !ok || !c.entries.Has(key) {
			return false
		}
	}
	return true
}

func (c *approvalCache) put(s SenderInfo, itemPath string) {
	if c.window == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if key, ok := s.cacheKey(itemPath); ok {
		c.entries.Put(key, time.Now().Add(c.window))
	}
}

// Caches the approval of each of the request's items for its sender.
func (c *approvalCache) putRequest(req *Request) {
	for _, item := range req.Items {
		c.put(req.SenderInfo, item.Path)
	}
}

func (c *approvalCache) claim(name ClientName) (ClientClaim, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, held := c.clients[name]; held {
		return ClientClaim{}, false
	}
	if c.clients == nil {
		c.clients = map[ClientName]ClientClaim{}
	}
	c.claims++
	claim := ClientClaim{Name: name, gen: c.claims}
	c.clients[name] = claim
	return claim, true
}

func (c *approvalCache) release(claim ClientClaim) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.clients[claim.Name] == claim {
		delete(c.clients, claim.Name)
	}
	c.entries.DeleteFunc(func(key approvalCacheKey) bool { return key.client == claim.Name && key.claim == claim.gen })
}
