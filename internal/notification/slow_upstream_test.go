package notification

import (
	"testing"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/procutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSlowUpstreamNotifier_NotifiesAndDismisses(t *testing.T) {
	mock := &mockNotifier{}
	n := newSlowUpstreamNotifier(mock)

	dismiss := n.NotifySlowUpstream(approval.RequestInfo{Items: []approval.ItemInfo{{Label: "my-secret", Path: "/test/item"}}})

	require.Equal(t, 1, mock.notifyCount())
	assert.Equal(t, notifyCall{summary: "Waiting for keyring unlock", body: "my-secret", icon: "dialog-password"}, mock.lastNotify())

	dismiss()

	require.Equal(t, 1, mock.closeCount())
	assert.Equal(t, mock.nextID, mock.lastClosed())
}

func TestSlowUpstreamSummary(t *testing.T) {
	tests := []struct {
		reqType approval.RequestType
		want    string
	}{
		{approval.RequestTypeGPGSign, "Signing commit"},
		{approval.RequestTypeSSHSign, "SSH key signing"},
		{approval.RequestTypeSearch, "Searching keyring"},
		{approval.RequestTypeUnlock, "Unlocking keyring"},
		{approval.RequestTypeGetSecret, "Waiting for keyring unlock"},
	}
	for _, tt := range tests {
		t.Run(string(tt.reqType), func(t *testing.T) {
			assert.Equal(t, tt.want, slowUpstreamSummary(tt.reqType))
		})
	}
}

func TestFormatSlowUpstreamBody(t *testing.T) {
	chain := func(names ...string) approval.SenderInfo {
		procs := make([]procutil.Process, len(names))
		for i, name := range names {
			procs[i] = procutil.Process{Name: name, PID: procutil.PID(1000 + i)}
		}
		return approval.SenderInfo{ProcessChain: procs}
	}
	tests := []struct {
		name string
		req  approval.RequestInfo
		want string
	}{
		{
			name: "single item",
			req:  approval.RequestInfo{Items: []approval.ItemInfo{{Label: "my-secret", Path: "/test/item"}}},
			want: "my-secret",
		},
		{
			name: "several items",
			req:  approval.RequestInfo{Items: []approval.ItemInfo{{Label: "secret-a"}, {Label: "secret-b"}, {Label: "secret-c"}}},
			want: "secret-a, secret-b, secret-c (3 items)",
		},
		{
			name: "no items",
			req:  approval.RequestInfo{},
			want: "",
		},
		{
			name: "path when the label is missing",
			req:  approval.RequestInfo{Items: []approval.ItemInfo{{Path: "/org/freedesktop/secrets/collection/Login"}}},
			want: "/org/freedesktop/secrets/collection/Login",
		},
		{
			name: "path for the unlabelled item among several",
			req: approval.RequestInfo{Items: []approval.ItemInfo{
				{Label: "Login", Path: "/org/freedesktop/secrets/collection/Login"},
				{Path: "/org/freedesktop/secrets/collection/Other"},
			}},
			want: "Login, /org/freedesktop/secrets/collection/Other (2 items)",
		},
		{
			name: "item and process chain",
			req:  approval.RequestInfo{Items: []approval.ItemInfo{{Label: "my-password"}}, SenderInfo: chain("git", "zsh", "claude")},
			want: "my-password\ngit ← zsh ← claude",
		},
		{
			name: "gpg sign item and process chain",
			req:  approval.RequestInfo{Type: approval.RequestTypeGPGSign, Items: []approval.ItemInfo{{Label: "my-repo: fix bug"}}, SenderInfo: chain("git", "zsh")},
			want: "my-repo: fix bug\ngit ← zsh",
		},
		{
			name: "process chain only",
			req:  approval.RequestInfo{SenderInfo: chain("secret-tool")},
			want: "secret-tool",
		},
		{
			name: "escaped markup",
			req:  approval.RequestInfo{Items: []approval.ItemInfo{{Label: "<b>&"}}, SenderInfo: chain("git<", "zsh")},
			want: "&lt;b&gt;&amp;\ngit&lt; ← zsh",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, formatSlowUpstreamBody(tt.req))
		})
	}
}
