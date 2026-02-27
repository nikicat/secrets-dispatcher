---
phase: 05-core-flow
plan: 02
subsystem: ui
tags: [bubbletea, lipgloss, tui, vt, approval, procchain]

# Dependency graph
requires:
  - phase: 05-core-flow/05-01
    provides: procchain.Walk(), tui.LockMode, tui.LockVT/UnlockVT, companion tty group
  - phase: 04-foundation
    provides: approval.Manager, approval.Request, SenderInfo, GPGSignInfo, Resolution types

provides:
  - bubbletea two-pane TUI model (list + detail) for VT-based approval UI
  - NewRequestMsg/RequestResolvedMsg/TickMsg/ApproveResultMsg message types
  - listPane: request list with type badges, countdown timers, resolved history section
  - detailPane: rich context rendering for secret, GPG sign, and search requests
  - lipgloss style definitions for all TUI components
  - 11 unit tests exercising model state machine via direct Update() calls

affects:
  - 05-03 (D-Bus handlers wire approval.Manager events to these TUI messages)
  - 05-04 (daemon.Run() wires tui.Model into tea.NewProgram with VT fd)

# Tech tracking
tech-stack:
  added:
    - github.com/charmbracelet/bubbletea v1.3.10 (Elm architecture TUI event loop)
    - github.com/charmbracelet/lipgloss v1.1.0 (terminal styling, two-pane layout)
    - github.com/charmbracelet/bubbles v1.0.0 (bubbles utilities, unused directly but transitive)
  patterns:
    - Elm architecture: Model.Init/Update/View + tea.Cmd for async operations
    - Injected approveFn/denyFn callbacks instead of direct approval.Manager reference (testability)
    - Direct model.Update() testing without p.Send() or bubbletea program startup
    - Async approve/deny via tea.Cmd returning ApproveResultMsg (prevents blocking event loop)
    - stripANSI helper for asserting on ANSI-styled View() output in tests

key-files:
  created:
    - internal/tui/messages.go: NewRequestMsg, RequestResolvedMsg, TickMsg, ApproveResultMsg
    - internal/tui/styles.go: lipgloss style definitions (badges, countdown, pane borders)
    - internal/tui/list_pane.go: listPane with cursor nav, badges, countdown, history
    - internal/tui/detail_pane.go: detailPane with secret/GPG/search/idle rendering
    - internal/tui/model.go: root Model implementing tea.Model, Config, NewModel
    - internal/tui/model_test.go: 11 unit tests via direct model.Update() calls
  modified:
    - go.mod: added bubbletea, lipgloss, bubbles and their transitive dependencies
    - go.sum: updated checksums

key-decisions:
  - "Custom listPane (not bubbles/list) for split active+history display: simpler than fighting bubbles/list item interface for the specific dual-section layout"
  - "Injected approveFn/denyFn callbacks in Model: approval.Manager not referenced directly; tests use closures"
  - "Async approve/deny via tea.Cmd: prevents blocking Update() while D-Bus call in flight"
  - "stripANSI helper in tests: allows asserting on View() output despite lipgloss styling"
  - "countdownNormal/Warn/Urgent styles at >60s/30-60s/<30s thresholds for visual urgency"

patterns-established:
  - "TUI test pattern: call model.Update(msg) directly, assert on model.View() after stripping ANSI"
  - "Badge rendering: secretBadgeStyle/signBadgeStyle/searchBadgeStyle for type identification"
  - "Process chain tree: indented with └─ prefix for each ancestor level"

requirements-completed: [VT-03, VT-04, VT-06, TEST-01]

# Metrics
duration: 10min
completed: 2026-02-27
---

# Phase 5 Plan 02: Core Flow TUI Summary

**bubbletea two-pane approval TUI with type-badged list, countdown timers, process chain rendering, and lock-mode-aware y/n keyboard approvals**

## Performance

- **Duration:** 10 min
- **Started:** 2026-02-27T10:44:47Z
- **Completed:** 2026-02-27T10:54:51Z
- **Tasks:** 2
- **Files modified:** 8

## Accomplishments
- Two-pane bubbletea TUI: left list pane (type badge, countdown, history) + right detail pane (full request context)
- Secret requests show path, PID/UID, process name, working directory, process chain in tree format
- GPG signing requests show repo, commit message, author, changed files, key ID, process chain
- y/n keystrokes trigger async approve/deny with lock mode enforcement (LockModeNone/Manual/Auto)
- New requests appear without stealing cursor focus; resolved requests move to dimmed "Recent" history
- 11 unit tests covering all key model behaviors via direct model.Update() calls (no bubbletea program required)

## Task Commits

Each task was committed atomically:

1. **Task 1: TUI message types, styles, and pane components** - `31f6a29` (feat)
2. **Task 2: Root TUI model with bubbletea Elm architecture and unit tests** - `4b5efc9` (feat)

## Files Created/Modified
- `internal/tui/messages.go` - NewRequestMsg, RequestResolvedMsg, TickMsg, ApproveResultMsg
- `internal/tui/styles.go` - lipgloss style definitions for all TUI components
- `internal/tui/list_pane.go` - Request list with type badges, countdown timers, cursor nav, history section
- `internal/tui/detail_pane.go` - Rich context rendering for secret, GPG sign, search, and idle states
- `internal/tui/model.go` - Root Model: Init/Update/View, Config, NewModel, lock mode handling
- `internal/tui/model_test.go` - 11 unit tests exercising model state machine
- `go.mod` / `go.sum` - Added bubbletea v1.3.10, lipgloss v1.1.0, bubbles v1.0.0

## Decisions Made
- Custom listPane (not bubbles/list) chosen for split active+history display — bubbles/list item interface doesn't compose easily with the dual active/resolved sections
- Injected approveFn/denyFn callbacks make the Model testable without a real approval.Manager
- Async tea.Cmd for approve/deny prevents blocking the bubbletea event loop while the D-Bus call is in flight
- stripANSI helper in tests enables asserting on View() string output despite lipgloss ANSI styling

## Deviations from Plan

None - plan executed exactly as written. All 8+ required test cases implemented (11 total). Custom listPane was suggested in the plan spec ("NOT bubbles/list") and followed.

## Issues Encountered
- Pre-existing race condition in `internal/notification` TestDBusNotifier_CloseReconnectsOnClosedConn — confirmed pre-existing by reverting changes and re-running. Not related to this plan's changes.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- TUI is ready for Plan 03: D-Bus handler methods that call approval.Manager and inject NewRequestMsg/RequestResolvedMsg via p.Send()
- Plan 04 wires tea.NewProgram(model, WithInput(vtFile), WithOutput(vtFile)) in daemon.Run()
- The observer pattern (approval.Manager.Subscribe + p.Send) is the bridge between D-Bus goroutine and TUI event loop

---
*Phase: 05-core-flow*
*Completed: 2026-02-27*
