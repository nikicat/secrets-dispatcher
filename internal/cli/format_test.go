package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/procutil"
	"github.com/nikicat/secrets-dispatcher/internal/sender"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func render(t *testing.T, format func(*Formatter) error) string {
	t.Helper()
	var buf strings.Builder
	require.NoError(t, format(NewFormatter(&buf, false)))
	return buf.String()
}

func TestRequestSummary_GPGSign(t *testing.T) {
	tests := []struct {
		name  string
		files []string
		want  string
	}{
		{"zero files", []string{}, "0 files"},
		{"one file", []string{"main.go"}, "1 file"},
		{"two files", []string{"main.go", "go.sum"}, "2 files"},
		{"three files", []string{"a", "b", "c"}, "3 files"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := RequestRecord{
				Type:        "gpg_sign",
				GPGSignInfo: &GPGSignInfo{Kind: approval.GPGSignCommit, ChangedFiles: tt.files},
			}
			got := req.Summary()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRequestSummary_GetSecret(t *testing.T) {
	tests := []struct {
		name string
		req  RequestRecord
		want string
	}{
		{
			name: "single item",
			req:  RequestRecord{Items: []ItemInfo{{Label: "MyPassword"}}},
			want: "MyPassword",
		},
		{
			name: "multiple items",
			req:  RequestRecord{Items: []ItemInfo{{Label: "A"}, {Label: "B"}}},
			want: "2 items",
		},
		{
			name: "search attributes",
			req:  RequestRecord{SearchAttributes: map[string]string{"service": "ssh"}},
			want: "service=ssh",
		},
		{
			name: "empty",
			req:  RequestRecord{},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.req.Summary()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestFormatEntry_GPGSign(t *testing.T) {
	req := PendingEntry{Request: RequestRecord{ID: "abc-123", Client: "git/2.43.0", Type: "gpg_sign", GPGSignInfo: &GPGSignInfo{
		Kind:         approval.GPGSignCommit,
		RepoName:     "myrepo",
		CommitMsg:    "fix: correct typo\n\nLonger description here.",
		Author:       "Alice <alice@example.com>",
		Committer:    "Bob <bob@example.com>",
		KeyID:        "ABCD1234",
		ChangedFiles: []string{"main.go", "go.sum"},
		ParentHash:   "deadbeef",
	}}, ExpiresAt: time.Now().Add(5 * time.Minute)}

	out := render(t, func(f *Formatter) error { return f.FormatEntry(req) })

	// Primary fields
	assert.Contains(t, out, "Repo:      myrepo")
	assert.Contains(t, out, "Author:    Alice <alice@example.com>")
	assert.Contains(t, out, "Key:       ABCD1234")

	// Commit subject indented
	assert.Contains(t, out, "    fix: correct typo")

	// Commit body indented
	assert.Contains(t, out, "    Longer description here.")

	// Changed files
	assert.Contains(t, out, "Changed files (2):")
	assert.Contains(t, out, "  main.go")
	assert.Contains(t, out, "  go.sum")

	// Secondary metadata (committer differs from author)
	assert.Contains(t, out, "Committer: Bob <bob@example.com>")
	assert.Contains(t, out, "Parent:    deadbeef")

	// Must NOT include Secret: or Query: lines
	assert.NotContains(t, out, "Secret:")
	assert.NotContains(t, out, "Query:")
}

func TestFormatEntry_GPGSign_SameCommitter(t *testing.T) {
	req := PendingEntry{Request: RequestRecord{ID: "abc-123", Client: "git/2.43.0", Type: "gpg_sign", GPGSignInfo: &GPGSignInfo{
		Kind:         approval.GPGSignCommit,
		RepoName:     "myrepo",
		CommitMsg:    "chore: bump version",
		Author:       "Alice <alice@example.com>",
		Committer:    "Alice <alice@example.com>", // same as author
		KeyID:        "ABCD1234",
		ChangedFiles: []string{"VERSION"},
	}}, ExpiresAt: time.Now().Add(5 * time.Minute)}

	out := render(t, func(f *Formatter) error { return f.FormatEntry(req) })
	// When committer == author, Committer line should be omitted
	assert.NotContains(t, out, "Committer:")
	// No ParentHash set, so Parent line should be omitted
	assert.NotContains(t, out, "Parent:")
}

func TestFormatEntry_GetSecret_Unchanged(t *testing.T) {
	req := PendingEntry{Request: RequestRecord{ID: "xyz-456", Client: "myapp", Type: "get_secret", Items: []ItemInfo{{Label: "DatabasePassword", Path: "/org/secrets/1"}}}, ExpiresAt: time.Now().Add(5 * time.Minute)}

	out := render(t, func(f *Formatter) error { return f.FormatEntry(req) })
	assert.Contains(t, out, "Secret:    DatabasePassword")
	assert.Contains(t, out, "Path:      /org/secrets/1")
	assert.NotContains(t, out, "Coll:")
	assert.NotContains(t, out, "Repo:")
	assert.NotContains(t, out, "Author:")
}

func TestFormatEntry_ShowsItemAttributes(t *testing.T) {
	req := PendingEntry{Request: RequestRecord{ID: "xyz-456", Client: "myapp", Type: "get_secret", Items: []ItemInfo{{
		Label:      "MySecret",
		Path:       "/org/freedesktop/secrets/collection/login/42",
		Attributes: map[string]string{"service": "ssh", "user": "alice"},
	}}, SenderInfo: sender.Info{
		Sender:      new(sender.UniqueName(":1.42")),
		PID:         1234,
		UID:         new(procutil.UID(1000)),
		SystemdUnit: "myapp.service",
	}, Session: "/org/freedesktop/secrets/session/s1"}, ExpiresAt: time.Now().Add(5 * time.Minute)}

	out := render(t, func(f *Formatter) error { return f.FormatEntry(req) })
	assert.Contains(t, out, "Secret:    MySecret")
	assert.Contains(t, out, "Path:      /org/freedesktop/secrets/collection/login/42")
	assert.Contains(t, out, "  Attrs:\n")
	assert.Contains(t, out, "    service: ssh\n")
	assert.Contains(t, out, "    user: alice\n")
	assert.Contains(t, out, "Process:   myapp.service")
	assert.Contains(t, out, "Sender:    :1.42")
	assert.Contains(t, out, "Session:   /org/freedesktop/secrets/session/s1")
	assert.Contains(t, out, "Expires:")
	assert.Contains(t, out, "remaining)")
}

func TestFormatEntry_MultipleItemsShowAttributes(t *testing.T) {
	req := PendingEntry{Request: RequestRecord{ID: "xyz-789", Client: "myapp", Type: "get_secret", Items: []ItemInfo{
		{Label: "Secret1", Path: "/org/secrets/1", Attributes: map[string]string{"service": "foo"}},
		{Label: "Secret2", Path: "/org/secrets/2"},
	}}, ExpiresAt: time.Now().Add(5 * time.Minute)}

	out := render(t, func(f *Formatter) error { return f.FormatEntry(req) })
	assert.Contains(t, out, "Secrets:   2 items")
	assert.Contains(t, out, "- Secret1  /org/secrets/1")
	assert.Contains(t, out, "    Attrs:\n")
	assert.Contains(t, out, "      service: foo\n")
	assert.Contains(t, out, "- Secret2  /org/secrets/2")
}

func TestFormatEntry_ShowsCollection(t *testing.T) {
	req := PendingEntry{Request: RequestRecord{ID: "xyz-456", Client: "myapp", Type: "get_secret", Items: []ItemInfo{{Label: "pw", Path: "/org/freedesktop/secrets/collection/work/99", Collection: new(dbustypes.CollectionName("work"))}}}, ExpiresAt: time.Now().Add(5 * time.Minute)}

	out := render(t, func(f *Formatter) error { return f.FormatEntry(req) })
	assert.Contains(t, out, "Coll:      work")
}

func TestFormatPending_SummaryColumnHeader(t *testing.T) {
	reqs := []PendingEntry{
		{Request: RequestRecord{ID: "abc", Items: []ItemInfo{{Label: "X"}}}, ExpiresAt: time.Now().Add(time.Minute)},
	}

	out := render(t, func(f *Formatter) error { return f.FormatPending(reqs) })
	assert.Contains(t, out, "SUMMARY")
	assert.Contains(t, out, "COLLECTION")
	assert.NotContains(t, out, "SECRET")
}

func TestFormatPending_CollectionColumn(t *testing.T) {
	reqs := []PendingEntry{
		{Request: RequestRecord{ID: "abc", Items: []ItemInfo{{Label: "X", Path: "/org/freedesktop/secrets/collection/mykeys/1", Collection: new(dbustypes.CollectionName("mykeys"))}}}, ExpiresAt: time.Now().Add(time.Minute)},
	}

	out := render(t, func(f *Formatter) error { return f.FormatPending(reqs) })
	assert.Contains(t, out, "mykeys")
}

func TestFormatHistory_SummaryColumnHeader(t *testing.T) {
	entries := []HistoryEntry{
		{
			Request:    RequestRecord{ID: "abc", Items: []ItemInfo{{Label: "X"}}},
			Resolution: "approved",
			ResolvedAt: time.Now(),
		},
	}

	out := render(t, func(f *Formatter) error { return f.FormatHistory(entries) })
	assert.Contains(t, out, "SUMMARY")
	assert.Contains(t, out, "COLLECTION")
	assert.NotContains(t, out, "SECRET")
}

func TestFormatHistory_CollectionColumn(t *testing.T) {
	entries := []HistoryEntry{
		{
			Request:    RequestRecord{ID: "abc", Items: []ItemInfo{{Label: "X", Path: "/org/freedesktop/secrets/collection/login/42", Collection: new(dbustypes.CollectionName("login"))}}},
			Resolution: "approved",
			ResolvedAt: time.Now(),
		},
	}

	out := render(t, func(f *Formatter) error { return f.FormatHistory(entries) })
	assert.Contains(t, out, "login")
}

func TestCommitSubject(t *testing.T) {
	tests := []struct {
		msg  string
		want string
	}{
		{"single line", "single line"},
		{"subject\n\nbody", "subject"},
		{"subject\nbody without blank", "subject"},
		{"", ""},
	}
	for _, tt := range tests {
		got := commitSubject(tt.msg)
		assert.Equal(t, tt.want, got, tt.msg)
	}
}

func TestCommitBody(t *testing.T) {
	tests := []struct {
		msg  string
		want string
	}{
		{"single line", ""},
		{"subject\n\nbody here", "body here"},
		{"subject\nbody no blank", "body no blank"},
		{"subject\n\nline1\nline2", "line1\nline2"},
		{"subject\n\nbody\n", "body"},
	}
	for _, tt := range tests {
		got := commitBody(tt.msg)
		assert.Equal(t, tt.want, got, tt.msg)
	}
}

// Pins the column layout of the pending and history tables.
func TestFormatTables_Layout(t *testing.T) {
	uid := procutil.UID(1000)
	req := RequestRecord{
		ID:         "0123456789abcdef",
		Client:     "a-client-name-longer-than-twenty",
		Type:       "get_secret",
		Items:      []ItemInfo{{Label: "a label that is longer than the summary column", Path: "/org/freedesktop/secrets/collection/a-very-long-collection/1", Collection: new(dbustypes.CollectionName("a-very-long-collection"))}},
		SenderInfo: sender.Info{PID: 4242, UID: &uid},
	}
	pending := render(t, func(f *Formatter) error {
		return f.FormatPending([]PendingEntry{{Request: req, ExpiresAt: time.Now().Add(-time.Minute)}})
	})
	assert.Equal(t, ""+
		"ID        CLIENT                    PID    UID  TYPE          COLLECTION       SUMMARY                    EXPIRES\n"+
		"--------  --------------------  -------  -----  ------------  ---------------  -------------------------  -------\n"+
		"0123456…  a-client-name-longe…     4242   1000  get_secret    a-very-long-co…  a label that is longer t…  expired\n",
		pending)

	history := render(t, func(f *Formatter) error {
		return f.FormatHistory([]HistoryEntry{{Request: RequestRecord{ID: "abc"}, Resolution: "auto_approved", ResolvedAt: time.Now().Add(time.Hour)}})
	})
	assert.Equal(t, ""+
		"ID        CLIENT                    PID    UID  TYPE          COLLECTION       RESULT      SUMMARY               RESOLVED\n"+
		"--------  --------------------  -------  -----  ------------  ---------------  ----------  --------------------  --------\n"+
		"abc       -                           -      -  -             -                auto_appr…  -                     just now\n",
		history)

	cyrillic := RequestRecord{
		ID:         "abc",
		Client:     "клиент-с-длинным-именем",
		Type:       "get_secret",
		Items:      []ItemInfo{{Label: "пароль от почты на работе и дома", Path: "/org/freedesktop/secrets/collection/login/1", Collection: new(dbustypes.CollectionName("login"))}},
		SenderInfo: sender.Info{PID: 4242, UID: &uid},
	}
	cut := render(t, func(f *Formatter) error {
		return f.FormatPending([]PendingEntry{{Request: cyrillic, ExpiresAt: time.Now().Add(-time.Minute)}})
	})
	assert.Equal(t, ""+
		"ID        CLIENT                    PID    UID  TYPE          COLLECTION       SUMMARY                    EXPIRES\n"+
		"--------  --------------------  -------  -----  ------------  ---------------  -------------------------  -------\n"+
		"abc       клиент-с-длинным-им…     4242   1000  get_secret    login            пароль от почты на работ…  expired\n",
		cut)
}

// Pins the text layout of a history entry. Every value sits in one column,
// whatever its label.
func TestFormatEntry_Layout(t *testing.T) {
	future := time.Date(2999, 1, 1, 0, 0, 0, 0, time.UTC)
	entry := HistoryEntry{
		Request: RequestRecord{
			ID: "abc", Client: "git", Type: "gpg_sign", Session: "/s/1",
			CreatedAt: time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC),
			GPGSignInfo: &GPGSignInfo{
				Kind: approval.GPGSignCommit, RepoName: "repo", CommitMsg: "fix: x",
				Author: "Alice", Committer: "Bob", KeyID: "KEY", ChangedFiles: []string{"a.go"}, ParentHash: "def",
			},
		},
		Resolution: "approved",
		ResolvedAt: future,
	}
	out := render(t, func(f *Formatter) error { return f.FormatEntry(entry) })
	assert.Equal(t, ""+
		"ID:        abc\n"+
		"Client:    git\n"+
		"Type:      gpg_sign\n"+
		"Session:   /s/1\n"+
		"Repo:      repo\n"+
		"Author:    Alice\n"+
		"Key:       KEY\n"+
		"\n    fix: x\n"+
		"\nChanged files (1):\n"+
		"  a.go\n"+
		"\nCommitter: Bob\n"+
		"Parent:    def\n"+
		"Created:   2026-11-22T10:00:00Z\n"+
		"Result:    approved\n"+
		"Resolved:  2999-01-01T00:00:00Z (just now)\n",
		out)
}
