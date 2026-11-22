package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
)

// Formatter outputs data in various formats.
type Formatter struct {
	w      io.Writer
	asJSON bool
}

// NewFormatter creates a new formatter.
func NewFormatter(w io.Writer, asJSON bool) *Formatter {
	return &Formatter{w: w, asJSON: asJSON}
}

// Writes the pending entries as JSON or as a table.
func (f *Formatter) FormatPending(entries []PendingEntry) error {
	if f.asJSON {
		return json.NewEncoder(f.w).Encode(entries)
	}

	if len(entries) == 0 {
		fmt.Fprintln(f.w, "No pending requests")
		return nil
	}

	t := requestTable(
		[]column[PendingEntry]{{title: "SUMMARY", width: 25, value: entrySummary[PendingEntry]}},
		lastColumn[PendingEntry]{title: "EXPIRES", value: func(e PendingEntry) string { return formatRemaining(e.ExpiresAt) }},
	)
	t.writeHeader(f.w)
	for _, entry := range entries {
		t.writeRow(f.w, entry)
	}
	return nil
}

// A column of a text table.
type column[R any] struct {
	title string
	width int
	// True if the cells are aligned right. False otherwise.
	right bool
	value func(R) string
}

// Returns the given text cut and padded to the column's width.
func (c column[R]) fit(text string) string {
	text = truncate(text, c.width)
	if c.right {
		return fmt.Sprintf("%*s", c.width, text)
	}
	return fmt.Sprintf("%-*s", c.width, text)
}

// The last column of a text table. Its cells are neither cut nor padded.
type lastColumn[R any] struct {
	title string
	value func(R) string
}

// A text table of columns two spaces apart.
type table[R any] struct {
	columns []column[R]
	last    lastColumn[R]
}

// Writes the column titles and a rule under each.
func (t table[R]) writeHeader(w io.Writer) {
	var titles, rules []string
	for _, c := range t.columns {
		titles = append(titles, c.fit(c.title))
		rules = append(rules, strings.Repeat("-", c.width))
	}
	writeLine(w, append(titles, t.last.title))
	writeLine(w, append(rules, strings.Repeat("-", len(t.last.title))))
}

// Writes the row's cells.
func (t table[R]) writeRow(w io.Writer, row R) {
	var cells []string
	for _, c := range t.columns {
		cells = append(cells, c.fit(cellText(c.value(row))))
	}
	writeLine(w, append(cells, cellText(t.last.value(row))))
}

func writeLine(w io.Writer, cells []string) {
	fmt.Fprintln(w, strings.Join(cells, "  "))
}

// Returns the text of a table cell. A value that is not known shows as "-".
func cellText(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

// Returns the table of the request columns, then the given columns, then the
// given last column.
func requestTable[R Entry](columns []column[R], last lastColumn[R]) table[R] {
	return table[R]{columns: append(requestColumns[R](), columns...), last: last}
}

// The columns that every request table starts with.
func requestColumns[R Entry]() []column[R] {
	return []column[R]{
		{title: "ID", width: 8, value: func(e R) string { return string(e.Record().ID) }},
		{title: "CLIENT", width: 20, value: func(e R) string { return string(e.Record().Client) }},
		{title: "PID", width: 7, right: true, value: func(e R) string {
			if pid := e.Record().SenderInfo.PID; pid != 0 {
				return pid.String()
			}
			return ""
		}},
		{title: "UID", width: 5, right: true, value: func(e R) string {
			if uid := e.Record().SenderInfo.UID; uid != nil {
				return uid.String()
			}
			return ""
		}},
		{title: "TYPE", width: 12, value: func(e R) string { return string(e.Record().Type) }},
		{title: "COLLECTION", width: 15, value: func(e R) string {
			if ref, ok := requestCollection(e.Record()); ok {
				return ref.Name
			}
			return ""
		}},
	}
}

func entrySummary[R Entry](e R) string { return requestSummary(e.Record()) }

func truncate(s string, maxLen int) string {
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	return string(r[:maxLen-1]) + "…"
}

func requestSummary(req RequestRecord) string {
	if req.GPGSignInfo != nil {
		switch req.GPGSignInfo.Kind {
		case approval.GPGSignTag:
			if req.GPGSignInfo.TagName != "" {
				return "tag " + string(req.GPGSignInfo.TagName)
			}
			return "tag"
		case approval.GPGSignPush:
			return "push"
		case approval.GPGSignCommit:
			n := len(req.GPGSignInfo.ChangedFiles)
			if n == 1 {
				return "1 file"
			}
			return fmt.Sprintf("%d files", n)
		}
	}
	if len(req.Items) == 0 {
		if len(req.SearchAttributes) > 0 {
			return formatAttrs(req.SearchAttributes)
		}
		return ""
	}
	if len(req.Items) == 1 {
		return req.Items[0].DisplayLabel()
	}
	return fmt.Sprintf("%d items", len(req.Items))
}

func formatRemaining(expiresAt time.Time) string {
	remaining := time.Until(expiresAt).Round(time.Second)
	if remaining <= 0 {
		return "expired"
	}
	return remaining.String()
}

// Writes the entry as JSON or as text.
func (f *Formatter) FormatEntry(e Entry) error {
	if f.asJSON {
		return json.NewEncoder(f.w).Encode(e)
	}
	_, err := fmt.Fprint(f.w, e)
	return err
}

func (e PendingEntry) String() string {
	var b strings.Builder
	writeRequest(&b, &e.Request)
	writeExpiry(&b, e.ExpiresAt)
	return b.String()
}

func (e HistoryEntry) String() string {
	var b strings.Builder
	writeRequest(&b, &e.Request)
	fmt.Fprintf(&b, "Result:  %s\n", e.Resolution)
	fmt.Fprintf(&b, "Resolved: %s (%s)\n", e.ResolvedAt.Format(time.RFC3339), formatAgo(e.ResolvedAt))
	return b.String()
}

func writeRequest(w io.Writer, req *RequestRecord) {
	fmt.Fprintf(w, "ID:      %s\n", req.ID)
	fmt.Fprintf(w, "Client:  %s\n", req.Client)
	fmt.Fprintf(w, "Type:    %s\n", req.Type)

	if ref, ok := requestCollection(*req); ok {
		fmt.Fprintf(w, "Coll:    %s\n", ref.Name)
	}

	// Sender process info
	if name := req.SenderInfo.DisplayName(); name != "" {
		fmt.Fprintf(w, "Process: %s\n", name)
	}
	if req.SenderInfo.PID != 0 {
		fmt.Fprintf(w, "PID:     %d\n", req.SenderInfo.PID)
	}
	if s := req.SenderInfo.Sender; s != nil {
		fmt.Fprintf(w, "Sender:  %s\n", *s)
	}
	if len(req.SenderInfo.ProcessChain) > 0 {
		fmt.Fprintln(w, "Chain:")
		for _, p := range req.SenderInfo.ProcessChain {
			exe := p.Name
			if path, ok := p.ExePath(); ok {
				exe = string(path)
			}
			fmt.Fprintf(w, "  %s[%d]", exe, p.PID)
			if cwd, ok := p.CWDPath(); ok {
				fmt.Fprintf(w, " cwd=%s", cwd)
			}
			fmt.Fprintln(w)
			if len(p.Args) > 1 {
				fmt.Fprintf(w, "    args: %s\n", strings.Join(p.Args[1:], " "))
			}
		}
	}

	if req.GPGSignInfo != nil {
		info := req.GPGSignInfo
		fmt.Fprintf(w, "Repo:    %s\n", info.RepoName)
		// The approver on the trusted VT sees which kind of object they sign,
		// with that kind's own fields.
		switch info.Kind {
		case approval.GPGSignTag:
			fmt.Fprintln(w, "Signing: annotated tag")
			if info.TagName != "" {
				fmt.Fprintf(w, "Tag:     %s\n", info.TagName)
			}
			fmt.Fprintf(w, "Tagger:  %s\n", info.Author)
			fmt.Fprintf(w, "Key:     %s\n", info.KeyID)
			if info.Target != "" {
				fmt.Fprintf(w, "Target:  %s\n", info.Target)
			}
			writeSignMessage(w, info.CommitMsg)
		case approval.GPGSignPush:
			fmt.Fprintln(w, "Signing: signed push")
			fmt.Fprintf(w, "Pusher:  %s\n", info.Author)
			if info.Pushee != "" {
				fmt.Fprintf(w, "Pushee:  %s\n", info.Pushee)
			}
			fmt.Fprintf(w, "Key:     %s\n", info.KeyID)
			fmt.Fprintln(w, "\nRef updates:")
			for line := range strings.SplitSeq(info.CommitMsg, "\n") {
				if line != "" {
					fmt.Fprintf(w, "  %s\n", line)
				}
			}
		case approval.GPGSignCommit:
			fmt.Fprintf(w, "Author:  %s\n", info.Author)
			fmt.Fprintf(w, "Key:     %s\n", info.KeyID)
			writeSignMessage(w, info.CommitMsg)
			fmt.Fprintln(w)
			fmt.Fprintf(w, "Changed files (%d):\n", len(info.ChangedFiles))
			for _, file := range info.ChangedFiles {
				fmt.Fprintf(w, "  %s\n", file)
			}
			if info.Committer != "" && info.Committer != info.Author {
				fmt.Fprintf(w, "\nCommitter: %s\n", info.Committer)
			}
			if info.ParentHash != "" {
				fmt.Fprintf(w, "Parent:    %s\n", info.ParentHash)
			}
		}
	} else {
		if len(req.Items) == 1 {
			fmt.Fprintf(w, "Secret:  %s\n", req.Items[0].Label)
			fmt.Fprintf(w, "Path:    %s\n", req.Items[0].Path)
			formatItemAttrs(w, req.Items[0].Attributes, "  ")
		} else if len(req.Items) > 1 {
			fmt.Fprintf(w, "Secrets: %d items\n", len(req.Items))
			for _, item := range req.Items {
				fmt.Fprintf(w, "  - %s  %s\n", item.Label, item.Path)
				formatItemAttrs(w, item.Attributes, "    ")
			}
		}

		if len(req.SearchAttributes) > 0 {
			attrs := formatAttrs(req.SearchAttributes)
			fmt.Fprintf(w, "Query:   %s\n", attrs)
		}
	}

	if req.Session != "" {
		fmt.Fprintf(w, "Session: %s\n", req.Session)
	}
	fmt.Fprintf(w, "Created: %s\n", req.CreatedAt.Format(time.RFC3339))
}

func writeExpiry(w io.Writer, expiresAt time.Time) {
	remaining := time.Until(expiresAt).Round(time.Second)
	if remaining > 0 {
		fmt.Fprintf(w, "Expires: %s (%s remaining)\n", expiresAt.Format(time.RFC3339), remaining)
	} else {
		fmt.Fprintf(w, "Expires: %s (expired)\n", expiresAt.Format(time.RFC3339))
	}
}

func commitSubject(msg string) string {
	if before, _, ok := strings.Cut(msg, "\n"); ok {
		return before
	}
	return msg
}

func commitBody(msg string) string {
	i := strings.IndexByte(msg, '\n')
	if i < 0 {
		return ""
	}
	body := strings.TrimLeft(msg[i:], "\n")
	return strings.TrimRight(body, "\n")
}

// Writes a signed object's message as an indented subject line followed by its
// body.
func writeSignMessage(w io.Writer, msg string) {
	fmt.Fprintf(w, "\n    %s\n", commitSubject(msg))
	if body := commitBody(msg); body != "" {
		for line := range strings.SplitSeq(body, "\n") {
			fmt.Fprintf(w, "    %s\n", line)
		}
	}
}

// FormatHistory outputs history entries as a table.
func (f *Formatter) FormatHistory(entries []HistoryEntry) error {
	if f.asJSON {
		return json.NewEncoder(f.w).Encode(entries)
	}

	if len(entries) == 0 {
		fmt.Fprintln(f.w, "No history entries")
		return nil
	}

	t := requestTable(
		[]column[HistoryEntry]{
			{title: "RESULT", width: 10, value: func(e HistoryEntry) string { return string(e.Resolution) }},
			{title: "SUMMARY", width: 20, value: entrySummary[HistoryEntry]},
		},
		lastColumn[HistoryEntry]{title: "RESOLVED", value: func(e HistoryEntry) string { return formatAgo(e.ResolvedAt) }},
	)
	t.writeHeader(f.w)
	for _, entry := range entries {
		t.writeRow(f.w, entry)
	}
	return nil
}

func formatAgo(t time.Time) string {
	ago := time.Since(t).Round(time.Second)
	if ago < 0 {
		return "just now"
	}
	return ago.String() + " ago"
}

// Writes the resolution that the given decision gave the request with the
// given ID.
func (f *Formatter) FormatDecision(d Decision, id approval.RequestID) error {
	if f.asJSON {
		return json.NewEncoder(f.w).Encode(struct {
			Status approval.Resolution `json:"status"`
			ID     approval.RequestID  `json:"id"`
		}{d.resolution(), id})
	}
	fmt.Fprintf(f.w, "Request %s: %s\n", id, d.resolution())
	return nil
}

// Returns the collection of the request's first item, or false when it names
// none.
func requestCollection(req RequestRecord) (dbustypes.CollectionRef, bool) {
	if len(req.Items) == 0 {
		return dbustypes.CollectionRef{}, false
	}
	return dbustypes.CollectionOf(dbus.ObjectPath(req.Items[0].Path))
}

func formatItemAttrs(w io.Writer, attrs map[string]string, indent string) {
	if len(attrs) == 0 {
		return
	}
	keys := make([]string, 0, len(attrs))
	for k := range attrs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Fprintf(w, "%sAttrs:\n", indent)
	for _, k := range keys {
		fmt.Fprintf(w, "%s  %s: %s\n", indent, k, attrs[k])
	}
}

func formatAttrs(attrs map[string]string) string {
	parts := make([]string, 0, len(attrs))
	for k, v := range attrs {
		parts = append(parts, fmt.Sprintf("%s=%s", k, v))
	}
	return strings.Join(parts, ", ")
}
