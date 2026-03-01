package cli

import (
	"strings"
	"testing"
	"time"
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
			req := PendingRequest{
				Type:        "gpg_sign",
				GPGSignInfo: &GPGSignInfo{ChangedFiles: tt.files},
			}
			got := requestSummary(req)
			if got != tt.want {
				t.Errorf("requestSummary() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRequestSummary_GetSecret(t *testing.T) {
	tests := []struct {
		name string
		req  PendingRequest
		want string
	}{
		{
			name: "single item",
			req:  PendingRequest{Items: []ItemInfo{{Label: "MyPassword"}}},
			want: "MyPassword",
		},
		{
			name: "multiple items",
			req:  PendingRequest{Items: []ItemInfo{{Label: "A"}, {Label: "B"}}},
			want: "2 items",
		},
		{
			name: "search attributes",
			req:  PendingRequest{SearchAttributes: map[string]string{"service": "ssh"}},
			want: "service=ssh",
		},
		{
			name: "empty",
			req:  PendingRequest{},
			want: "-",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := requestSummary(tt.req)
			if got != tt.want {
				t.Errorf("requestSummary() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatRequest_GPGSign(t *testing.T) {
	req := &PendingRequest{
		ID:        "abc-123",
		Client:    "git/2.43.0",
		Type:      "gpg_sign",
		ExpiresAt: time.Now().Add(5 * time.Minute),
		GPGSignInfo: &GPGSignInfo{
			RepoName:     "myrepo",
			CommitMsg:    "fix: correct typo\n\nLonger description here.",
			Author:       "Alice <alice@example.com>",
			Committer:    "Bob <bob@example.com>",
			KeyID:        "ABCD1234",
			ChangedFiles: []string{"main.go", "go.sum"},
			ParentHash:   "deadbeef",
		},
	}

	var buf strings.Builder
	f := NewFormatter(&buf, false)
	if err := f.FormatRequest(req); err != nil {
		t.Fatalf("FormatRequest failed: %v", err)
	}

	out := buf.String()

	// Primary fields
	mustContain(t, out, "Repo:    myrepo")
	mustContain(t, out, "Author:  Alice <alice@example.com>")
	mustContain(t, out, "Key:     ABCD1234")

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

func TestFormatRequest_GPGSign_SameCommitter(t *testing.T) {
	req := &PendingRequest{
		ID:        "abc-123",
		Client:    "git/2.43.0",
		Type:      "gpg_sign",
		ExpiresAt: time.Now().Add(5 * time.Minute),
		GPGSignInfo: &GPGSignInfo{
			RepoName:     "myrepo",
			CommitMsg:    "chore: bump version",
			Author:       "Alice <alice@example.com>",
			Committer:    "Alice <alice@example.com>", // same as author
			KeyID:        "ABCD1234",
			ChangedFiles: []string{"VERSION"},
		},
	}

	var buf strings.Builder
	f := NewFormatter(&buf, false)
	if err := f.FormatRequest(req); err != nil {
		t.Fatalf("FormatRequest failed: %v", err)
	}

	out := buf.String()
	// When committer == author, Committer line should be omitted
	mustNotContain(t, out, "Committer:")
	// No ParentHash set, so Parent line should be omitted
	mustNotContain(t, out, "Parent:")
}

func TestFormatRequest_GetSecret_Unchanged(t *testing.T) {
	req := &PendingRequest{
		ID:        "xyz-456",
		Client:    "myapp",
		Type:      "get_secret",
		ExpiresAt: time.Now().Add(5 * time.Minute),
		Items:     []ItemInfo{{Label: "DatabasePassword", Path: "/org/secrets/1"}},
	}

	var buf strings.Builder
	f := NewFormatter(&buf, false)
	if err := f.FormatRequest(req); err != nil {
		t.Fatalf("FormatRequest failed: %v", err)
	}

	out := buf.String()
	mustContain(t, out, "Secret:  DatabasePassword")
	mustNotContain(t, out, "Coll:")
	mustNotContain(t, out, "Repo:")
	mustNotContain(t, out, "Author:")
}

func TestFormatRequest_ShowsCollection(t *testing.T) {
	req := &PendingRequest{
		ID:        "xyz-456",
		Client:    "myapp",
		Type:      "get_secret",
		ExpiresAt: time.Now().Add(5 * time.Minute),
		Items:     []ItemInfo{{Label: "pw", Path: "/org/freedesktop/secrets/collection/work/99"}},
	}

	var buf strings.Builder
	f := NewFormatter(&buf, false)
	if err := f.FormatRequest(req); err != nil {
		t.Fatalf("FormatRequest failed: %v", err)
	}

	mustContain(t, buf.String(), "Coll:    work")
}

func TestFormatRequests_SummaryColumnHeader(t *testing.T) {
	reqs := []PendingRequest{
		{
			ID:        "abc",
			ExpiresAt: time.Now().Add(time.Minute),
			Items:     []ItemInfo{{Label: "X"}},
		},
	}

	var buf strings.Builder
	f := NewFormatter(&buf, false)
	if err := f.FormatRequests(reqs); err != nil {
		t.Fatalf("FormatRequests failed: %v", err)
	}

	out := buf.String()
	mustContain(t, out, "SUMMARY")
	mustContain(t, out, "COLLECTION")
	mustNotContain(t, out, "SECRET")
}

func TestFormatRequests_CollectionColumn(t *testing.T) {
	reqs := []PendingRequest{
		{
			ID:        "abc",
			ExpiresAt: time.Now().Add(time.Minute),
			Items:     []ItemInfo{{Label: "X", Path: "/org/freedesktop/secrets/collection/mykeys/1"}},
		},
	}

	var buf strings.Builder
	f := NewFormatter(&buf, false)
	if err := f.FormatRequests(reqs); err != nil {
		t.Fatalf("FormatRequests failed: %v", err)
	}

	mustContain(t, buf.String(), "mykeys")
}

func TestFormatHistory_SummaryColumnHeader(t *testing.T) {
	entries := []HistoryEntry{
		{
			Request:    PendingRequest{ID: "abc", Items: []ItemInfo{{Label: "X"}}},
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
			Request:    PendingRequest{ID: "abc", Items: []ItemInfo{{Label: "X", Path: "/org/freedesktop/secrets/collection/login/42"}}},
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
