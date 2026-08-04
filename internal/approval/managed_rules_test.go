package approval

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validManagedRule() ManagedTrustRule {
	return ManagedTrustRule{
		ID:        uuid.New().String(),
		CreatedAt: time.Now().UTC(),
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
		{"write", func(r *ManagedTrustRule) { r.RequestTypes = []string{string(RequestTypeWrite)} }},
		{"gpg", func(r *ManagedTrustRule) { r.RequestTypes = []string{string(RequestTypeGPGSign)} }},
		{"not direct", func(r *ManagedTrustRule) { r.Process.Direct = false }},
		{"missing exe", func(r *ManagedTrustRule) { r.Process.Exe = "" }},
		{"relative exe", func(r *ManagedTrustRule) { r.Process.Exe = "bin/gh" }},
		{"name fallback", func(r *ManagedTrustRule) { r.Process.Name = "gh" }},
		{"wildcard exe", func(r *ManagedTrustRule) { r.Process.Exe = "/usr/bin/*" }},
		{"wildcard attribute", func(r *ManagedTrustRule) { r.Secret.Attributes["service"] = "git*" }},
		{"missing attributes", func(r *ManagedTrustRule) { r.Secret.Attributes = nil }},
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
		{"write", func(r *Request) { r.Type = RequestTypeWrite }},
		{"multiple items", func(r *Request) { r.Items = append(r.Items, r.Items[0]) }},
		{"no attributes", func(r *Request) { r.Items[0].Attributes = nil }},
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

func TestCreateManagedTrustRuleRequiresManualApproval(t *testing.T) {
	mgr := NewManager(ManagerConfig{HistoryMax: 10})
	req := managedRuleRequest("/usr/bin/gh")
	mgr.AddHistoryEntry(HistoryEntry{Request: req, Resolution: ResolutionAutoApproved})
	_, _, err := mgr.CreateManagedTrustRuleFromRequest(req.ID)
	require.ErrorIs(t, err, ErrInvalidManagedRule)
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
