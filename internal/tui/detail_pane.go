package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/procchain"
)

// detailPane renders the right-hand pane with full context for the selected
// request, or an idle status screen when no request is selected.
type detailPane struct {
	item   *requestItem // currently displayed item; nil = idle state
	width  int
	height int

	// Idle state info fields.
	idleUptime        time.Duration
	idleCompanionUser string
	idleLockMode      LockMode
}

// SetSize updates the pane dimensions. Call this on tea.WindowSizeMsg.
func (p *detailPane) SetSize(w, h int) {
	p.width = w
	p.height = h
}

// SetItem sets the currently displayed item. Pass nil to show idle state.
func (p *detailPane) SetItem(item *requestItem) {
	p.item = item
}

// SetIdle updates the idle-state display fields.
func (p *detailPane) SetIdle(uptime time.Duration, companionUser string, lockMode LockMode) {
	p.idleUptime = uptime
	p.idleCompanionUser = companionUser
	p.idleLockMode = lockMode
}

// View renders the detail pane content. The caller wraps it in a border via
// detailPaneStyle.
func (p *detailPane) View() string {
	if p.item == nil {
		return p.viewIdle()
	}
	if p.item.resolved {
		return p.viewResolved(p.item)
	}
	switch p.item.request.Type {
	case approval.RequestTypeGPGSign:
		return p.viewGPGSign(p.item)
	case approval.RequestTypeSearch:
		return p.viewSearch(p.item)
	default:
		return p.viewSecret(p.item)
	}
}

// viewIdle renders the idle state (no request selected).
func (p *detailPane) viewIdle() string {
	var sb strings.Builder
	sb.WriteString(idleTitleStyle.Render("secrets-dispatcher"))
	sb.WriteString("\n\n")

	lockModeName := lockModeName(p.idleLockMode)
	sb.WriteString(kv("Companion user", p.idleCompanionUser))
	sb.WriteString(kv("Lock mode", lockModeName))
	sb.WriteString(kv("Uptime", formatDuration(p.idleUptime)))
	sb.WriteString("\n")
	sb.WriteString(statusBarStyle.Render("Select a request from the left pane to review it."))

	return sb.String()
}

// viewResolved renders a resolved item in the detail pane (shows outcome).
func (p *detailPane) viewResolved(item *requestItem) string {
	var sb strings.Builder
	sb.WriteString(headerStyle.Render("Resolved"))
	sb.WriteString("\n\n")
	sb.WriteString(kv("ID", item.request.ID))
	sb.WriteString(kv("Outcome", string(item.resolution)))
	return sb.String()
}

// viewSecret renders the detail view for a get_secret request.
func (p *detailPane) viewSecret(item *requestItem) string {
	req := item.request
	var sb strings.Builder

	sb.WriteString(headerStyle.Render("Secret Request"))
	sb.WriteString("\n\n")

	// Secret paths
	if len(req.Items) > 0 {
		sb.WriteString(keyStyle.Render("Path"))
		sb.WriteString("\n")
		for _, it := range req.Items {
			sb.WriteString("  ")
			sb.WriteString(valueStyle.Render(it.Path))
			sb.WriteByte('\n')
		}
		sb.WriteByte('\n')
	}

	// Sender info
	sb.WriteString(keyStyle.Render("Requester"))
	sb.WriteByte('\n')
	sb.WriteString(kvi("PID", fmt.Sprintf("%d", req.SenderInfo.PID)))
	sb.WriteString(kvi("UID", fmt.Sprintf("%d", req.SenderInfo.UID)))
	if req.SenderInfo.UserName != "" {
		sb.WriteString(kvi("User", req.SenderInfo.UserName))
	}
	if req.SenderInfo.UnitName != "" {
		sb.WriteString(kvi("Unit", req.SenderInfo.UnitName))
	}
	sb.WriteByte('\n')

	// Working directory (from first process in chain)
	if len(item.procChain) > 0 && item.procChain[0].CWD != "" {
		sb.WriteString(kv("Working dir", item.procChain[0].CWD))
	}

	// Process chain
	sb.WriteString(renderProcChain(item.procChain))

	return sb.String()
}

// viewGPGSign renders the detail view for a gpg_sign request.
func (p *detailPane) viewGPGSign(item *requestItem) string {
	req := item.request
	info := req.GPGSignInfo
	var sb strings.Builder

	sb.WriteString(headerStyle.Render("GPG Signing Request"))
	sb.WriteString("\n\n")

	if info != nil {
		sb.WriteString(kv("Repository", info.RepoName))
		sb.WriteString(kv("Key ID", info.KeyID))
		sb.WriteString("\n")

		// Commit message (full, not truncated).
		sb.WriteString(keyStyle.Render("Commit message"))
		sb.WriteByte('\n')
		for _, line := range strings.Split(info.CommitMsg, "\n") {
			sb.WriteString("  ")
			sb.WriteString(valueStyle.Render(line))
			sb.WriteByte('\n')
		}
		sb.WriteByte('\n')

		// Author / committer
		sb.WriteString(kv("Author", info.Author))
		if info.Committer != "" && info.Committer != info.Author {
			sb.WriteString(kv("Committer", info.Committer))
		}
		sb.WriteByte('\n')

		// Changed files with stat summary
		if len(info.ChangedFiles) > 0 {
			sb.WriteString(keyStyle.Render("Changed files"))
			sb.WriteByte('\n')
			for _, f := range info.ChangedFiles {
				sb.WriteString("  ")
				sb.WriteString(valueStyle.Render(f))
				sb.WriteByte('\n')
			}
			// Stat summary line.
			sb.WriteString("  ")
			sb.WriteString(dimmedStyle.Render(fmt.Sprintf("%d file(s) changed", len(info.ChangedFiles))))
			sb.WriteByte('\n')
			sb.WriteByte('\n')
		}

		if info.ParentHash != "" {
			sb.WriteString(kv("Parent", shortHash(info.ParentHash)))
		}
	}

	// Sender info
	sb.WriteString(keyStyle.Render("Requester"))
	sb.WriteByte('\n')
	sb.WriteString(kvi("PID", fmt.Sprintf("%d", req.SenderInfo.PID)))
	sb.WriteString(kvi("UID", fmt.Sprintf("%d", req.SenderInfo.UID)))
	if req.SenderInfo.UserName != "" {
		sb.WriteString(kvi("User", req.SenderInfo.UserName))
	}
	sb.WriteByte('\n')

	// Process chain
	sb.WriteString(renderProcChain(item.procChain))

	return sb.String()
}

// viewSearch renders the detail view for a search request.
func (p *detailPane) viewSearch(item *requestItem) string {
	req := item.request
	var sb strings.Builder

	sb.WriteString(headerStyle.Render("Search Request"))
	sb.WriteString("\n\n")

	// Search attributes
	if len(req.SearchAttributes) > 0 {
		sb.WriteString(keyStyle.Render("Search criteria"))
		sb.WriteByte('\n')
		for k, v := range req.SearchAttributes {
			sb.WriteString(fmt.Sprintf("  %s: %s\n", keyStyle.Render(k), valueStyle.Render(v)))
		}
		sb.WriteByte('\n')
	}

	// Sender info
	sb.WriteString(keyStyle.Render("Requester"))
	sb.WriteByte('\n')
	sb.WriteString(kvi("PID", fmt.Sprintf("%d", req.SenderInfo.PID)))
	sb.WriteString(kvi("UID", fmt.Sprintf("%d", req.SenderInfo.UID)))
	if req.SenderInfo.UserName != "" {
		sb.WriteString(kvi("User", req.SenderInfo.UserName))
	}
	sb.WriteByte('\n')

	// Working directory (from first process in chain)
	if len(item.procChain) > 0 && item.procChain[0].CWD != "" {
		sb.WriteString(kv("Working dir", item.procChain[0].CWD))
	}

	// Process chain
	sb.WriteString(renderProcChain(item.procChain))

	return sb.String()
}

// renderProcChain formats the process chain as an indented tree.
//
//	Process chain:
//	  git (pid 1234)
//	  └─ bash (pid 1200)
//	     └─ tmux: server (pid 900)
func renderProcChain(chain []procchain.ProcInfo) string {
	if len(chain) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(keyStyle.Render("Process chain"))
	sb.WriteByte('\n')

	for i, p := range chain {
		var prefix string
		if i == 0 {
			prefix = "  "
		} else {
			prefix = strings.Repeat("   ", i) + "└─ "
		}
		sb.WriteString(prefix)
		sb.WriteString(valueStyle.Render(fmt.Sprintf("%s (pid %d)", p.Name, p.PID)))
		sb.WriteByte('\n')
	}
	return sb.String()
}

// kv renders a key: value line in the detail pane.
func kv(key, value string) string {
	return keyStyle.Render(key+":") + " " + valueStyle.Render(value) + "\n"
}

// kvi renders an indented key: value line (for sub-fields like PID, UID).
func kvi(key, value string) string {
	return "  " + keyStyle.Render(key+":") + " " + valueStyle.Render(value) + "\n"
}

// lockModeName returns a human-readable name for a LockMode.
func lockModeName(mode LockMode) string {
	switch mode {
	case LockModeManual:
		return "manual"
	case LockModeAuto:
		return "auto"
	default:
		return "none"
	}
}

// formatDuration formats a duration as a human-readable string.
func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

// shortHash returns the first 8 characters of a git hash.
func shortHash(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}
