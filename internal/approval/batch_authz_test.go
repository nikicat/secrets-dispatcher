package approval

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An auto-approve rule scoped to one collection does not cover a batch that
// also reads another collection.
func TestBatchGetSecrets_AutoApproveDoesNotCoverForeignItem(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})

	sender := testSender("someapp", "/usr/bin/someapp")

	// The user auto-approved a read scoped to the "public" collection.
	mustAddRule(t, mgr, &Request{
		Type:       RequestTypeGetSecret,
		Items:      []ItemInfo{itemInCollection("public", "x")},
		SenderInfo: sender,
	})

	// A batch whose first item is the benign one, second item is sensitive.
	batch := []ItemInfo{
		itemInCollection("public", "x"),
		itemInCollection("login", "github-token"),
	}
	assert.Nil(t, mgr.CheckAutoApproveRules(RequestInfo{Items: batch, Type: RequestTypeGetSecret, SenderInfo: sender}),
		"auto-approve scoped to 'public' must not cover a batch that also reads 'login'")

	// A batch fully inside the approved collection is still covered.
	inScope := []ItemInfo{
		itemInCollection("public", "x"),
		itemInCollection("public", "y"),
	}
	assert.NotNil(t, mgr.CheckAutoApproveRules(RequestInfo{Items: inScope, Type: RequestTypeGetSecret, SenderInfo: sender}),
		"auto-approve scoped to 'public' should still cover an all-'public' batch")
}

// A deny rule fires when any item of the batch is in its scope, even behind a
// benign first item (ADR 0005).
func TestBatchGetSecrets_DenyRuleFiresOnAnyItem(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
	mgr.trustRules = []TrustRule{{
		Name:   "deny-login",
		Action: "deny",
		Secret: &SecretMatcher{Collection: MustGlob("login")},
	}}

	batch := []ItemInfo{
		itemInCollection("public", "x"),
		itemInCollection("login", "github-token"),
	}
	rule := mgr.CheckTrustRules(RequestInfo{Items: batch, Type: RequestTypeGetSecret, SenderInfo: SenderInfo{Client: ClientLocal}})
	require.NotNil(t, rule, "deny rule scoped to 'login' must fire on a batch containing a 'login' item")
	assert.Equal(t, "deny-login", rule.Name)
}

// An approve rule fires only when every item of the batch is in its scope
// (ADR 0005).
func TestBatchGetSecrets_ApproveRuleRequiresEveryItem(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: 5 * time.Second, HistoryMax: 100})
	mgr.trustRules = []TrustRule{{
		Name:   "approve-public",
		Action: "approve",
		Secret: &SecretMatcher{Collection: MustGlob("public")},
	}}

	mixed := []ItemInfo{
		itemInCollection("public", "x"),
		itemInCollection("login", "github-token"),
	}
	assert.Nil(t, mgr.CheckTrustRules(RequestInfo{Items: mixed, Type: RequestTypeGetSecret, SenderInfo: SenderInfo{Client: ClientLocal}}),
		"approve rule scoped to 'public' must not cover a batch that also reads 'login'")

	allPublic := []ItemInfo{
		itemInCollection("public", "x"),
		itemInCollection("public", "y"),
	}
	rule := mgr.CheckTrustRules(RequestInfo{Items: allPublic, Type: RequestTypeGetSecret, SenderInfo: SenderInfo{Client: ClientLocal}})
	require.NotNil(t, rule, "approve rule scoped to 'public' should cover an all-'public' batch")
	assert.Equal(t, "approve-public", rule.Name)
}
