package approval

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/unixsock"
)

func TestClientFromSocket(t *testing.T) {
	tests := []struct {
		path unixsock.Path
		want ClientName
	}{
		{"/path/to/myserver.sock", "myserver"},
		{"relative/path.sock", "path"},
		{"/run/myserver", "myserver"},
	}
	for _, tc := range tests {
		got, err := ClientFromSocket(tc.path)
		assert.NoError(t, err, tc.path)
		assert.Equal(t, tc.want, got, tc.path)
	}

	for _, path := range []unixsock.Path{"/run/local.sock", "/run/ssh-agent.sock", "/run/unknown", "/run/local", "/run/.sock", "/run/local.sock/"} {
		_, err := ClientFromSocket(path)
		assert.Error(t, err, path)
	}
}

func TestTrustedSignerValidate(t *testing.T) {
	assert.NoError(t, TrustedSigner{ExePath: "/usr/bin/nvim"}.Validate())
	assert.EqualError(t, TrustedSigner{RepoName: "dotfiles"}.Validate(), "exe_path is required")
}

func TestTrustRuleValidate(t *testing.T) {
	tests := []struct {
		name    string
		rule    TrustRule
		wantErr string
	}{
		{name: "approve with no request types", rule: TrustRule{Action: RuleApprove}},
		{name: "deny a search", rule: TrustRule{Action: RuleDeny, RequestTypes: []RequestType{RequestTypeSearch}}},
		{name: "ignore a write", rule: TrustRule{Action: RuleIgnore, RequestTypes: []RequestType{RequestTypeWrite}}},
		{
			name:    "signing request type",
			rule:    TrustRule{Action: RuleApprove, RequestTypes: []RequestType{RequestTypeGPGSign}},
			wantErr: `invalid request_type "gpg_sign"`,
		},
		{
			name:    "ignore with no request types",
			rule:    TrustRule{Action: RuleIgnore},
			wantErr: "requires non-empty request_types",
		},
		{
			name:    "ignore a non-write type",
			rule:    TrustRule{Action: RuleIgnore, RequestTypes: []RequestType{RequestTypeWrite, RequestTypeDelete}},
			wantErr: `got "delete"`,
		},
		{
			name:    "zero attribute glob",
			rule:    TrustRule{Secret: &SecretMatcher{Attributes: map[string]Glob{"service": {}}}},
			wantErr: "secret.attributes: service: empty glob; omit the key",
		},
		{
			name:    "zero search attribute glob",
			rule:    TrustRule{SearchAttributes: map[string]Glob{"service": {}}},
			wantErr: "search_attributes: service: empty glob; omit the key",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.rule.Validate()
			if tc.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.ErrorContains(t, err, tc.wantErr)
			}
		})
	}
}

// A null attribute value decodes to the zero Glob, which Validate rejects.
func TestValidateRejectsNullAttributeGlob(t *testing.T) {
	var r TrustRule
	require.NoError(t, json.Unmarshal([]byte(`{"secret": {"attributes": {"service": null}}}`), &r))
	assert.ErrorIs(t, r.Validate(), errEmptyGlob)

	r = TrustRule{}
	require.NoError(t, yaml.Unmarshal([]byte("search_attributes:\n  service:\n"), &r))
	assert.ErrorIs(t, r.Validate(), errEmptyGlob)
}

func TestRequestInfo_PayloadFollowsType(t *testing.T) {
	sign := CommitSignInfoForTest("repo", "fix")
	attrs := dbustypes.Attributes{"service": "gh"}

	got, ok := RequestInfo{Type: RequestTypeGPGSign, Payload: sign}.GPGSign()
	assert.True(t, ok)
	assert.Same(t, sign, got)
	_, ok = RequestInfo{Type: RequestTypeGetSecret, Payload: sign}.GPGSign()
	assert.False(t, ok)
	_, ok = RequestInfo{Type: RequestTypeGPGSign, Payload: (*GPGSignInfo)(nil)}.GPGSign()
	assert.False(t, ok)

	search, ok := RequestInfo{Type: RequestTypeSearch, Payload: SearchPayload{attrs}}.Search()
	assert.True(t, ok)
	assert.Equal(t, attrs, search.Attributes)
	_, ok = RequestInfo{Type: RequestTypeGPGSign, Payload: SearchPayload{attrs}}.Search()
	assert.False(t, ok)
}

func TestTrustRuleSearchAttributesMatchOnlySearch(t *testing.T) {
	r := TrustRule{Action: RuleApprove, SearchAttributes: map[string]Glob{"service": MustGlob("*")}}
	local := SenderInfo{Client: ClientLocal}

	assert.True(t, r.matches(RequestInfo{Type: RequestTypeSearch, Payload: SearchPayload{map[string]string{"service": "gh"}}, SenderInfo: local}))
	assert.True(t, r.matches(RequestInfo{Type: RequestTypeSearch, Payload: SearchPayload{}, SenderInfo: local}), "a missing attribute reads as empty")
	for _, rt := range []RequestType{RequestTypeGetSecret, RequestTypeDelete, RequestTypeWrite, RequestTypeUnlock} {
		assert.False(t, r.matches(RequestInfo{Type: rt, SenderInfo: local}), rt)
	}
}

// A collection glob matches only items that lie in a collection, so a "*"
// does not reach an ssh_sign key.
func TestSecretMatcherCollectionNeedsCollection(t *testing.T) {
	sm := &SecretMatcher{Collection: MustGlob("*")}
	assert.True(t, sm.matchesItem(ItemInfo{Path: "/org/freedesktop/secrets/collection/login/1"}))
	assert.False(t, sm.matchesItem(SSHSignItem("SHA256:abc", "me@host", nil)))
}
