package approval

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validManagedRule() ManagedTrustRule {
	return ManagedTrustRule{
		ID:        uuid.New().String(),
		Enabled:   true,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		TrustRule: TrustRule{
			Name:         "gh: token",
			Action:       "approve",
			RequestTypes: []string{string(RequestTypeGetSecret)},
			Process:      &ProcessMatcher{Exe: "/usr/bin/gh", Direct: true},
			Secret: &SecretMatcher{
				Collection: "login",
				Attributes: map[string]string{"service": "github"},
			},
		},
	}
}

func managedRuleRequest(exe string) *Request {
	return &Request{
		ID:   "approved-request",
		Type: RequestTypeGetSecret,
		Items: []ItemInfo{{
			Path:       "/org/freedesktop/secrets/collection/login/item1",
			Label:      "GitHub token",
			Attributes: map[string]string{"service": "github"},
		}},
		SenderInfo: SenderInfo{
			PID:          101,
			ProcessChain: []ProcessInfo{{Name: filepath.Base(exe), PID: 101, Exe: exe, Args: []string{exe}}},
		},
	}
}

func TestFileManagedTrustRuleStoreRoundTrip(t *testing.T) {
	store := NewFileManagedTrustRuleStore(t.TempDir())
	rule := validManagedRule()
	require.NoError(t, store.Save([]ManagedTrustRule{rule}))

	info, err := os.Stat(store.path)
	require.NoError(t, err)
	assert.Zero(t, info.Mode().Perm()&0o077)

	loaded, err := store.Load()
	require.NoError(t, err)
	require.Len(t, loaded, 1)
	assert.Equal(t, rule, loaded[0])

	loaded[0].Secret.Attributes["service"] = "changed"
	reloaded, err := store.Load()
	require.NoError(t, err)
	assert.Equal(t, "github", reloaded[0].Secret.Attributes["service"])
}

func TestFileManagedTrustRuleStoreMissingFile(t *testing.T) {
	store := NewFileManagedTrustRuleStore(t.TempDir())
	rules, err := store.Load()
	require.NoError(t, err)
	assert.Empty(t, rules)
}

func TestFileManagedTrustRuleStoreMigratesLegacyRules(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.Chmod(dir, 0o700))
	file := filepath.Join(dir, "approval-rules.json")
	legacy := `{
  "version": 1,
  "rules": [
    {
      "id": "7aed3d0a-b610-4dff-88a1-cf6fd80491ca",
      "name": "gh get_secret",
      "enabled": true,
      "request_types": ["get_secret"],
      "process": {"exe": "/usr/bin/gh"},
      "secret": {"collection": "login", "attributes": {"service": "github"}},
      "created_at": "2026-06-16T12:00:18Z",
      "updated_at": "2026-06-16T12:00:18Z"
    },
    {
      "id": "legacy-search-rule",
      "name": "unsupported search",
      "enabled": true,
      "request_types": ["search"],
      "process": {"exe": "/usr/bin/gh"},
      "search_attributes": {"service": "github"},
      "created_at": "2026-06-16T12:00:18Z",
      "updated_at": "2026-06-16T12:00:18Z"
    },
    {
      "id": "unsupported-gpg-rule",
      "name": "unsupported gpg",
      "enabled": true,
      "request_types": ["gpg_sign"],
      "process": {"exe": "/usr/bin/git"},
      "created_at": "2026-06-16T12:00:18Z",
      "updated_at": "2026-06-16T12:00:18Z"
    }
  ]
}`
	require.NoError(t, os.WriteFile(file, []byte(legacy), 0o600))

	store := NewFileManagedTrustRuleStore(dir)
	rules, err := store.Load()
	require.NoError(t, err)
	require.Len(t, rules, 2)
	assert.Equal(t, "gh get_secret", rules[0].Name)
	assert.Equal(t, "approve", rules[0].Action)
	assert.False(t, rules[0].Process.Direct)
	assert.Equal(t, []string{"search"}, rules[1].RequestTypes)
	assert.Equal(t, map[string]string{"service": "github"}, rules[1].SearchAttributes)

	backup, err := os.ReadFile(file + ".legacy-v1")
	require.NoError(t, err)
	assert.Equal(t, legacy, string(backup))

	reloaded, err := store.Load()
	require.NoError(t, err)
	assert.Equal(t, rules, reloaded)
}

func TestFileManagedTrustRuleStoreRejectsUnsafeFiles(t *testing.T) {
	t.Run("unknown field", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.Chmod(dir, 0o700))
		file := filepath.Join(dir, "approval-rules.json")
		require.NoError(t, os.WriteFile(file, []byte(`{"version":1,"rules":[],"unexpected":true}`), 0o600))
		_, err := NewFileManagedTrustRuleStore(dir).Load()
		require.Error(t, err)
	})

	t.Run("unsupported version", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.Chmod(dir, 0o700))
		file := filepath.Join(dir, "approval-rules.json")
		require.NoError(t, os.WriteFile(file, []byte(`{"version":2,"rules":[]}`), 0o600))
		_, err := NewFileManagedTrustRuleStore(dir).Load()
		require.ErrorContains(t, err, "unsupported")
	})

	t.Run("public permissions", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.Chmod(dir, 0o700))
		file := filepath.Join(dir, "approval-rules.json")
		require.NoError(t, os.WriteFile(file, []byte(`{"version":1,"rules":[]}`), 0o644))
		_, err := NewFileManagedTrustRuleStore(dir).Load()
		require.ErrorContains(t, err, "permissions")
	})

	t.Run("symlink", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.Chmod(dir, 0o700))
		target := filepath.Join(dir, "target")
		require.NoError(t, os.WriteFile(target, []byte(`{"version":1,"rules":[]}`), 0o600))
		require.NoError(t, os.Symlink(target, filepath.Join(dir, "approval-rules.json")))
		_, err := NewFileManagedTrustRuleStore(dir).Load()
		require.ErrorContains(t, err, "regular file")
	})

	t.Run("multiple JSON values", func(t *testing.T) {
		dir := t.TempDir()
		require.NoError(t, os.Chmod(dir, 0o700))
		file := filepath.Join(dir, "approval-rules.json")
		require.NoError(t, os.WriteFile(file, []byte(`{"version":1,"rules":[]} {}`), 0o600))
		_, err := NewFileManagedTrustRuleStore(dir).Load()
		require.Error(t, err)
	})
}

func TestValidateManagedTrustRuleRejectsUnsafePolicy(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*ManagedTrustRule)
	}{
		{"deny", func(r *ManagedTrustRule) { r.Action = "deny" }},
		{"gpg", func(r *ManagedTrustRule) { r.RequestTypes = []string{string(RequestTypeGPGSign)} }},
		{"missing exe", func(r *ManagedTrustRule) { r.Process.Exe = "" }},
		{"relative exe", func(r *ManagedTrustRule) { r.Process.Exe = "bin/gh" }},
		{"invalid glob", func(r *ManagedTrustRule) { r.Process.Exe = "[" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rule := validManagedRule()
			tc.mutate(&rule)
			require.ErrorIs(t, ValidateManagedTrustRule(&rule), ErrInvalidManagedRule)
		})
	}
}

func TestManagedTrustRuleGenerationIsExactAndDirect(t *testing.T) {
	req := managedRuleRequest("/usr/bin/gh")
	req.Items[0].Attributes["service"] = "gh[prod]*"
	rule, err := managedTrustRuleFromRequest(req)
	require.NoError(t, err)
	require.NotNil(t, rule.Process)
	assert.Equal(t, "/usr/bin/gh", rule.Process.Exe)
	assert.True(t, rule.Process.Direct)
	assert.Empty(t, rule.Process.Name)
	assert.Equal(t, `gh\[prod]\*`, rule.Secret.Attributes["service"])

	sender := req.SenderInfo
	items := req.Items
	assert.True(t, matchTrustRule(&rule.TrustRule, sender, items, RequestTypeGetSecret, nil))
	items[0].Attributes = map[string]string{"service": "ghp"}
	assert.False(t, matchTrustRule(&rule.TrustRule, sender, items, RequestTypeGetSecret, nil))

	// The trusted executable appearing only as an ancestor cannot satisfy Direct.
	sender.ProcessChain = []ProcessInfo{
		{Name: "malware", PID: 200, Exe: "/tmp/malware"},
		{Name: "gh", PID: 101, Exe: "/usr/bin/gh"},
	}
	items[0].Attributes = map[string]string{"service": "gh[prod]*"}
	assert.False(t, matchTrustRule(&rule.TrustRule, sender, items, RequestTypeGetSecret, nil))
}

func TestManagedTrustRuleGenerationHandlesInterpreters(t *testing.T) {
	t.Run("absolute script", func(t *testing.T) {
		req := managedRuleRequest("/usr/bin/bash")
		req.SenderInfo.ProcessChain[0].Args = []string{"bash", "/home/me/bin/read-secret"}
		rule, err := managedTrustRuleFromRequest(req)
		require.NoError(t, err)
		assert.Equal(t, "/home/me/bin/read-secret", rule.Process.Args)
		assert.Empty(t, rule.Process.CWD)
		assert.True(t, matchTrustRule(&rule.TrustRule, req.SenderInfo, req.Items, RequestTypeGetSecret, nil))
	})

	t.Run("relative script binds cwd", func(t *testing.T) {
		req := managedRuleRequest("/usr/bin/python3")
		req.SenderInfo.ProcessChain[0].Args = []string{"python3", "scripts/read.py"}
		req.SenderInfo.ProcessChain[0].CWD = "/home/me/project"
		rule, err := managedTrustRuleFromRequest(req)
		require.NoError(t, err)
		assert.Equal(t, "scripts/read.py", rule.Process.Args)
		assert.Equal(t, "/home/me/project", rule.Process.CWD)
	})

	t.Run("options rejected", func(t *testing.T) {
		req := managedRuleRequest("/usr/bin/python3")
		req.SenderInfo.ProcessChain[0].Args = []string{"python3", "-m", "module"}
		_, err := managedTrustRuleFromRequest(req)
		require.ErrorIs(t, err, ErrInvalidManagedRule)
	})
}

func TestManagedTrustRuleGenerationRejectsUnsafeRequests(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Request)
	}{
		{"gpg", func(r *Request) { r.Type = RequestTypeGPGSign }},
		{"no process", func(r *Request) { r.SenderInfo.ProcessChain = nil }},
		{"unresolved exe", func(r *Request) { r.SenderInfo.ProcessChain[0].Exe = "" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := managedRuleRequest("/usr/bin/gh")
			tc.mutate(req)
			_, err := managedTrustRuleFromRequest(req)
			require.ErrorIs(t, err, ErrInvalidManagedRule)
		})
	}
}

type recordingManagedStore struct {
	err   error
	saved [][]ManagedTrustRule
}

func (s *recordingManagedStore) Load() ([]ManagedTrustRule, error) { return nil, s.err }

func (s *recordingManagedStore) Save(rules []ManagedTrustRule) error {
	if s.err != nil {
		return s.err
	}
	s.saved = append(s.saved, cloneManagedTrustRules(rules))
	return nil
}

func TestManagerManagedRulesPersistBeforeMemoryAndEvents(t *testing.T) {
	storeErr := errors.New("disk full")
	store := &recordingManagedStore{err: storeErr}
	mgr := NewManager(ManagerConfig{HistoryMax: 10, ManagedRuleStore: store})
	mgr.AddHistoryEntry(HistoryEntry{Request: managedRuleRequest("/usr/bin/gh"), Resolution: ResolutionApproved})
	observer := &testObserver{}
	mgr.Subscribe(observer)

	_, _, err := mgr.CreateManagedTrustRuleFromRequest("approved-request")
	require.ErrorIs(t, err, storeErr)
	assert.Empty(t, mgr.ListManagedTrustRules())
	assert.Empty(t, observer.Events())

	existing := validManagedRule()
	store.err = nil
	mgr = NewManager(ManagerConfig{HistoryMax: 10, ManagedTrustRules: []ManagedTrustRule{existing}, ManagedRuleStore: store})
	observer = &testObserver{}
	mgr.Subscribe(observer)
	store.err = storeErr
	require.ErrorIs(t, mgr.RemoveManagedTrustRule(existing.ID), storeErr)
	assert.Len(t, mgr.ListManagedTrustRules(), 1)
	assert.Empty(t, observer.Events())
}

func TestManagerManagedRuleLifecycleAndAttribution(t *testing.T) {
	store := &recordingManagedStore{}
	mgr := NewManager(ManagerConfig{Timeout: time.Second, HistoryMax: 10, ManagedRuleStore: store})
	req := managedRuleRequest("/usr/bin/gh")
	mgr.AddHistoryEntry(HistoryEntry{Request: req, Resolution: ResolutionApproved})

	rule, created, err := mgr.CreateManagedTrustRuleFromRequest(req.ID)
	require.NoError(t, err)
	assert.True(t, created)
	require.Len(t, store.saved, 1)
	require.Len(t, mgr.ListManagedTrustRules(), 1)

	// Returned and listed rules cannot mutate manager state.
	rule.Secret.Attributes["service"] = "mutated"
	listed := mgr.ListManagedTrustRules()
	listed[0].Secret.Attributes["service"] = "also-mutated"
	assert.Equal(t, "github", mgr.ListManagedTrustRules()[0].Secret.Attributes["service"])

	autoApproved, err := mgr.RequireApproval(context.Background(), "client", req.Items, "", RequestTypeGetSecret, nil, req.SenderInfo)
	require.NoError(t, err)
	assert.True(t, autoApproved)
	history := mgr.History()
	require.NotEmpty(t, history)
	require.NotNil(t, history[0].Request.Attribution)
	assert.Equal(t, "managed_rule", history[0].Request.Attribution.Source)
	assert.Equal(t, mgr.ListManagedTrustRules()[0].ID, history[0].Request.Attribution.RuleID)

	require.NoError(t, mgr.RemoveManagedTrustRule(mgr.ListManagedTrustRules()[0].ID))
	assert.Empty(t, mgr.ListManagedTrustRules())
	require.Len(t, store.saved, 2)
}

func TestManagerManagedRuleMatchesAfterStoreReload(t *testing.T) {
	store := NewFileManagedTrustRuleStore(t.TempDir())
	mgr := NewManager(ManagerConfig{Timeout: time.Second, HistoryMax: 10, ManagedRuleStore: store})
	req := managedRuleRequest("/usr/bin/gh")
	mgr.AddHistoryEntry(HistoryEntry{Request: req, Resolution: ResolutionApproved})
	_, _, err := mgr.CreateManagedTrustRuleFromRequest(req.ID)
	require.NoError(t, err)

	loaded, err := store.Load()
	require.NoError(t, err)
	reloaded := NewManager(ManagerConfig{Timeout: time.Second, HistoryMax: 10, ManagedTrustRules: loaded, ManagedRuleStore: store})
	autoApproved, err := reloaded.RequireApproval(context.Background(), "client", req.Items, "", RequestTypeGetSecret, nil, req.SenderInfo)
	require.NoError(t, err)
	assert.True(t, autoApproved)
}

func TestCreateManagedTrustRuleAllowsEligibleAutoApprovedHistory(t *testing.T) {
	mgr := NewManager(ManagerConfig{HistoryMax: 10})
	req := managedRuleRequest("/usr/bin/gh")
	mgr.AddHistoryEntry(HistoryEntry{Request: req, Resolution: ResolutionAutoApproved})
	_, created, err := mgr.CreateManagedTrustRuleFromRequest(req.ID)
	require.NoError(t, err)
	assert.True(t, created)
}

func TestConfigDenyPrecedesAllAutomaticApprovalSources(t *testing.T) {
	req := managedRuleRequest("/usr/bin/gh")
	managed, err := managedTrustRuleFromRequest(req)
	require.NoError(t, err)
	mgr := NewManager(ManagerConfig{
		Timeout:             time.Second,
		HistoryMax:          10,
		ApprovalWindow:      time.Minute,
		AutoApproveDuration: time.Minute,
		ManagedTrustRules:   []ManagedTrustRule{managed},
		TrustRules: []TrustRule{
			{Name: "early-approve", Action: "approve", Process: &ProcessMatcher{Exe: "/usr/bin/gh"}},
			{Name: "hard-deny", Action: "deny", Process: &ProcessMatcher{Exe: "/usr/bin/gh"}},
		},
	})
	mgr.CacheItemForSender(req.SenderInfo.Sender, req.Items[0].Path)
	mgr.AddAutoApproveRule(req)

	autoResolved, err := mgr.RequireApproval(context.Background(), "client", req.Items, "", RequestTypeGetSecret, nil, req.SenderInfo)
	require.ErrorIs(t, err, ErrDeniedByRule)
	assert.True(t, autoResolved)
	require.NotEmpty(t, mgr.History())
	assert.Equal(t, ResolutionDenied, mgr.History()[0].Resolution)
}

func TestConfigIgnorePrecedesTemporaryApproval(t *testing.T) {
	req := managedRuleRequest("/usr/bin/gh")
	req.Type = RequestTypeWrite
	mgr := NewManager(ManagerConfig{
		Timeout:             time.Second,
		HistoryMax:          10,
		AutoApproveDuration: time.Minute,
		TrustRules: []TrustRule{{
			Name:         "hard-ignore",
			Action:       "ignore",
			RequestTypes: []string{string(RequestTypeWrite)},
			Process:      &ProcessMatcher{Exe: "/usr/bin/gh"},
		}},
	})
	mgr.AddAutoApproveRule(req)

	autoResolved, err := mgr.RequireApproval(context.Background(), "client", req.Items, "", RequestTypeWrite, nil, req.SenderInfo)
	require.ErrorIs(t, err, ErrIgnored)
	assert.True(t, autoResolved)
	require.NotEmpty(t, mgr.History())
	assert.Equal(t, ResolutionIgnored, mgr.History()[0].Resolution)
}

func TestCheckTrustRulesByActionSkipsOtherActions(t *testing.T) {
	mgr := NewManager(ManagerConfig{TrustRules: []TrustRule{
		{Name: "approve", Action: "approve"},
		{Name: "deny", Action: "deny"},
	}})
	rule := mgr.CheckTrustRulesByAction(SenderInfo{}, nil, RequestTypeSearch, nil, "deny")
	require.NotNil(t, rule)
	assert.Equal(t, "deny", rule.Name)
}

func TestDirectUnitMatcherDoesNotRequireProcessChain(t *testing.T) {
	rule := TrustRule{Process: &ProcessMatcher{Unit: "app.service", Direct: true}}
	assert.True(t, matchTrustRule(&rule, SenderInfo{SystemdUnit: "app.service"}, nil, RequestTypeGetSecret, nil))
}

func TestManagedTrustRuleCRUDAndBroadMatching(t *testing.T) {
	mgr := NewManager(ManagerConfig{HistoryMax: 10})
	rule, err := mgr.CreateManagedTrustRule(ManagedTrustRule{Enabled: true, TrustRule: TrustRule{
		Name:         "legacy browser secrets",
		RequestTypes: []string{string(RequestTypeGetSecret)},
		Process:      &ProcessMatcher{Name: "browser-*", CWD: "/home/*", Unit: "app-*.service"},
		Secret:       &SecretMatcher{Collection: "log*", Attributes: map[string]string{"service": "git*"}},
	}})
	require.NoError(t, err)
	assert.True(t, rule.Enabled)

	sender := SenderInfo{SystemdUnit: "app-browser.service", ProcessChain: []ProcessInfo{
		{Name: "helper", CWD: "/tmp"},
		{Name: "browser-stable", CWD: "/home/tim"},
	}}
	items := []ItemInfo{
		{Path: "/org/freedesktop/secrets/collection/login/1", Attributes: map[string]string{"service": "github"}},
		{Path: "/org/freedesktop/secrets/collection/login/2", Attributes: map[string]string{"service": "gitlab"}},
	}
	assert.NotNil(t, mgr.checkManagedTrustRules(sender, items, RequestTypeGetSecret, nil))
	searchRule, err := mgr.CreateManagedTrustRule(ManagedTrustRule{Enabled: true, TrustRule: TrustRule{
		RequestTypes:     []string{string(RequestTypeSearch)},
		Process:          &ProcessMatcher{Name: "browser-*"},
		SearchAttributes: map[string]string{"service": "git*"},
	}})
	require.NoError(t, err)
	assert.NotNil(t, mgr.checkManagedTrustRules(sender, nil, RequestTypeSearch, map[string]string{"service": "github"}))

	rule.Enabled = false
	updated, err := mgr.UpdateManagedTrustRule(rule.ID, rule)
	require.NoError(t, err)
	assert.False(t, updated.Enabled)
	assert.Nil(t, mgr.checkManagedTrustRules(sender, items, RequestTypeGetSecret, nil))
	require.NoError(t, mgr.RemoveManagedTrustRule(rule.ID))
	require.NoError(t, mgr.RemoveManagedTrustRule(searchRule.ID))
	assert.Empty(t, mgr.ListManagedTrustRules())
}

func TestRecordPassthroughAttributesMatchingRules(t *testing.T) {
	tests := []struct {
		name        string
		requestType RequestType
		items       []ItemInfo
		searchAttrs map[string]string
		rule        TrustRule
	}{
		{
			name:        "search",
			requestType: RequestTypeSearch,
			searchAttrs: map[string]string{"service": "github"},
			rule: TrustRule{
				RequestTypes:     []string{string(RequestTypeSearch)},
				Process:          &ProcessMatcher{Exe: "/usr/bin/browser", Direct: true},
				SearchAttributes: map[string]string{"service": "git*"},
			},
		},
		{
			name:        "unlock",
			requestType: RequestTypeUnlock,
			items:       []ItemInfo{{Path: "/org/freedesktop/secrets/collection/login"}},
			rule: TrustRule{
				RequestTypes: []string{string(RequestTypeUnlock)},
				Process:      &ProcessMatcher{Exe: "/usr/bin/browser", Direct: true},
				Secret:       &SecretMatcher{Collection: "login"},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mgr := NewManager(ManagerConfig{HistoryMax: 10})
			rule, err := mgr.CreateManagedTrustRule(ManagedTrustRule{Enabled: true, TrustRule: tc.rule})
			require.NoError(t, err)

			mgr.RecordPassthrough("local", tc.items, "", tc.requestType, tc.searchAttrs, SenderInfo{
				ProcessChain: []ProcessInfo{{Exe: "/usr/bin/browser"}},
			})

			history := mgr.History()
			require.Len(t, history, 1)
			require.NotNil(t, history[0].Request.Attribution)
			assert.Equal(t, "managed_rule", history[0].Request.Attribution.Source)
			assert.Equal(t, rule.ID, history[0].Request.Attribution.RuleID)
		})
	}
}

func TestCreateManagedTrustRulePreservesDisabled(t *testing.T) {
	mgr := NewManager(ManagerConfig{HistoryMax: 10})
	rule, err := mgr.CreateManagedTrustRule(ManagedTrustRule{Enabled: false, TrustRule: TrustRule{
		RequestTypes: []string{string(RequestTypeGetSecret)},
		Process:      &ProcessMatcher{Name: "legacy-*"},
	}})
	require.NoError(t, err)
	assert.False(t, rule.Enabled)
	assert.False(t, mgr.ListManagedTrustRules()[0].Enabled)
	assert.Nil(t, mgr.checkManagedTrustRules(SenderInfo{ProcessChain: []ProcessInfo{{Name: "legacy-app"}}}, nil, RequestTypeGetSecret, nil))
}

func TestCreateManagedTrustRuleFromPending(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: time.Second, HistoryMax: 10})
	req := managedRuleRequest("/usr/bin/gh")
	result := make(chan error, 1)
	go func() {
		_, err := mgr.RequireApproval(context.Background(), "client", req.Items, "", req.Type, nil, req.SenderInfo)
		result <- err
	}()
	require.Eventually(t, func() bool { return mgr.PendingCount() == 1 }, time.Second, time.Millisecond)
	pending := mgr.List()[0]
	rule, created, err := mgr.CreateManagedTrustRuleFromRequest(pending.ID)
	require.NoError(t, err)
	assert.True(t, created)
	assert.True(t, rule.Process.Direct)
	require.NoError(t, mgr.Deny(pending.ID))
	require.ErrorIs(t, <-result, ErrDenied)
}

func TestPersistAutoApproveRuleCreatesPermanentRule(t *testing.T) {
	mgr := NewManager(ManagerConfig{HistoryMax: 10, AutoApproveDuration: time.Minute})
	req := managedRuleRequest("/usr/bin/gh")
	temporaryID := mgr.AddAutoApproveRule(req)
	rule, err := mgr.PersistAutoApproveRule(temporaryID)
	require.NoError(t, err)
	assert.True(t, rule.Enabled)
	assert.True(t, rule.Process.Direct)
	assert.Empty(t, mgr.ListAutoApproveRules())
	assert.Len(t, mgr.ListManagedTrustRules(), 1)
}

func TestPersistAutoApproveRuleConcurrentIsIdempotent(t *testing.T) {
	mgr := NewManager(ManagerConfig{HistoryMax: 10, AutoApproveDuration: time.Minute})
	temporaryID := mgr.AddAutoApproveRule(managedRuleRequest("/usr/bin/gh"))
	const workers = 16
	results := make(chan ManagedTrustRule, workers)
	errors := make(chan error, workers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			rule, err := mgr.PersistAutoApproveRule(temporaryID)
			results <- rule
			errors <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	var persistedID string
	for rule := range results {
		if persistedID == "" {
			persistedID = rule.ID
		}
		assert.Equal(t, persistedID, rule.ID)
	}
	assert.Len(t, mgr.ListManagedTrustRules(), 1)
	assert.Empty(t, mgr.ListAutoApproveRules())
}

func TestPersistAutoApproveRuleFailureRestoresTemporaryRule(t *testing.T) {
	storeErr := errors.New("disk unavailable")
	mgr := NewManager(ManagerConfig{HistoryMax: 10, AutoApproveDuration: time.Minute, ManagedRuleStore: &recordingManagedStore{err: storeErr}})
	temporaryID := mgr.AddAutoApproveRule(managedRuleRequest("/usr/bin/gh"))
	_, err := mgr.PersistAutoApproveRule(temporaryID)
	require.ErrorIs(t, err, storeErr)
	assert.Empty(t, mgr.ListManagedTrustRules())
	rules := mgr.ListAutoApproveRules()
	require.Len(t, rules, 1)
	assert.Equal(t, temporaryID, rules[0].ID)
}

func TestPersistAutoApproveRuleConcurrentDeleteHasSingleWinner(t *testing.T) {
	for range 50 {
		mgr := NewManager(ManagerConfig{HistoryMax: 10, AutoApproveDuration: time.Minute})
		temporaryID := mgr.AddAutoApproveRule(managedRuleRequest("/usr/bin/gh"))
		start := make(chan struct{})
		persistResult := make(chan error, 1)
		deleteResult := make(chan error, 1)
		go func() {
			<-start
			_, err := mgr.PersistAutoApproveRule(temporaryID)
			persistResult <- err
		}()
		go func() {
			<-start
			deleteResult <- mgr.RemoveAutoApproveRule(temporaryID)
		}()
		close(start)
		persistErr := <-persistResult
		deleteErr := <-deleteResult
		assert.True(t, (persistErr == nil) != (deleteErr == nil), "persist=%v delete=%v", persistErr, deleteErr)
		if persistErr == nil {
			assert.ErrorIs(t, deleteErr, ErrNotFound)
			assert.Len(t, mgr.ListManagedTrustRules(), 1)
		} else {
			assert.ErrorIs(t, persistErr, ErrNotFound)
			require.NoError(t, deleteErr)
			assert.Empty(t, mgr.ListManagedTrustRules())
		}
		assert.Empty(t, mgr.ListAutoApproveRules())
	}
}

func TestTemporaryDecisionAttributionUsesAuthoritativeExecutable(t *testing.T) {
	rule := &AutoApproveRule{ID: "temporary", InvokerName: "spoofed.service", InvokerExe: "/usr/bin/gh", RequestType: RequestTypeGetSecret}
	attribution := NewTemporaryDecisionAttribution(rule)
	require.NotNil(t, attribution.Process)
	assert.Equal(t, "/usr/bin/gh", attribution.Process.Exe)
	assert.True(t, attribution.Process.Direct)
	assert.Empty(t, attribution.Process.Unit)
}

func TestTemporaryRuleRecallsPendingSearchExactlyOnce(t *testing.T) {
	mgr := NewManager(ManagerConfig{Timeout: time.Second, HistoryMax: 10, AutoApproveDuration: time.Minute})
	sender := SenderInfo{PID: 42, ProcessChain: []ProcessInfo{{PID: 42, Exe: "/usr/bin/gh"}}}
	attrs := map[string]string{"service": "github"}
	result := make(chan struct {
		auto bool
		err  error
	}, 1)
	go func() {
		auto, err := mgr.RequireApproval(context.Background(), "client", nil, "", RequestTypeSearch, attrs, sender)
		result <- struct {
			auto bool
			err  error
		}{auto, err}
	}()
	require.Eventually(t, func() bool { return mgr.PendingCount() == 1 }, time.Second, time.Millisecond)
	mgr.AddAutoApproveRule(&Request{Type: RequestTypeSearch, SearchAttributes: attrs, SenderInfo: sender})
	got := <-result
	require.NoError(t, got.err)
	assert.True(t, got.auto)
	require.Eventually(t, func() bool { return mgr.PendingCount() == 0 }, time.Second, time.Millisecond)
	history := mgr.History()
	require.Len(t, history, 1)
	assert.Equal(t, ResolutionAutoApproved, history[0].Resolution)
	assert.Equal(t, attrs, history[0].Request.Attribution.SearchAttributes)
}

func TestTemporaryRuleInstallRegistrationRace(t *testing.T) {
	for range 100 {
		mgr := NewManager(ManagerConfig{Timeout: 200 * time.Millisecond, HistoryMax: 10, AutoApproveDuration: time.Minute})
		observer := &testObserver{}
		mgr.Subscribe(observer)
		req := managedRuleRequest("/usr/bin/gh")
		start := make(chan struct{})
		result := make(chan struct {
			auto bool
			err  error
		}, 1)
		added := make(chan struct{})
		go func() {
			<-start
			auto, err := mgr.RequireApproval(context.Background(), "client", req.Items, "", req.Type, nil, req.SenderInfo)
			result <- struct {
				auto bool
				err  error
			}{auto, err}
		}()
		go func() {
			<-start
			mgr.AddAutoApproveRule(req)
			close(added)
		}()
		close(start)
		got := <-result
		require.NoError(t, got.err)
		assert.True(t, got.auto)
		<-added
		require.Len(t, mgr.History(), 1)
		assert.Equal(t, ResolutionAutoApproved, mgr.History()[0].Resolution)
		createdIndex, resolvedIndex := -1, -1
		for i, event := range observer.Events() {
			if event.Type == EventRequestCreated {
				createdIndex = i
			}
			if event.Type == EventRequestAutoApproved {
				resolvedIndex = i
			}
		}
		assert.NotEqual(t, -1, resolvedIndex)
		if createdIndex >= 0 {
			assert.Less(t, createdIndex, resolvedIndex)
		}
	}
}
