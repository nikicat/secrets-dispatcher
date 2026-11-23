package api

import (
	"testing"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/proxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Checks that approving a gpg_sign request "and auto-approve" approves it with
// gpg's result, failure included, and records a rule only for a signature.
func TestResolverApproveAndAutoApprove_GPGSign(t *testing.T) {
	local := approval.LocalSenderForTest("git", "/usr/bin/git")
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
			mgr := approval.NewManagerForTest(approval.ManagerConfig{})
			r := NewResolver(mgr, proxy.SlowUpstream{})
			r.GPGRunner = &fakeGPGRunner{res: tt.res}
			id := mgr.Create(approval.CommitSignRequestForTest(local, "repo"))

			require.NoError(t, r.ApproveAndAutoApprove(id))

			_, ok := mgr.GetPending(id)
			assert.False(t, ok, "the request is resolved")
			assert.Equal(t, tt.wantRule, len(mgr.ListAutoApproveRules()) == 1)
		})
	}
}

// Checks that a gpg_sign request that Request.AutoApprovable rejects is refused
// before gpg runs, and that it stays pending.
func TestResolverApproveAndAutoApprove_GPGSignWithoutExe(t *testing.T) {
	mgr := approval.NewManagerForTest(approval.ManagerConfig{})
	r := NewResolver(mgr, proxy.SlowUpstream{})
	r.GPGRunner = &fakeGPGRunner{res: approval.GPGResult{Signature: []byte("SIG")}}
	id := mgr.Create(approval.CommitSignRequestForTest(approval.RemoteSenderForTest("git"), "repo"))

	require.ErrorIs(t, r.ApproveAndAutoApprove(id), approval.ErrNotAutoApprovable)

	_, ok := mgr.GetPending(id)
	assert.True(t, ok, "the request stays pending")
	assert.Empty(t, mgr.ListAutoApproveRules())
}
