package approval

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// itemInCollection builds an ItemInfo whose path places it in the named collection.
func itemInCollection(collection, name string) ItemInfo {
	return ItemInfo{Path: "/org/freedesktop/secrets/collection/" + collection + "/" + name}
}

// TestBatchGetSecrets_AutoApproveDoesNotCoverForeignItem is a regression test for
// the batch authorization bypass (Vuln 3). An ephemeral auto-approve rule scoped
// to a benign collection must not authorize a whole batch that also pulls a secret
// from a different collection — matching must consider every item, not just
// items[0].
func TestBatchGetSecrets_AutoApproveDoesNotCoverForeignItem(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100, AutoApproveDuration: 2 * time.Minute})

	// The user auto-approved a read scoped to the "public" collection.
	mgr.AddAutoApproveRule(&Request{
		Type:       RequestTypeGetSecret,
		Items:      []ItemInfo{itemInCollection("public", "x")},
		SenderInfo: SenderInfo{UnitName: "someapp"},
	})

	// A batch whose first item is the benign one, second item is sensitive.
	batch := []ItemInfo{
		itemInCollection("public", "x"),
		itemInCollection("login", "github-token"),
	}
	assert.Nil(t, mgr.checkAutoApproveRules(SenderInfo{UnitName: "someapp"}, batch, RequestTypeGetSecret),
		"auto-approve scoped to 'public' must not cover a batch that also reads 'login'")

	// A batch fully inside the approved collection is still covered.
	inScope := []ItemInfo{
		itemInCollection("public", "x"),
		itemInCollection("public", "y"),
	}
	assert.NotNil(t, mgr.checkAutoApproveRules(SenderInfo{UnitName: "someapp"}, inScope, RequestTypeGetSecret),
		"auto-approve scoped to 'public' should still cover an all-'public' batch")
}

// TestBatchGetSecrets_DenyRuleFiresOnAnyItem verifies that a deny rule scoped to a
// sensitive collection fires when a batch smuggles that collection in behind a
// benign first item (deny is restrictive: any in-scope item triggers it).
func TestBatchGetSecrets_DenyRuleFiresOnAnyItem(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
	mgr.trustRules = []TrustRule{{
		Name:   "deny-login",
		Action: "deny",
		Secret: &SecretMatcher{Collection: "login"},
	}}

	batch := []ItemInfo{
		itemInCollection("public", "x"),
		itemInCollection("login", "github-token"),
	}
	rule := mgr.CheckTrustRules(SenderInfo{}, batch, RequestTypeGetSecret, nil)
	require.NotNil(t, rule, "deny rule scoped to 'login' must fire on a batch containing a 'login' item")
	assert.Equal(t, "deny-login", rule.Name)
}

// TestBatchGetSecrets_ApproveRuleRequiresEveryItem verifies that a permissive
// approve rule scoped to a benign collection does not silently authorize a batch
// that also reads a foreign collection (approve is permissive: every item must be
// in scope).
func TestBatchGetSecrets_ApproveRuleRequiresEveryItem(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
	mgr.trustRules = []TrustRule{{
		Name:   "approve-public",
		Action: "approve",
		Secret: &SecretMatcher{Collection: "public"},
	}}

	mixed := []ItemInfo{
		itemInCollection("public", "x"),
		itemInCollection("login", "github-token"),
	}
	assert.Nil(t, mgr.CheckTrustRules(SenderInfo{}, mixed, RequestTypeGetSecret, nil),
		"approve rule scoped to 'public' must not cover a batch that also reads 'login'")

	allPublic := []ItemInfo{
		itemInCollection("public", "x"),
		itemInCollection("public", "y"),
	}
	rule := mgr.CheckTrustRules(SenderInfo{}, allPublic, RequestTypeGetSecret, nil)
	require.NotNil(t, rule, "approve rule scoped to 'public' should cover an all-'public' batch")
	assert.Equal(t, "approve-public", rule.Name)
}
