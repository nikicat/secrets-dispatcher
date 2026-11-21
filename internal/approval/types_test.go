package approval

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

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
