package api

import (
	"testing"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Checks that approving a gpg_sign request "and auto-approve" approves it with
// gpg's result, failure included, and records a rule only for a signature.
func TestResolverApproveAndAutoApprove_GPGSign(t *testing.T) {
	tests := []struct {
		name     string
		res      approval.GPGResult
		wantRule bool
	}{
		{"signed", approval.GPGResult{Signature: []byte("SIG")}, true},
		{"gpg failed", approval.GPGResult{Status: []byte("[GNUPG:] ERROR"), ExitCode: 2}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr := newTestManager()
			r := NewResolver(mgr, nil, 0)
			r.GPGRunner = &fakeGPGRunner{res: tt.res}
			id, err := mgr.CreateGPGSignRequest("test-client", &approval.GPGSignInfo{KeyID: "ABCD", CommitObject: "tree 0\n"}, approval.SenderInfo{})
			require.NoError(t, err)

			require.NoError(t, r.ApproveAndAutoApprove(id))

			assert.Nil(t, mgr.GetPending(id), "the request is resolved")
			assert.Equal(t, tt.wantRule, len(mgr.ListAutoApproveRules()) == 1)
		})
	}
}
