# Phase 5: Core Flow - Context

**Gathered:** 2026-02-26
**Status:** Ready for planning

<domain>
## Phase Boundary

End-to-end secret request and GPG signing flows work through the VT TUI: request arrives on system D-Bus, appears on a dedicated VT in a fullscreen two-pane TUI, keyboard y/n resolves it, result is returned to the caller. Builds on Phase 4's daemon skeleton and D-Bus registration. Desktop agent and notifications are Phase 6; store lock/unlock and CLI mode are Phase 7.

</domain>

<decisions>
## Implementation Decisions

### TUI framework & layout
- Use bubbletea (Elm architecture) + lipgloss for the VT TUI
- Fullscreen two-pane layout: narrow left pane (request list), wide right pane (request detail)
- Left pane items show type badge + short description: `[SECRET] github token`, `[SIGN] fix: auth flow`
- Arrow keys to navigate the list; selected item's full detail shown in right pane
- New requests appear in list without stealing focus — user navigates to them when ready
- Resolved requests fade to a "Recent" history section at bottom of list (dimmed, showing outcome)
- Live countdown timer shown per item in the left pane (not the detail pane)

### TUI detail pane content
- Secret requests: secret path, requester PID/UID/process name, working directory, parent process chain (up to 5 levels)
- GPG signing requests: repo name, commit message, author, changed file names + stat summary line (e.g. "3 files changed, +47, -12"), key ID
- Process chain displayed for both request types

### Approval interaction
- y/n keystrokes act immediately — no confirmation dialog
- y/n only active when VT is in locked state (for manual and auto lock modes)
- Three configurable VT lock modes:
  1. **No locking** — y/n work anytime, no VT_PROCESS mode
  2. **Manual lock/unlock** — user explicitly locks VT, can only approve/deny while locked
  3. **Auto lock on select** — VT locks when a pending request is selected, unlocks when cursor moves away or request is resolved
- Esc to unlock VT (in manual and auto modes), then normal Ctrl+Alt+Fn to switch

### VT allocation & lifecycle
- VT number configurable (flag/config), defaults to VT8
- Daemon claims VT (opens /dev/ttyN, initializes TUI) on startup, not lazily on first request
- Idle state: same two-pane layout with empty list and status info in right pane (daemon uptime, companion user, lock mode, last activity)
- No desktop notification in Phase 5 — user manually switches to VT to check; Phase 6 agent handles awareness

### Crash recovery
- If daemon crashes while holding VT_PROCESS mode, cleanup handlers + signal handlers return VT to VT_AUTO
- User can switch VTs normally after crash

### Caller-side experience
- Denied requests return custom D-Bus error with reason (e.g. `net.mowaka.Error.Denied` with "Request denied by user")
- Timed-out requests return error mentioning VT: "Request timed out — approve on VT8 (Ctrl+Alt+F8)"
- D-Bus activation file installed so bus attempts to start daemon; returns actionable error if daemon can't start
- GPG thin client prints single status line to stderr while blocking: "Waiting for approval on VT8..."

### Claude's Discretion
- D-Bus method signatures and interface design (freedesktop Secret Service subset vs custom)
- bubbletea model/update/view architecture and component breakdown
- Process chain retrieval implementation (/proc traversal)
- Exact lipgloss styling (colors, borders, spacing)
- Integration test strategy for VT interactions (mock TTY)
- D-Bus activation file format and error messaging details
- How lock mode configuration is stored/switched

</decisions>

<specifics>
## Specific Ideas

- Master-detail pattern: left pane is the scannable index, right pane is the full context for informed decisions — similar to email clients or file managers
- The VT lock mode spectrum (none → manual → auto) lets users choose their security posture — paranoid users lock every time, convenience users skip it
- Countdown timer on the left pane means you can scan urgency across all pending requests without selecting each one
- Resolved items staying as dimmed history provides audit context of recent actions

</specifics>

<deferred>
## Deferred Ideas

None — discussion stayed within phase scope

</deferred>

---

*Phase: 05-core-flow*
*Context gathered: 2026-02-26*
