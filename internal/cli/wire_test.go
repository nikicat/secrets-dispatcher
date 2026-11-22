package cli

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nikicat/secrets-dispatcher/internal/api"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/procutil"
	"github.com/nikicat/secrets-dispatcher/internal/sender"
)

// Encodes the given value as the daemon does, and decodes it into a new T as
// cli does.
func decodeAsCLI[T any](t *testing.T, in any) T {
	t.Helper()
	raw, err := json.Marshal(in)
	require.NoError(t, err)
	var got T
	require.NoError(t, json.Unmarshal(raw, &got))
	return got
}

var (
	wireCreated  = time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	wireExpires  = wireCreated.Add(5 * time.Minute)
	wireResolved = wireCreated.Add(time.Minute)
)

// Returns an api.RequestRecord with every field set, and the cli.RequestRecord
// that a decode of it must give.
func wireRecords(sign *approval.GPGSignInfo, want *GPGSignInfo) (api.RequestRecord, RequestRecord) {
	uid := procutil.UID(1000)
	senderInfo := sender.Info{
		Sender:      new(sender.UniqueName(":1.42")),
		PID:         42,
		UID:         &uid,
		InvokerName: "git",
		SystemdUnit: "app.service",
	}
	items := []api.ItemInfo{{Path: "/org/freedesktop/secrets/collection/login/1", Label: "pw", Attributes: map[string]string{"service": "ssh"}}}
	return api.RequestRecord{
		ID:               "req-1",
		Client:           "local",
		Items:            items,
		Session:          "/org/freedesktop/secrets/session/s1",
		CreatedAt:        wireCreated,
		Type:             approval.RequestTypeGPGSign,
		SearchAttributes: map[string]string{"service": "ssh"},
		SenderInfo:       senderInfo,
		GPGSignInfo:      sign,
		AutoApprovable:   true,
	}, RequestRecord{
		ID:               "req-1",
		Client:           "local",
		Items:            []ItemInfo{{Path: items[0].Path, Label: items[0].Label, Attributes: items[0].Attributes}},
		Session:          "/org/freedesktop/secrets/session/s1",
		CreatedAt:        wireCreated,
		Type:             "gpg_sign",
		SearchAttributes: map[string]string{"service": "ssh"},
		GPGSignInfo:      want,
		SenderInfo:       senderInfo,
	}
}

// Checks that each field cli declares decodes from the daemon's encoding of a
// request, for each kind of signed object.
func TestAPIWire(t *testing.T) {
	kinds := []struct {
		name   string
		object approval.SignedObject
		staged approval.ListedFiles
		want   *GPGSignInfo
	}{
		{
			name:   "commit",
			object: approval.SignedObject("tree 0\nparent abc\nauthor Alice <a@x>\ncommitter Bob <b@x>\n\nfix: thing\n"),
			staged: approval.ListedFiles{"a.go"},
			want: &GPGSignInfo{
				RepoName: "repo", Kind: "commit", CommitMsg: "fix: thing", Author: "Alice <a@x>",
				Committer: "Bob <b@x>", KeyID: "KEY", ChangedFiles: []string{"a.go"}, ParentHash: "abc",
			},
		},
		{
			name:   "tag",
			object: approval.SignedObject("object def\ntype commit\ntag v1.0\ntagger Alice <a@x>\n\nv1\n"),
			want:   &GPGSignInfo{RepoName: "repo", Kind: "tag", CommitMsg: "v1", Author: "Alice <a@x>", KeyID: "KEY", TagName: "v1.0", Target: "def"},
		},
		{
			name:   "push",
			object: approval.SignedObject("certificate version 0.1\npusher Alice <a@x>\npushee https://x/r.git\n\n0 1 refs/heads/main\n"),
			want:   &GPGSignInfo{RepoName: "repo", Kind: "push", CommitMsg: "0 1 refs/heads/main", Author: "Alice <a@x>", KeyID: "KEY", Pushee: "https://x/r.git"},
		},
	}
	for _, k := range kinds {
		t.Run(k.name, func(t *testing.T) {
			sign, err := approval.NewGPGSignInfo(k.object, "repo", "KEY", k.staged)
			require.NoError(t, err)
			apiRecord, want := wireRecords(sign, k.want)

			t.Run("pending", func(t *testing.T) {
				got := decodeAsCLI[PendingResponse](t, api.PendingListResponse{
					Requests: []api.PendingRequest{{RequestRecord: apiRecord, ExpiresAt: wireExpires}},
				})
				assert.Equal(t, PendingResponse{Requests: []PendingEntry{{Request: want, ExpiresAt: wireExpires}}}, got)
			})

			t.Run("history", func(t *testing.T) {
				got := decodeAsCLI[HistoryResponse](t, api.HistoryResponse{
					Entries: []api.HistoryEntry{{Request: apiRecord, Resolution: approval.ResolutionApproved, ResolvedAt: wireResolved}},
				})
				assert.Equal(t, HistoryResponse{Entries: []HistoryEntry{{Request: want, Resolution: "approved", ResolvedAt: wireResolved}}}, got)
			})
		})
	}
}

// show --json prints a pending request as list --json prints each one.
func TestFormatEntry_PendingJSON(t *testing.T) {
	req := PendingEntry{Request: RequestRecord{ID: "req-1", Type: "get_secret", CreatedAt: wireCreated}, ExpiresAt: wireExpires}

	var shown, listed bytes.Buffer
	require.NoError(t, NewFormatter(&shown, true).FormatEntry(req))
	require.NoError(t, NewFormatter(&listed, true).FormatPending([]PendingEntry{req}))

	var list []json.RawMessage
	require.NoError(t, json.Unmarshal(listed.Bytes(), &list))
	require.Len(t, list, 1)
	assert.JSONEq(t, string(list[0]), shown.String())
}
