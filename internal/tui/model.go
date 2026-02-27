package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/nikicat/secrets-dispatcher/internal/approval"
	"github.com/nikicat/secrets-dispatcher/internal/procchain"
)

// Config holds the configuration for the TUI model.
type Config struct {
	// LockMode controls VT_PROCESS mode engagement.
	LockMode LockMode

	// VTPath is the path to the VT device (e.g. "/dev/tty8").
	VTPath string

	// VTFD is the file descriptor of the opened VT device.
	// Used by LockVT/UnlockVT when LockMode is not LockModeNone.
	VTFD uintptr

	// CompanionUser is the companion Linux username.
	CompanionUser string

	// StartTime is when the daemon started (for idle state uptime display).
	StartTime time.Time
}

// Model is the root bubbletea model for the approval TUI. It owns the list
// and detail panes and routes all messages through the Elm architecture.
//
// approveFunc and denyFunc are injected callbacks so the model does not hold a
// direct reference to approval.Manager, making it straightforward to test.
type Model struct {
	list   listPane
	detail detailPane
	config Config

	vtLocked bool
	width    int
	height   int
	statusMsg string // transient status line

	approveFunc func(id string) error
	denyFunc    func(id string) error
}

// NewModel creates a new TUI Model. approveFn and denyFn are called
// asynchronously when the user presses y or n respectively.
func NewModel(cfg Config, approveFn, denyFn func(string) error) Model {
	m := Model{
		config:      cfg,
		approveFunc: approveFn,
		denyFunc:    denyFn,
	}
	m.detail.SetIdle(0, cfg.CompanionUser, cfg.LockMode)
	return m
}

// Init implements tea.Model. Returns a tickCmd to start the countdown timer.
func (m Model) Init() tea.Cmd {
	return tickCmd()
}

// tickCmd fires a TickMsg every second to drive countdown updates.
func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return TickMsg(t)
	})
}

// approveCmd returns a tea.Cmd that calls approveFn asynchronously.
func approveCmd(approveFn func(string) error, id string) tea.Cmd {
	return func() tea.Msg {
		err := approveFn(id)
		return ApproveResultMsg{ID: id, Err: err}
	}
}

// denyCmd returns a tea.Cmd that calls denyFn asynchronously.
func denyCmd(denyFn func(string) error, id string) tea.Cmd {
	return func() tea.Msg {
		err := denyFn(id)
		return ApproveResultMsg{ID: id, Err: err}
	}
}

// Update implements tea.Model. Routes all bubbletea messages through the Elm
// update cycle and returns the new model state plus any commands to run.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		leftW := m.width / 3
		rightW := m.width - leftW
		// Subtract 2 from each dimension to account for lipgloss border.
		m.list.SetSize(leftW, m.height-2)
		m.detail.SetSize(rightW, m.height-2)

	case NewRequestMsg:
		m.list.AddRequest(msg.Request, msg.ProcChain)
		// Auto-select the first item if nothing was selected.
		if len(m.list.items) == 1 {
			m.detail.SetItem(m.list.SelectedItem())
			// In auto-lock mode, lock the VT when a request is selected.
			if m.config.LockMode == LockModeAuto && !m.vtLocked {
				m.engageLock()
			}
		}

	case RequestResolvedMsg:
		wasSelected := m.list.SelectedItem() != nil &&
			m.list.SelectedItem().request.ID == msg.ID
		m.list.ResolveRequest(msg.ID, msg.Resolution)
		// Update detail pane after the list state changes.
		if wasSelected {
			m.detail.SetItem(m.list.SelectedItem())
			// In auto-lock mode, unlock if no item is selected now.
			if m.config.LockMode == LockModeAuto && len(m.list.items) == 0 {
				m.releaseLock()
			}
		}

	case TickMsg:
		// Tick drives countdown re-render; just continue ticking.
		return m, tickCmd()

	case ApproveResultMsg:
		if msg.Err != nil {
			m.statusMsg = fmt.Sprintf("Error: %v", msg.Err)
		} else {
			m.statusMsg = ""
		}

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

// handleKey processes keyboard input and returns the updated model.
func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		m.list.CursorUp()
		m.detail.SetItem(m.list.SelectedItem())

	case "down", "j":
		m.list.CursorDown()
		m.detail.SetItem(m.list.SelectedItem())

	case "y":
		if m.canApprove() {
			item := m.list.SelectedItem()
			return m, approveCmd(m.approveFunc, item.request.ID)
		}

	case "n":
		if m.canApprove() {
			item := m.list.SelectedItem()
			return m, denyCmd(m.denyFunc, item.request.ID)
		}

	case "l":
		// Toggle VT lock in manual mode.
		if m.config.LockMode == LockModeManual {
			if m.vtLocked {
				m.releaseLock()
			} else {
				m.engageLock()
			}
		}

	case "esc":
		// Unlock VT in manual or auto mode.
		if m.vtLocked {
			m.releaseLock()
		}

	case "q", "ctrl+c":
		return m, tea.Quit
	}

	return m, nil
}

// canApprove returns true if y/n keystrokes should trigger approve/deny.
// In LockModeNone: always allowed.
// In LockModeManual and LockModeAuto: only when vtLocked.
func (m *Model) canApprove() bool {
	item := m.list.SelectedItem()
	if item == nil || item.resolved {
		return false
	}
	if m.config.LockMode == LockModeNone {
		return true
	}
	return m.vtLocked
}

// engageLock calls LockVT and sets vtLocked. Logs on failure but does not
// return an error — the model degrades gracefully to LockModeNone behavior.
func (m *Model) engageLock() {
	if m.config.VTFD != 0 {
		if err := LockVT(m.config.VTFD); err != nil {
			m.statusMsg = fmt.Sprintf("VT lock failed: %v (degraded to no-lock mode)", err)
			return
		}
	}
	m.vtLocked = true
}

// releaseLock calls UnlockVT and clears vtLocked.
func (m *Model) releaseLock() {
	if m.config.VTFD != 0 {
		_ = UnlockVT(m.config.VTFD) // failure is non-fatal
	}
	m.vtLocked = false
}

// View implements tea.Model. Renders the full TUI as a string.
func (m Model) View() string {
	if m.width == 0 {
		return "Initializing..."
	}

	leftW := m.width / 3
	rightW := m.width - leftW

	// Inner width/height account for borders (2 chars each dimension).
	left := listPaneStyle.
		Width(leftW - 2).
		Height(m.height - 2 - 1). // -1 for status bar row
		Render(m.list.View())

	right := detailPaneStyle.
		Width(rightW - 2).
		Height(m.height - 2 - 1). // -1 for status bar row
		Render(m.detail.View())

	panes := lipgloss.JoinHorizontal(lipgloss.Top, left, right)

	// Status bar at the bottom.
	statusBar := m.renderStatusBar()

	return lipgloss.JoinVertical(lipgloss.Left, panes, statusBar)
}

// renderStatusBar builds the status bar line.
func (m *Model) renderStatusBar() string {
	var parts []string

	// Lock mode and status.
	switch m.config.LockMode {
	case LockModeManual:
		if m.vtLocked {
			parts = append(parts, lockedStyle.Render("LOCKED"))
		} else {
			parts = append(parts, statusBarStyle.Render("unlocked"))
		}
	case LockModeAuto:
		if m.vtLocked {
			parts = append(parts, lockedStyle.Render("LOCKED (auto)"))
		}
	}

	// Help text.
	help := "y:approve  n:deny"
	if m.config.LockMode == LockModeManual {
		help += "  l:lock/unlock"
	}
	if m.vtLocked {
		help += "  esc:unlock"
	}
	help += "  ↑↓:navigate  q:quit"
	parts = append(parts, statusBarStyle.Render(help))

	// Transient status message (e.g. error from approve/deny).
	if m.statusMsg != "" {
		parts = append(parts, statusBarStyle.Render("  "+m.statusMsg))
	}

	return strings.Join(parts, "  ")
}

// AddRequest is a convenience helper for external code (e.g. approval.Manager
// observer) to build a NewRequestMsg and send it to a running program.
// The caller sends this message via p.Send(NewRequestMsg{...}).
func NewRequestMessage(req *approval.Request, chain []procchain.ProcInfo) NewRequestMsg {
	return NewRequestMsg{Request: req, ProcChain: chain}
}
