package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func apiManagedRule() approval.ManagedTrustRule {
	return approval.ManagedTrustRule{
		ID:        "5cdad1eb-a0ef-4e61-ab62-a80a4da95da8",
		Enabled:   true,
		CreatedAt: time.Now().UTC(),
		UpdatedAt: time.Now().UTC(),
		TrustRule: approval.TrustRule{
			Name:         "gh: GitHub token",
			Action:       "approve",
			RequestTypes: []string{string(approval.RequestTypeGetSecret)},
			Process:      &approval.ProcessMatcher{Exe: "/usr/bin/gh", Direct: true},
			Secret: &approval.SecretMatcher{
				Collection: "login",
				Attributes: map[string]string{"service": "github"},
			},
		},
	}
}

func apiApprovedHistory(requestID string) approval.HistoryEntry {
	return approval.HistoryEntry{
		Request: &approval.Request{
			ID:   requestID,
			Type: approval.RequestTypeGetSecret,
			Items: []approval.ItemInfo{{
				Path:       "/org/freedesktop/secrets/collection/login/item1",
				Label:      "GitHub token",
				Attributes: map[string]string{"service": "github"},
			}},
			SenderInfo: approval.SenderInfo{
				PID:         101,
				SystemdUnit: "app.service",
				ProcessChain: []approval.ProcessInfo{{
					Name: "gh", PID: 101, Exe: "/usr/bin/gh", Args: []string{"gh"},
				}},
			},
		},
		Resolution: approval.ResolutionApproved,
		ResolvedAt: time.Now(),
	}
}

func TestManagedTrustRuleHandlers(t *testing.T) {
	t.Run("list returns empty array", func(t *testing.T) {
		mgr := approval.NewManager(approval.ManagerConfig{HistoryMax: 10})
		h := testHandlers(t, mgr)
		rec := httptest.NewRecorder()
		h.HandleManagedTrustRuleList(rec, httptest.NewRequest(http.MethodGet, "/api/v1/approval-rules", nil))
		require.Equal(t, http.StatusOK, rec.Code)
		assert.JSONEq(t, `[]`, rec.Body.String())
	})

	t.Run("create and duplicate", func(t *testing.T) {
		mgr := approval.NewManager(approval.ManagerConfig{HistoryMax: 10})
		mgr.AddHistoryEntry(apiApprovedHistory("approved-1"))
		h := testHandlers(t, mgr)

		body := []byte(`{"request_id":"approved-1"}`)
		rec := httptest.NewRecorder()
		h.HandleManagedTrustRuleCreateFromRequest(rec, httptest.NewRequest(http.MethodPost, "/api/v1/approval-rules/from-request", bytes.NewReader(body)))
		require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
		var created approval.ManagedTrustRule
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&created))
		require.NotEmpty(t, created.ID)

		rec = httptest.NewRecorder()
		h.HandleManagedTrustRuleCreateFromRequest(rec, httptest.NewRequest(http.MethodPost, "/api/v1/approval-rules/from-request", bytes.NewReader(body)))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var existing approval.ManagedTrustRule
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&existing))
		assert.Equal(t, created.ID, existing.ID)
		assert.Len(t, mgr.ListManagedTrustRules(), 1)
	})

	t.Run("create validation errors", func(t *testing.T) {
		mgr := approval.NewManager(approval.ManagerConfig{HistoryMax: 10})
		h := testHandlers(t, mgr)

		tests := []struct {
			name string
			body string
			want int
		}{
			{"malformed", `{`, http.StatusBadRequest},
			{"missing id", `{}`, http.StatusBadRequest},
			{"not found", `{"request_id":"missing"}`, http.StatusNotFound},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				rec := httptest.NewRecorder()
				h.HandleManagedTrustRuleCreateFromRequest(rec, httptest.NewRequest(http.MethodPost, "/api/v1/approval-rules/from-request", bytes.NewBufferString(tc.body)))
				assert.Equal(t, tc.want, rec.Code, rec.Body.String())
			})
		}

		unsafe := apiApprovedHistory("unsafe")
		unsafe.Resolution = approval.ResolutionAutoApproved
		mgr.AddHistoryEntry(unsafe)
		rec := httptest.NewRecorder()
		h.HandleManagedTrustRuleCreateFromRequest(rec, httptest.NewRequest(http.MethodPost, "/api/v1/approval-rules/from-request", bytes.NewBufferString(`{"request_id":"unsafe"}`)))
		assert.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())
	})

	t.Run("delete", func(t *testing.T) {
		rule := apiManagedRule()
		mgr := approval.NewManager(approval.ManagerConfig{HistoryMax: 10, ManagedTrustRules: []approval.ManagedTrustRule{rule}})
		h := testHandlers(t, mgr)

		rec := httptest.NewRecorder()
		h.HandleManagedTrustRuleDelete(rec, httptest.NewRequest(http.MethodDelete, "/api/v1/approval-rules/"+rule.ID, nil))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		assert.JSONEq(t, `{"status":"deleted"}`, rec.Body.String())

		rec = httptest.NewRecorder()
		h.HandleManagedTrustRuleDelete(rec, httptest.NewRequest(http.MethodDelete, "/api/v1/approval-rules/"+rule.ID, nil))
		assert.Equal(t, http.StatusNotFound, rec.Code)

		rec = httptest.NewRecorder()
		h.HandleManagedTrustRuleDelete(rec, httptest.NewRequest(http.MethodDelete, "/api/v1/approval-rules/bad/path", nil))
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("wrong methods", func(t *testing.T) {
		mgr := approval.NewManager(approval.ManagerConfig{HistoryMax: 10})
		h := testHandlers(t, mgr)
		for _, call := range []struct {
			handler http.HandlerFunc
			path    string
		}{
			{h.HandleManagedTrustRuleList, "/api/v1/approval-rules"},
			{h.HandleManagedTrustRuleCreateFromRequest, "/api/v1/approval-rules/from-request"},
			{h.HandleManagedTrustRuleDelete, "/api/v1/approval-rules/id"},
		} {
			rec := httptest.NewRecorder()
			call.handler(rec, httptest.NewRequest(http.MethodPatch, call.path, nil))
			assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
		}
	})
}

type failingManagedRuleStore struct {
	err error
}

func (s failingManagedRuleStore) Load() ([]approval.ManagedTrustRule, error) { return nil, s.err }
func (s failingManagedRuleStore) Save([]approval.ManagedTrustRule) error     { return s.err }

func TestManagedTrustRuleHandlersPersistenceErrors(t *testing.T) {
	storeErr := errors.New("storage unavailable")
	mgr := approval.NewManager(approval.ManagerConfig{HistoryMax: 10, ManagedRuleStore: failingManagedRuleStore{err: storeErr}})
	mgr.AddHistoryEntry(apiApprovedHistory("approved-1"))
	h := testHandlers(t, mgr)

	rec := httptest.NewRecorder()
	h.HandleManagedTrustRuleCreateFromRequest(rec, httptest.NewRequest(http.MethodPost, "/api/v1/approval-rules/from-request", bytes.NewBufferString(`{"request_id":"approved-1"}`)))
	assert.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	assert.Empty(t, mgr.ListManagedTrustRules())

	rule := apiManagedRule()
	mgr = approval.NewManager(approval.ManagerConfig{HistoryMax: 10, ManagedTrustRules: []approval.ManagedTrustRule{rule}, ManagedRuleStore: failingManagedRuleStore{err: storeErr}})
	h = testHandlers(t, mgr)
	rec = httptest.NewRecorder()
	h.HandleManagedTrustRuleDelete(rec, httptest.NewRequest(http.MethodDelete, "/api/v1/approval-rules/"+rule.ID, nil))
	assert.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	assert.Len(t, mgr.ListManagedTrustRules(), 1)
}

func TestManagedTrustRuleRoutesRequireAuthenticationAndRouteCRUD(t *testing.T) {
	mgr := approval.NewManager(approval.ManagerConfig{Timeout: time.Second, HistoryMax: 10})
	mgr.AddHistoryEntry(apiApprovedHistory("approved-1"))
	auth, err := NewAuth(t.TempDir())
	require.NoError(t, err)
	server, err := NewServer("127.0.0.1:0", mgr, "/socket", "client", auth, "", false, nil, 0)
	require.NoError(t, err)
	require.NoError(t, server.Start())
	defer server.Shutdown(context.Background())

	baseURL := "http://" + server.Addr()
	client := &http.Client{Timeout: 5 * time.Second}
	for _, request := range []*http.Request{
		mustRequest(t, http.MethodGet, baseURL+"/api/v1/approval-rules", nil),
		mustRequest(t, http.MethodPost, baseURL+"/api/v1/approval-rules", bytes.NewBufferString(`{"request_types":["search"],"process":{"name":"browser"}}`)),
		mustRequest(t, http.MethodPost, baseURL+"/api/v1/approval-rules/from-request", bytes.NewBufferString(`{"request_id":"approved-1"}`)),
		mustRequest(t, http.MethodPut, baseURL+"/api/v1/approval-rules/unknown", bytes.NewBufferString(`{}`)),
		mustRequest(t, http.MethodDelete, baseURL+"/api/v1/approval-rules/unknown", nil),
		mustRequest(t, http.MethodPost, baseURL+"/api/v1/auto-approve/unknown/persist", nil),
	} {
		resp, err := client.Do(request)
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	}

	post := func() *http.Response {
		req := mustRequest(t, http.MethodPost, baseURL+"/api/v1/approval-rules/from-request", bytes.NewBufferString(`{"request_id":"approved-1"}`))
		req.Header.Set("Authorization", "Bearer "+auth.Token())
		resp, err := client.Do(req)
		require.NoError(t, err)
		return resp
	}
	resp := post()
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created approval.ManagedTrustRule
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	resp.Body.Close()

	resp = post()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var duplicate approval.ManagedTrustRule
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&duplicate))
	resp.Body.Close()
	assert.Equal(t, created.ID, duplicate.ID)

	req := mustRequest(t, http.MethodGet, baseURL+"/api/v1/approval-rules", nil)
	req.Header.Set("Authorization", "Bearer "+auth.Token())
	resp, err = client.Do(req)
	require.NoError(t, err)
	var listed []approval.ManagedTrustRule
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&listed))
	resp.Body.Close()
	require.Len(t, listed, 1)
	assert.Equal(t, created.ID, listed[0].ID)

	req = mustRequest(t, http.MethodDelete, baseURL+"/api/v1/approval-rules/"+created.ID, nil)
	req.Header.Set("Authorization", "Bearer "+auth.Token())
	resp, err = client.Do(req)
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	assert.Empty(t, mgr.ListManagedTrustRules())

	req = mustRequest(t, http.MethodPost, baseURL+"/api/v1/approval-rules", bytes.NewBufferString(`{"name":"browser search","request_types":["search"],"process":{"name":"browser-*"},"search_attributes":{"service":"git*"}}`))
	req.Header.Set("Authorization", "Bearer "+auth.Token())
	resp, err = client.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var manual approval.ManagedTrustRule
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&manual))
	resp.Body.Close()
	manual.Enabled = false
	body, err = json.Marshal(manual)
	require.NoError(t, err)
	req = mustRequest(t, http.MethodPut, baseURL+"/api/v1/approval-rules/"+manual.ID, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+auth.Token())
	resp, err = client.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var disabled approval.ManagedTrustRule
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&disabled))
	resp.Body.Close()
	assert.False(t, disabled.Enabled)
}

func mustRequest(t *testing.T, method, url string, body io.Reader) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, url, body)
	require.NoError(t, err)
	return req
}

func TestDecisionAttributionConversionAndInjection(t *testing.T) {
	attribution := &approval.DecisionAttribution{
		Source: "managed_rule", RuleID: "rule-1", RuleName: "gh token", Action: "approve",
	}
	entry := apiApprovedHistory("approved-1")
	entry.Request.Attribution = attribution
	converted := convertHistoryEntry(entry)
	assert.Equal(t, attribution, converted.Request.Attribution)

	mgr := approval.NewManager(approval.ManagerConfig{HistoryMax: 10})
	h := testHandlers(t, mgr)
	h.SetTestMode(true)
	body, err := json.Marshal(converted)
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	h.HandleTestInjectHistory(rec, httptest.NewRequest(http.MethodPost, "/api/v1/test/history", bytes.NewReader(body)))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	history := mgr.History()
	require.Len(t, history, 1)
	assert.Equal(t, attribution, history[0].Request.Attribution)
	assert.Equal(t, entry.Request.SenderInfo.SystemdUnit, history[0].Request.SenderInfo.SystemdUnit)
}

func TestManagedTrustRuleCreateUpdateAndPersistHandlers(t *testing.T) {
	mgr := approval.NewManager(approval.ManagerConfig{Timeout: time.Second, HistoryMax: 10, AutoApproveDuration: time.Minute})
	h := testHandlers(t, mgr)

	createBody := bytes.NewBufferString(`{"name":"browser","request_types":["search"],"process":{"name":"browser-*"},"search_attributes":{"service":"git*"}}`)
	rec := httptest.NewRecorder()
	h.HandleManagedTrustRuleCreate(rec, httptest.NewRequest(http.MethodPost, "/api/v1/approval-rules", createBody))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var created approval.ManagedTrustRule
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&created))
	assert.True(t, created.Enabled)

	created.Enabled = false
	body, err := json.Marshal(created)
	require.NoError(t, err)
	rec = httptest.NewRecorder()
	h.HandleManagedTrustRuleUpdate(rec, httptest.NewRequest(http.MethodPut, "/api/v1/approval-rules/"+created.ID, bytes.NewReader(body)))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var updated approval.ManagedTrustRule
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&updated))
	assert.False(t, updated.Enabled)

	temporaryID := mgr.AddAutoApproveRule(apiApprovedHistory("temporary").Request)
	rec = httptest.NewRecorder()
	h.HandleAutoApprovePersist(rec, httptest.NewRequest(http.MethodPost, "/api/v1/auto-approve/"+temporaryID+"/persist", nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Empty(t, mgr.ListAutoApproveRules())
	assert.Len(t, mgr.ListManagedTrustRules(), 2)

	rec = httptest.NewRecorder()
	h.HandleManagedTrustRuleCreate(rec, httptest.NewRequest(http.MethodPost, "/api/v1/approval-rules", bytes.NewBufferString(`{"enabled":false,"request_types":["get_secret"],"process":{"name":"disabled-*"}}`)))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var disabled approval.ManagedTrustRule
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&disabled))
	assert.False(t, disabled.Enabled)
}

func TestManagedTrustRuleCreateUpdateRejectUnknownAndTrailingJSON(t *testing.T) {
	mgr := approval.NewManager(approval.ManagerConfig{HistoryMax: 10})
	h := testHandlers(t, mgr)

	badCreateBodies := []string{
		`{"request_types":["get_secret"],"process":{"name":"app"},"search_atributes":{"service":"*"}}`,
		`{"request_types":["get_secret"],"process":{"name":"app","nam":"*"}}`,
		`{"request_types":["get_secret"],"process":{"name":"app"}} {"request_types":["get_secret"],"process":{"name":"*"}}`,
	}
	for _, body := range badCreateBodies {
		rec := httptest.NewRecorder()
		h.HandleManagedTrustRuleCreate(rec, httptest.NewRequest(http.MethodPost, "/api/v1/approval-rules", bytes.NewBufferString(body)))
		assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	}
	assert.Empty(t, mgr.ListManagedTrustRules())

	created, err := mgr.CreateManagedTrustRule(approval.ManagedTrustRule{Enabled: true, TrustRule: approval.TrustRule{
		Name:         "bounded",
		RequestTypes: []string{string(approval.RequestTypeGetSecret)},
		Process:      &approval.ProcessMatcher{Name: "safe-app"},
		Secret:       &approval.SecretMatcher{Collection: "login"},
	}})
	require.NoError(t, err)
	badUpdateBodies := []string{
		`{"enabled":true,"request_types":["get_secret"],"process":{"name":"*"},"secrect":{"collection":"login"}}`,
		`{"enabled":true,"request_types":["get_secret"],"process":{"name":"*"}} null`,
	}
	for _, body := range badUpdateBodies {
		rec := httptest.NewRecorder()
		h.HandleManagedTrustRuleUpdate(rec, httptest.NewRequest(http.MethodPut, "/api/v1/approval-rules/"+created.ID, bytes.NewBufferString(body)))
		assert.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	}
	listed := mgr.ListManagedTrustRules()
	require.Len(t, listed, 1)
	assert.Equal(t, "safe-app", listed[0].Process.Name)
	assert.Equal(t, "login", listed[0].Secret.Collection)
}
