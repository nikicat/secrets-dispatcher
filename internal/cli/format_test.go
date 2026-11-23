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
			if got != tt.want {
				t.Errorf("Summary() = %q, want %q", got, tt.want)
			}
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
			if got != tt.want {
				t.Errorf("Summary() = %q, want %q", got, tt.want)
			}
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

	var buf strings.Builder
	f := NewFormatter(&buf, false)
	if err := f.FormatEntry(req); err != nil {
		t.Fatalf("FormatEntry failed: %v", err)
	}

	out := buf.String()

	// Primary fields
	mustContain(t, out, "Repo:      myrepo")
	mustContain(t, out, "Author:    Alice <alice@example.com>")
	mustContain(t, out, "Key:       ABCD1234")

	// Commit subject indented
	mustContain(t, out, "    fix: correct typo")

	// Commit body indented
	mustContain(t, out, "    Longer description here.")

	// Changed files
	mustContain(t, out, "Changed files (2):")
	mustContain(t, out, "  main.go")
	mustContain(t, out, "  go.sum")

	// Secondary metadata (committer differs from author)
	mustContain(t, out, "Committer: Bob <bob@example.com>")
	mustContain(t, out, "Parent:    deadbeef")

	// Must NOT include Secret: or Query: lines
	mustNotContain(t, out, "Secret:")
	mustNotContain(t, out, "Query:")
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

	var buf strings.Builder
	f := NewFormatter(&buf, false)
	if err := f.FormatEntry(req); err != nil {
		t.Fatalf("FormatEntry failed: %v", err)
	}

	out := buf.String()
	// When committer == author, Committer line should be omitted
	mustNotContain(t, out, "Committer:")
	// No ParentHash set, so Parent line should be omitted
	mustNotContain(t, out, "Parent:")
}

func TestFormatEntry_GetSecret_Unchanged(t *testing.T) {
	req := PendingEntry{Request: RequestRecord{ID: "xyz-456", Client: "myapp", Type: "get_secret", Items: []ItemInfo{{Label: "DatabasePassword", Path: "/org/secrets/1"}}}, ExpiresAt: time.Now().Add(5 * time.Minute)}

	var buf strings.Builder
	f := NewFormatter(&buf, false)
	if err := f.FormatEntry(req); err != nil {
		t.Fatalf("FormatEntry failed: %v", err)
	}

	out := buf.String()
	mustContain(t, out, "Secret:    DatabasePassword")
	mustContain(t, out, "Path:      /org/secrets/1")
	mustNotContain(t, out, "Coll:")
	mustNotContain(t, out, "Repo:")
	mustNotContain(t, out, "Author:")
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

	var buf strings.Builder
	f := NewFormatter(&buf, false)
	if err := f.FormatEntry(req); err != nil {
		t.Fatalf("FormatEntry failed: %v", err)
	}

	out := buf.String()
	mustContain(t, out, "Secret:    MySecret")
	mustContain(t, out, "Path:      /org/freedesktop/secrets/collection/login/42")
	mustContain(t, out, "  Attrs:\n")
	mustContain(t, out, "    service: ssh\n")
	mustContain(t, out, "    user: alice\n")
	mustContain(t, out, "Process:   myapp.service")
	mustContain(t, out, "Sender:    :1.42")
	mustContain(t, out, "Session:   /org/freedesktop/secrets/session/s1")
	mustContain(t, out, "Expires:")
	mustContain(t, out, "remaining)")
}

func TestFormatEntry_MultipleItemsShowAttributes(t *testing.T) {
	req := PendingEntry{Request: RequestRecord{ID: "xyz-789", Client: "myapp", Type: "get_secret", Items: []ItemInfo{
		{Label: "Secret1", Path: "/org/secrets/1", Attributes: map[string]string{"service": "foo"}},
		{Label: "Secret2", Path: "/org/secrets/2"},
	}}, ExpiresAt: time.Now().Add(5 * time.Minute)}

	var buf strings.Builder
	f := NewFormatter(&buf, false)
	if err := f.FormatEntry(req); err != nil {
		t.Fatalf("FormatEntry failed: %v", err)
	}

	out := buf.String()
	mustContain(t, out, "Secrets:   2 items")
	mustContain(t, out, "- Secret1  /org/secrets/1")
	mustContain(t, out, "    Attrs:\n")
	mustContain(t, out, "      service: foo\n")
	mustContain(t, out, "- Secret2  /org/secrets/2")
}

func TestFormatEntry_ShowsCollection(t *testing.T) {
	req := PendingEntry{Request: RequestRecord{ID: "xyz-456", Client: "myapp", Type: "get_secret", Items: []ItemInfo{{Label: "pw", Path: "/org/freedesktop/secrets/collection/work/99", Collection: new(dbustypes.CollectionName("work"))}}}, ExpiresAt: time.Now().Add(5 * time.Minute)}

	var buf strings.Builder
	f := NewFormatter(&buf, false)
	if err := f.FormatEntry(req); err != nil {
		t.Fatalf("FormatEntry failed: %v", err)
	}

	mustContain(t, buf.String(), "Coll:      work")
}

func TestFormatPending_SummaryColumnHeader(t *testing.T) {
	reqs := []PendingEntry{
		{Request: RequestRecord{ID: "abc", Items: []ItemInfo{{Label: "X"}}}, ExpiresAt: time.Now().Add(time.Minute)},
	}

	var buf strings.Builder
	f := NewFormatter(&buf, false)
	if err := f.FormatPending(reqs); err != nil {
		t.Fatalf("FormatPending failed: %v", err)
	}

	out := buf.String()
	mustContain(t, out, "SUMMARY")
	mustContain(t, out, "COLLECTION")
	mustNotContain(t, out, "SECRET")
}

func TestFormatPending_CollectionColumn(t *testing.T) {
	reqs := []PendingEntry{
		{Request: RequestRecord{ID: "abc", Items: []ItemInfo{{Label: "X", Path: "/org/freedesktop/secrets/collection/mykeys/1", Collection: new(dbustypes.CollectionName("mykeys"))}}}, ExpiresAt: time.Now().Add(time.Minute)},
	}

	var buf strings.Builder
	f := NewFormatter(&buf, false)
	if err := f.FormatPending(reqs); err != nil {
		t.Fatalf("FormatPending failed: %v", err)
	}

	mustContain(t, buf.String(), "mykeys")
}

func TestFormatHistory_SummaryColumnHeader(t *testing.T) {
	entries := []HistoryEntry{
		{
			Request:    RequestRecord{ID: "abc", Items: []ItemInfo{{Label: "X"}}},
			Resolution: "approved",
			ResolvedAt: time.Now(),
		},
	}

	var buf strings.Builder
	f := NewFormatter(&buf, false)
	if err := f.FormatHistory(entries); err != nil {
		t.Fatalf("FormatHistory failed: %v", err)
	}

	out := buf.String()
	mustContain(t, out, "SUMMARY")
	mustContain(t, out, "COLLECTION")
	mustNotContain(t, out, "SECRET")
}

func TestFormatHistory_CollectionColumn(t *testing.T) {
	entries := []HistoryEntry{
		{
			Request:    RequestRecord{ID: "abc", Items: []ItemInfo{{Label: "X", Path: "/org/freedesktop/secrets/collection/login/42", Collection: new(dbustypes.CollectionName("login"))}}},
			Resolution: "approved",
			ResolvedAt: time.Now(),
		},
	}

	var buf strings.Builder
	f := NewFormatter(&buf, false)
	if err := f.FormatHistory(entries); err != nil {
		t.Fatalf("FormatHistory failed: %v", err)
	}

	mustContain(t, buf.String(), "login")
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
		if got != tt.want {
			t.Errorf("commitSubject(%q) = %q, want %q", tt.msg, got, tt.want)
		}
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
		if got != tt.want {
			t.Errorf("commitBody(%q) = %q, want %q", tt.msg, got, tt.want)
		}
	}
}

func mustContain(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("expected output to contain %q\nfull output:\n%s", substr, s)
	}
}

func mustNotContain(t *testing.T, s, substr string) {
	t.Helper()
	if strings.Contains(s, substr) {
		t.Errorf("expected output NOT to contain %q\nfull output:\n%s", substr, s)
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
	var pending strings.Builder
	require.NoError(t, NewFormatter(&pending, false).FormatPending([]PendingEntry{{Request: req, ExpiresAt: time.Now().Add(-time.Minute)}}))
	assert.Equal(t, ""+
		"ID        CLIENT                    PID    UID  TYPE          COLLECTION       SUMMARY                    EXPIRES\n"+
		"--------  --------------------  -------  -----  ------------  ---------------  -------------------------  -------\n"+
		"0123456…  a-client-name-longe…     4242   1000  get_secret    a-very-long-co…  a label that is longer t…  expired\n",
		pending.String())

	var history strings.Builder
	require.NoError(t, NewFormatter(&history, false).FormatHistory([]HistoryEntry{{Request: RequestRecord{ID: "abc"}, Resolution: "auto_approved", ResolvedAt: time.Now().Add(time.Hour)}}))
	assert.Equal(t, ""+
		"ID        CLIENT                    PID    UID  TYPE          COLLECTION       RESULT      SUMMARY               RESOLVED\n"+
		"--------  --------------------  -------  -----  ------------  ---------------  ----------  --------------------  --------\n"+
		"abc       -                           -      -  -             -                auto_appr…  -                     just now\n",
		history.String())

	cyrillic := RequestRecord{
		ID:         "abc",
		Client:     "клиент-с-длинным-именем",
		Type:       "get_secret",
		Items:      []ItemInfo{{Label: "пароль от почты на работе и дома", Path: "/org/freedesktop/secrets/collection/login/1", Collection: new(dbustypes.CollectionName("login"))}},
		SenderInfo: sender.Info{PID: 4242, UID: &uid},
	}
	var cut strings.Builder
	require.NoError(t, NewFormatter(&cut, false).FormatPending([]PendingEntry{{Request: cyrillic, ExpiresAt: time.Now().Add(-time.Minute)}}))
	assert.Equal(t, ""+
		"ID        CLIENT                    PID    UID  TYPE          COLLECTION       SUMMARY                    EXPIRES\n"+
		"--------  --------------------  -------  -----  ------------  ---------------  -------------------------  -------\n"+
		"abc       клиент-с-длинным-им…     4242   1000  get_secret    login            пароль от почты на работ…  expired\n",
		cut.String())
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
	var out strings.Builder
	require.NoError(t, NewFormatter(&out, false).FormatEntry(entry))
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
		out.String())
}
