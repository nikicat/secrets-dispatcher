package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/procchain"
)

// requestItem holds one entry in the list pane, either an active pending
// request or a resolved historical item.
type requestItem struct {
	request    *approval.Request
	procChain  []procchain.ProcInfo
	resolved   bool
	resolution approval.Resolution
}

// listPane is the left-hand pane that shows pending requests (with type badge,
// description, and countdown) and a dimmed history section of resolved items.
type listPane struct {
	items   []requestItem // active pending requests; cursor stays here
	history []requestItem // resolved requests (dimmed, shown after separator)
	cursor  int           // selected index into items
	width   int
	height  int
}

// SetSize updates the pane dimensions. Call this on tea.WindowSizeMsg.
func (p *listPane) SetSize(w, h int) {
	p.width = w
	p.height = h
}

// AddRequest appends a new pending request without moving the cursor.
func (p *listPane) AddRequest(req *approval.Request, chain []procchain.ProcInfo) {
	p.items = append(p.items, requestItem{request: req, procChain: chain})
}

// ResolveRequest moves the item with the given ID from items to history.
// Returns true if the item was found and moved.
func (p *listPane) ResolveRequest(id string, resolution approval.Resolution) bool {
	for i, item := range p.items {
		if item.request.ID == id {
			item.resolved = true
			item.resolution = resolution
			// Prepend to history (newest first).
			p.history = append([]requestItem{item}, p.history...)
			p.items = append(p.items[:i], p.items[i+1:]...)
			// Keep cursor in bounds.
			if p.cursor >= len(p.items) && p.cursor > 0 {
				p.cursor = len(p.items) - 1
			}
			return true
		}
	}
	return false
}

// SelectedItem returns a pointer to the currently selected item, or nil if the
// list is empty.
func (p *listPane) SelectedItem() *requestItem {
	if len(p.items) == 0 {
		return nil
	}
	if p.cursor < 0 || p.cursor >= len(p.items) {
		return nil
	}
	return &p.items[p.cursor]
}

// CursorUp moves the cursor up one position (minimum 0).
func (p *listPane) CursorUp() {
	if p.cursor > 0 {
		p.cursor--
	}
}

// CursorDown moves the cursor down one position (maximum len(items)-1).
func (p *listPane) CursorDown() {
	if p.cursor < len(p.items)-1 {
		p.cursor++
	}
}

// View renders the list pane as a string. The caller wraps it in a border via
// listPaneStyle so this function renders only the inner content.
func (p *listPane) View() string {
	// Inner width is the pane width minus the border (2 chars).
	innerW := p.width - 2
	if innerW <= 0 {
		innerW = 10
	}

	var sb strings.Builder

	if len(p.items) == 0 && len(p.history) == 0 {
		sb.WriteString(dimmedStyle.Render("No pending requests"))
		return sb.String()
	}

	// Render active pending items.
	for i, item := range p.items {
		line := p.renderItemLine(item, innerW)
		if i == p.cursor {
			sb.WriteString(selectedItemStyle.Width(innerW).Render(line))
		} else {
			sb.WriteString(normalItemStyle.Render(line))
		}
		sb.WriteByte('\n')
	}

	// Render history section if non-empty.
	if len(p.history) > 0 {
		if len(p.items) > 0 {
			sb.WriteString(historySectionStyle.Render(strings.Repeat("─", innerW)))
			sb.WriteByte('\n')
		}
		sb.WriteString(historySectionStyle.Render("Recent"))
		sb.WriteByte('\n')
		for _, item := range p.history {
			sb.WriteString(dimmedStyle.Render(p.renderHistoryLine(item, innerW)))
			sb.WriteByte('\n')
		}
	}

	return strings.TrimRight(sb.String(), "\n")
}

// renderItemLine renders a single active item line:
//
//	[BADGE] description             mm:ss
func (p *listPane) renderItemLine(item requestItem, innerW int) string {
	badge := p.badgeFor(item.request)
	desc := p.descFor(item.request)
	countdown := p.countdownFor(item.request)

	// The countdown is right-justified; badge + desc fills the left.
	countdownW := utf8.RuneCountInString(countdown)
	availForDesc := innerW - len(badge) - 1 - countdownW - 1 // 1 space after badge, 1 before countdown
	if availForDesc < 4 {
		availForDesc = 4
	}
	if utf8.RuneCountInString(desc) > availForDesc {
		desc = truncate(desc, availForDesc-1) + "…"
	}

	// Build: "badge desc ... countdown"
	left := badge + " " + desc
	padding := innerW - utf8.RuneCountInString(left) - countdownW
	if padding < 1 {
		padding = 1
	}
	return left + strings.Repeat(" ", padding) + countdown
}

// renderHistoryLine renders a single history item line (dimmed).
//
//	[BADGE] description   approved
func (p *listPane) renderHistoryLine(item requestItem, innerW int) string {
	badge := p.badgeFor(item.request)
	desc := p.descFor(item.request)
	outcome := string(item.resolution)

	outcomeW := len(outcome)
	availForDesc := innerW - len(badge) - 1 - outcomeW - 1
	if availForDesc < 4 {
		availForDesc = 4
	}
	if utf8.RuneCountInString(desc) > availForDesc {
		desc = truncate(desc, availForDesc-1) + "…"
	}

	left := badge + " " + desc
	padding := innerW - utf8.RuneCountInString(left) - outcomeW
	if padding < 1 {
		padding = 1
	}
	return left + strings.Repeat(" ", padding) + outcome
}

// badgeFor returns the styled type badge for a request.
// Styling is applied by the caller's style (selected/dimmed), so we return
// plain text here to keep styling composable.
func (p *listPane) badgeFor(req *approval.Request) string {
	switch req.Type {
	case approval.RequestTypeGPGSign:
		return signBadgeStyle.Render("[SIGN]")
	case approval.RequestTypeSearch:
		return searchBadgeStyle.Render("[SEARCH]")
	default: // RequestTypeGetSecret and unknown types
		return secretBadgeStyle.Render("[SECRET]")
	}
}

// descFor returns a short description of the request for the list pane.
func (p *listPane) descFor(req *approval.Request) string {
	switch req.Type {
	case approval.RequestTypeGPGSign:
		if req.GPGSignInfo != nil && req.GPGSignInfo.CommitMsg != "" {
			// First line of commit message, max 30 chars.
			msg := strings.SplitN(req.GPGSignInfo.CommitMsg, "\n", 2)[0]
			return msg
		}
		return "(no commit message)"
	case approval.RequestTypeSearch:
		// Show the first search attribute or the client name.
		if len(req.SearchAttributes) > 0 {
			for _, v := range req.SearchAttributes {
				return v
			}
		}
		return req.Client
	default: // get_secret
		if len(req.Items) > 0 {
			return req.Items[0].Path
		}
		return req.Client
	}
}

// countdownFor returns a styled mm:ss countdown string for the item.
func (p *listPane) countdownFor(req *approval.Request) string {
	remaining := time.Until(req.ExpiresAt)
	if remaining < 0 {
		remaining = 0
	}
	mins := int(remaining.Minutes())
	secs := int(remaining.Seconds()) % 60
	text := fmt.Sprintf("%02d:%02d", mins, secs)

	switch {
	case remaining < 30*time.Second:
		return countdownUrgentStyle.Render(text)
	case remaining < 60*time.Second:
		return countdownWarnStyle.Render(text)
	default:
		return countdownNormalStyle.Render(text)
	}
}

// truncate truncates s to at most n runes.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	count := 0
	for i := range s {
		if count >= n {
			return s[:i]
		}
		count++
	}
	return s
}
