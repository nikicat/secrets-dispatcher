package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

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

// FormatRequests outputs a list of pending requests as a table.
func (f *Formatter) FormatRequests(requests []PendingRequest) error {
	if f.asJSON {
		return json.NewEncoder(f.w).Encode(requests)
	}

	if len(requests) == 0 {
		fmt.Fprintln(f.w, "No pending requests")
		return nil
	}

	// Print header
	fmt.Fprintf(f.w, "%-8s  %-20s  %7s  %5s  %-12s  %-15s  %-25s  %s\n", "ID", "CLIENT", "PID", "UID", "TYPE", "COLLECTION", "SUMMARY", "EXPIRES")
	fmt.Fprintf(f.w, "%-8s  %-20s  %7s  %5s  %-12s  %-15s  %-25s  %s\n", "--------", "--------------------", "-------", "-----", "------------", "---------------", "-------------------------", "-------")

	for _, req := range requests {
		id := truncate(req.ID, 8)
		client := truncate(req.Client, 20)
		pid := formatPID(req.SenderInfo.PID)
		uid := formatUID(req.SenderInfo.UID)
		reqType := truncate(req.Type, 12)
		coll := truncate(extractCollection(req), 15)
		if coll == "" {
			coll = "-"
		}
		secret := truncate(requestSummary(req), 25)
		remaining := formatRemaining(req.ExpiresAt)

		fmt.Fprintf(f.w, "%-8s  %-20s  %7s  %5s  %-12s  %-15s  %-25s  %s\n", id, client, pid, uid, reqType, coll, secret, remaining)
	}
	return nil
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-1] + "…"
}

func requestSummary(req PendingRequest) string {
	if req.GPGSignInfo != nil {
		n := len(req.GPGSignInfo.ChangedFiles)
		if n == 1 {
			return "1 file"
		}
		return fmt.Sprintf("%d files", n)
	}
	if len(req.Items) == 0 {
		if len(req.SearchAttributes) > 0 {
			return formatAttrs(req.SearchAttributes)
		}
		return "-"
	}
	if len(req.Items) == 1 {
		return req.Items[0].Label
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

func formatPID(pid uint32) string {
	if pid == 0 {
		return "-"
	}
	return fmt.Sprintf("%d", pid)
}

func formatUID(uid uint32) string {
	return fmt.Sprintf("%d", uid)
}

// FormatShowResult outputs a single request (pending or resolved).
func (f *Formatter) FormatShowResult(result *ShowResult) error {
	if f.asJSON {
		return json.NewEncoder(f.w).Encode(result)
	}
	f.formatRequest(&result.Request)
	if result.Resolution != "" {
		fmt.Fprintf(f.w, "Result:  %s\n", result.Resolution)
		fmt.Fprintf(f.w, "Resolved: %s (%s)\n", result.ResolvedAt.Format(time.RFC3339), formatAgo(result.ResolvedAt))
	}
	return nil
}

func (f *Formatter) formatRequest(req *PendingRequest) {
	fmt.Fprintf(f.w, "ID:      %s\n", req.ID)
	fmt.Fprintf(f.w, "Client:  %s\n", req.Client)
	fmt.Fprintf(f.w, "Type:    %s\n", req.Type)

	if coll := extractCollection(*req); coll != "" {
		fmt.Fprintf(f.w, "Coll:    %s\n", coll)
	}

	// Sender process info
	if req.SenderInfo.UnitName != "" {
		fmt.Fprintf(f.w, "Process: %s\n", req.SenderInfo.UnitName)
	}
	if req.SenderInfo.PID != 0 {
		fmt.Fprintf(f.w, "PID:     %d\n", req.SenderInfo.PID)
	}
	if req.SenderInfo.UserName != "" {
		fmt.Fprintf(f.w, "User:    %s (UID %d)\n", req.SenderInfo.UserName, req.SenderInfo.UID)
	}
	if req.SenderInfo.Sender != "" {
		fmt.Fprintf(f.w, "Sender:  %s\n", req.SenderInfo.Sender)
	}
	if len(req.SenderInfo.ProcessChain) > 0 {
		fmt.Fprintln(f.w, "Chain:")
		for _, p := range req.SenderInfo.ProcessChain {
			exe := p.Exe
			if exe == "" {
				exe = p.Name
			}
			fmt.Fprintf(f.w, "  %s[%d]", exe, p.PID)
			if p.CWD != "" {
				fmt.Fprintf(f.w, " cwd=%s", p.CWD)
			}
			fmt.Fprintln(f.w)
			if len(p.Args) > 1 {
				fmt.Fprintf(f.w, "    args: %s\n", strings.Join(p.Args[1:], " "))
			}
		}
	}

	if req.GPGSignInfo != nil {
		info := req.GPGSignInfo
		fmt.Fprintf(f.w, "Repo:    %s\n", info.RepoName)
		fmt.Fprintf(f.w, "Author:  %s\n", info.Author)
		fmt.Fprintf(f.w, "Key:     %s\n", info.KeyID)
		fmt.Fprintf(f.w, "\n    %s\n", commitSubject(info.CommitMsg))
		if body := commitBody(info.CommitMsg); body != "" {
			for _, line := range strings.Split(body, "\n") {
				fmt.Fprintf(f.w, "    %s\n", line)
			}
		}
		fmt.Fprintln(f.w)
		fmt.Fprintf(f.w, "Changed files (%d):\n", len(info.ChangedFiles))
		for _, file := range info.ChangedFiles {
			fmt.Fprintf(f.w, "  %s\n", file)
		}
		if info.Committer != "" && info.Committer != info.Author {
			fmt.Fprintf(f.w, "\nCommitter: %s\n", info.Committer)
		}
		if info.ParentHash != "" {
			fmt.Fprintf(f.w, "Parent:    %s\n", info.ParentHash)
		}
	} else {
		if len(req.Items) == 1 {
			fmt.Fprintf(f.w, "Secret:  %s\n", req.Items[0].Label)
			fmt.Fprintf(f.w, "Path:    %s\n", req.Items[0].Path)
			formatItemAttrs(f.w, req.Items[0].Attributes, "  ")
		} else if len(req.Items) > 1 {
			fmt.Fprintf(f.w, "Secrets: %d items\n", len(req.Items))
			for _, item := range req.Items {
				fmt.Fprintf(f.w, "  - %s  %s\n", item.Label, item.Path)
				formatItemAttrs(f.w, item.Attributes, "    ")
			}
		}

		if len(req.SearchAttributes) > 0 {
			attrs := formatAttrs(req.SearchAttributes)
			fmt.Fprintf(f.w, "Query:   %s\n", attrs)
		}
	}

	if req.Session != "" {
		fmt.Fprintf(f.w, "Session: %s\n", req.Session)
	}
	fmt.Fprintf(f.w, "Created: %s\n", req.CreatedAt.Format(time.RFC3339))
	if !req.ExpiresAt.IsZero() {
		remaining := time.Until(req.ExpiresAt).Round(time.Second)
		if remaining > 0 {
			fmt.Fprintf(f.w, "Expires: %s (%s remaining)\n", req.ExpiresAt.Format(time.RFC3339), remaining)
		} else {
			fmt.Fprintf(f.w, "Expires: %s (expired)\n", req.ExpiresAt.Format(time.RFC3339))
		}
	}
}

func commitSubject(msg string) string {
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		return msg[:i]
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

// FormatHistory outputs history entries as a table.
func (f *Formatter) FormatHistory(entries []HistoryEntry) error {
	if f.asJSON {
		return json.NewEncoder(f.w).Encode(entries)
	}

	if len(entries) == 0 {
		fmt.Fprintln(f.w, "No history entries")
		return nil
	}

	// Print header
	fmt.Fprintf(f.w, "%-8s  %-20s  %7s  %5s  %-12s  %-15s  %-10s  %-20s  %s\n", "ID", "CLIENT", "PID", "UID", "TYPE", "COLLECTION", "RESULT", "SUMMARY", "RESOLVED")
	fmt.Fprintf(f.w, "%-8s  %-20s  %7s  %5s  %-12s  %-15s  %-10s  %-20s  %s\n", "--------", "--------------------", "-------", "-----", "------------", "---------------", "----------", "--------------------", "--------")

	for _, entry := range entries {
		id := truncate(entry.Request.ID, 8)
		client := truncate(entry.Request.Client, 20)
		pid := formatPID(entry.Request.SenderInfo.PID)
		uid := formatUID(entry.Request.SenderInfo.UID)
		reqType := truncate(entry.Request.Type, 12)
		coll := truncate(extractCollection(entry.Request), 15)
		if coll == "" {
			coll = "-"
		}
		resolution := truncate(entry.Resolution, 10)
		secret := truncate(requestSummary(entry.Request), 20)
		ago := formatAgo(entry.ResolvedAt)

		fmt.Fprintf(f.w, "%-8s  %-20s  %7s  %5s  %-12s  %-15s  %-10s  %-20s  %s\n", id, client, pid, uid, reqType, coll, resolution, secret, ago)
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

// FormatAction outputs an action result.
func (f *Formatter) FormatAction(action, id string) error {
	if f.asJSON {
		return json.NewEncoder(f.w).Encode(map[string]string{
			"status": action,
			"id":     id,
		})
	}
	fmt.Fprintf(f.w, "Request %s: %s\n", id, action)
	return nil
}

func extractCollection(req PendingRequest) string {
	if len(req.Items) == 0 {
		return ""
	}
	return dbustypes.ExtractCollection(req.Items[0].Path)
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
