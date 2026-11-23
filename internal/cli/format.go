package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	dbustypes "github.com/nikicat/secrets-dispatcher/internal/dbus"
	"github.com/nikicat/secrets-dispatcher/internal/sender"
)

// Writes the CLI's output as JSON or as text.
type Formatter struct {
	w      io.Writer
	asJSON bool
}

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
		[]column[PendingEntry]{{title: "SUMMARY", width: 25, value: func(e PendingEntry) string { return e.Request.Summary() }}},
		lastColumn[PendingEntry]{title: "EXPIRES", value: func(e PendingEntry) string { return timeLeftUntil(e.ExpiresAt).String() }},
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
			if name, ok := e.Record().Collection(); ok {
				return string(name)
			}
			return ""
		}},
	}
}

func truncate(s string, maxLen int) string {
	r := []rune(s)
	if len(r) <= maxLen {
		return s
	}
	return string(r[:maxLen-1]) + "…"
}

// Returns a short description of what the request asks for.
func (req RequestRecord) Summary() string {
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
			return req.SearchAttributes.String()
		}
		return ""
	}
	if len(req.Items) == 1 {
		return req.Items[0].DisplayLabel()
	}
	return fmt.Sprintf("%d items", len(req.Items))
}

// The time left until a pending request expires, to the second.
type timeLeft time.Duration

func timeLeftUntil(expiresAt time.Time) timeLeft {
	return timeLeft(time.Until(expiresAt).Round(time.Second))
}

func (l timeLeft) expired() bool { return l <= 0 }

// Returns the time left, or "expired".
func (l timeLeft) String() string {
	if l.expired() {
		return "expired"
	}
	return time.Duration(l).String()
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
	var d details
	d.request(&e.Request)
	d.expiry(e.ExpiresAt)
	return d.String()
}

func (e HistoryEntry) String() string {
	var d details
	d.request(&e.Request)
	d.field("Result", string(e.Resolution))
	d.field("Resolved", fmt.Sprintf("%s (%s)", e.ResolvedAt.Format(time.RFC3339), formatAgo(e.ResolvedAt)))
	return d.String()
}

// A request written as text, one labeled field per line.
type details struct{ strings.Builder }

// The width of a field's label and its colon, which fits the longest label.
const labelWidth = len("Committer:")

// Writes a line of the given label and value, with every value in one column.
func (d *details) field(label, value string) {
	fmt.Fprintf(d, "%-*s %s\n", labelWidth, label+":", value)
}

func (d *details) request(req *RequestRecord) {
	d.field("ID", string(req.ID))
	d.field("Client", string(req.Client))
	d.field("Type", string(req.Type))
	if name, ok := req.Collection(); ok {
		d.field("Coll", string(name))
	}
	if req.Session != "" {
		d.field("Session", string(req.Session))
	}
	d.sender(req.SenderInfo)
	if req.GPGSignInfo != nil {
		d.signInfo(req.GPGSignInfo)
	} else {
		d.items(req)
	}
	d.field("Created", req.CreatedAt.Format(time.RFC3339))
}

// Writes the sender process and its process chain.
func (d *details) sender(info sender.Info) {
	if name := info.DisplayName(); name != "" {
		d.field("Process", name)
	}
	if info.PID != 0 {
		d.field("PID", info.PID.String())
	}
	if s := info.Sender; s != nil {
		d.field("Sender", string(*s))
	}
	if len(info.ProcessChain) == 0 {
		return
	}
	fmt.Fprintln(d, "Chain:")
	for _, p := range info.ProcessChain {
		exe := p.Name
		if path, ok := p.ExePath(); ok {
			exe = string(path)
		}
		fmt.Fprintf(d, "  %s[%d]", exe, p.PID)
		if cwd, ok := p.CWDPath(); ok {
			fmt.Fprintf(d, " cwd=%s", cwd)
		}
		fmt.Fprintln(d)
		if len(p.Args) > 1 {
			fmt.Fprintf(d, "    args: %s\n", strings.Join(p.Args[1:], " "))
		}
	}
}

// Writes the repository and the object to sign, with the fields of its kind.
func (d *details) signInfo(info *GPGSignInfo) {
	d.field("Repo", string(info.RepoName))
	switch info.Kind {
	case approval.GPGSignTag:
		d.tag(info)
	case approval.GPGSignPush:
		d.push(info)
	case approval.GPGSignCommit:
		d.commit(info)
	}
}

func (d *details) tag(info *GPGSignInfo) {
	d.field("Signing", "annotated tag")
	if info.TagName != "" {
		d.field("Tag", string(info.TagName))
	}
	d.field("Tagger", info.Author)
	d.field("Key", string(info.KeyID))
	if info.Target != "" {
		d.field("Target", string(info.Target))
	}
	d.signMessage(info.CommitMsg)
}

func (d *details) push(info *GPGSignInfo) {
	d.field("Signing", "signed push")
	d.field("Pusher", info.Author)
	if info.Pushee != "" {
		d.field("Pushee", string(info.Pushee))
	}
	d.field("Key", string(info.KeyID))
	fmt.Fprintln(d, "\nRef updates:")
	for line := range strings.SplitSeq(info.CommitMsg, "\n") {
		if line != "" {
			fmt.Fprintf(d, "  %s\n", line)
		}
	}
}

func (d *details) commit(info *GPGSignInfo) {
	d.field("Author", info.Author)
	d.field("Key", string(info.KeyID))
	d.signMessage(info.CommitMsg)
	fmt.Fprintln(d)
	fmt.Fprintf(d, "Changed files (%d):\n", len(info.ChangedFiles))
	for _, file := range info.ChangedFiles {
		fmt.Fprintf(d, "  %s\n", file)
	}
	if info.Committer != "" && info.Committer != info.Author {
		fmt.Fprintln(d)
		d.field("Committer", info.Committer)
	}
	if info.ParentHash != "" {
		d.field("Parent", string(info.ParentHash))
	}
}

// Writes the secret items that the request reads, and its search query.
func (d *details) items(req *RequestRecord) {
	if len(req.Items) == 1 {
		d.field("Secret", req.Items[0].Label)
		d.field("Path", req.Items[0].Path)
		d.itemAttrs(req.Items[0].Attributes, "  ")
	} else if len(req.Items) > 1 {
		d.field("Secrets", fmt.Sprintf("%d items", len(req.Items)))
		for _, item := range req.Items {
			fmt.Fprintf(d, "  - %s  %s\n", item.Label, item.Path)
			d.itemAttrs(item.Attributes, "    ")
		}
	}
	if len(req.SearchAttributes) > 0 {
		d.field("Query", req.SearchAttributes.String())
	}
}

func (d *details) expiry(expiresAt time.Time) {
	left := timeLeftUntil(expiresAt)
	status := left.String()
	if !left.expired() {
		status += " remaining"
	}
	d.field("Expires", fmt.Sprintf("%s (%s)", expiresAt.Format(time.RFC3339), status))
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
func (d *details) signMessage(msg string) {
	fmt.Fprintf(d, "\n    %s\n", commitSubject(msg))
	if body := commitBody(msg); body != "" {
		for line := range strings.SplitSeq(body, "\n") {
			fmt.Fprintf(d, "    %s\n", line)
		}
	}
}

// Writes the history entries as JSON or as a table.
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
			{title: "SUMMARY", width: 20, value: func(e HistoryEntry) string { return e.Request.Summary() }},
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

// Returns the collection of the request's first item.
func (req RequestRecord) Collection() (dbustypes.CollectionName, bool) {
	if len(req.Items) == 0 || req.Items[0].Collection == nil {
		return "", false
	}
	return *req.Items[0].Collection, true
}

func (d *details) itemAttrs(attrs dbustypes.Attributes, indent string) {
	if len(attrs) == 0 {
		return
	}
	fmt.Fprintf(d, "%sAttrs:\n", indent)
	for _, k := range slices.Sorted(maps.Keys(attrs)) {
		fmt.Fprintf(d, "%s  %s: %s\n", indent, k, attrs[k])
	}
}
