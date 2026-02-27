---
phase: 05-core-flow
plan: 01
subsystem: tui
tags: [procchain, vt-ioctl, linux, syscall, companion, provisioning]

# Dependency graph
requires:
  - phase: 04-foundation
    provides: companion provisioning (Provision/Check), injectable sysfuncs pattern, D-Bus daemon skeleton
provides:
  - procchain.Walk() for /proc PPid traversal returning PID, PPid, Name, CWD per entry
  - VT ioctl helpers: OpenVT, LockVT, UnlockVT, AckRelease, CleanupOnSignal with crash recovery
  - LockMode enum (None/Manual/Auto) for VT_PROCESS mode control
  - Companion user tty group membership in provisioning and check (11 checks total)
affects: [05-02, 05-03, tui, daemon, gpgsign]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - /proc/{pid}/status PPid traversal with cycle detection via seen map
    - VT ioctl via syscall.Syscall(SYS_IOCTL) with vtMode struct matching kernel ABI
    - SIGUSR1 goroutine for VT_RELDISP acknowledgment (prevents VT switch stall)
    - Signal handlers registered before LockVT to avoid race window

key-files:
  created:
    - internal/procchain/procchain.go
    - internal/procchain/procchain_test.go
    - internal/tui/vt.go
    - internal/tui/vt_test.go
  modified:
    - internal/companion/sysfuncs.go
    - internal/companion/provision.go
    - internal/companion/check.go
    - internal/companion/check_test.go
    - internal/companion/provision_test.go

key-decisions:
  - "userInGroup uses real user.LookupGroup + u.GroupIds() (not injectable) — test coverage via fake UID that has no OS groups"
  - "CleanupOnSignal uses close(ch) to terminate goroutines on cleanup, not just signal.Stop"
  - "vtMode struct uses uint8/int16 primitives (not C types) — verified 8-byte ABI via unsafe.Sizeof test"
  - "Walk stops at PID <= 1 (not just PID 1) to handle PID 0 gracefully"

patterns-established:
  - "Pattern: vtMode struct layout tested via unsafe.Sizeof + Offsetof to catch ABI drift at compile time"
  - "Pattern: VT device tests gated on SD_TEST_VT env var — ioctl tests separated from unit tests"
  - "Pattern: usermodFunc injectable alongside userAddFunc for provisioning operations"

requirements-completed: [VT-01, VT-02, VT-05, VT-09, TEST-01]

# Metrics
duration: 5min
completed: 2026-02-27
---

# Phase 5 Plan 01: Foundation Utilities Summary

**/proc PPid traversal package, VT_SETMODE ioctl helpers with crash recovery signals, and companion user tty group membership via injectable usermod**

## Performance

- **Duration:** 5 min
- **Started:** 2026-02-27T10:44:14Z
- **Completed:** 2026-02-27T10:49:09Z
- **Tasks:** 2
- **Files modified:** 9

## Accomplishments

- `internal/procchain` package: `Walk(startPID, maxDepth) []ProcInfo` traverses `/proc/{pid}/status` PPid chain with cycle detection; `ProcInfo` carries PID, PPid, Name (from comm), CWD (best-effort readlink)
- `internal/tui/vt.go`: `OpenVT`, `LockVT`, `UnlockVT`, `AckRelease`, `CleanupOnSignal` using `syscall.Syscall(SYS_IOCTL)` with constants verified from `/usr/include/linux/vt.h`; `LockMode` enum for three VT locking strategies
- Companion provisioning extended with `ensureTTYGroup` step (step 2b) and `Check()` now reports 11 checks (was 10) including "companion user in tty group"

## Task Commits

1. **Task 1: Create procchain package and VT ioctl helpers** - `36c6d44` (feat)
2. **Task 2: Patch provisioning to add companion user to tty group** - `631e600` (feat)

**Plan metadata:** (docs commit follows)

## Files Created/Modified

- `internal/procchain/procchain.go` - Walk() + readProc() + parsePPid(); /proc traversal with seen map
- `internal/procchain/procchain_test.go` - self-process, max-depth, invalid PID, cycle detection, parsePPid unit tests
- `internal/tui/vt.go` - OpenVT, LockVT, UnlockVT, AckRelease, CleanupOnSignal; LockMode constants; vtMode ABI struct
- `internal/tui/vt_test.go` - struct size/offset tests, constant value assertions, non-existent path error, SD_TEST_VT-gated real VT tests
- `internal/companion/sysfuncs.go` - added `usermodFunc` (defaultUsermod runs `usermod <args> <username>`)
- `internal/companion/provision.go` - added `ensureTTYGroup()` and step 2b call in `Provision()`
- `internal/companion/check.go` - added check #10 "companion user in tty group" using `userInGroup()` helper; updated doc comment 10->11
- `internal/companion/check_test.go` - updated expectedChecks 10->11; added TTYGroupFail and TTYGroupMissingWhenUserMissing tests
- `internal/companion/provision_test.go` - added usermodFunc to saveOrigFuncs and noopFuncs; added TestProvision_AddsTTYGroup; stubbed usermodFunc in 3 existing tests

## Decisions Made

- `userInGroup` uses real `user.LookupGroup` + `u.GroupIds()` rather than injectable functions. Tests cover the failure path via fake UIDs that have no OS group memberships. The tty group pass case is effectively tested in integration/production.
- `CleanupOnSignal` closes the channel to terminate SIGUSR1 goroutine on cleanup (avoids goroutine leak on tests), not just `signal.Stop`.
- `vtMode` struct field types are Go primitives (`uint8`, `int16`) that match the C struct layout exactly; verified at test time via `unsafe.Sizeof` (8 bytes) and `unsafe.Offsetof` for each field.
- `Walk` stops when `current <= 1` (not just `== 1`) so PID 0 is also correctly handled as a sentinel.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Stubbed usermodFunc in existing provision tests that bypass noopFuncs**
- **Found during:** Task 2 (test run after adding usermodFunc)
- **Issue:** Tests `TestProvision_WritesDBusPolicy`, `TestProvision_WritesSystemdUnit`, `TestProvision_WritesPAMConfig` manually stub individual functions without calling `noopFuncs()`. Adding `usermodFunc` without stubbing it in these tests caused real `usermod` to be invoked, failing with exit 6 (user does not exist).
- **Fix:** Added `usermodFunc = func(username string, args ...string) error { return nil }` to each affected test's setup block.
- **Files modified:** `internal/companion/provision_test.go`
- **Verification:** All companion tests pass with -race.
- **Committed in:** `631e600` (Task 2 commit)

---

**Total deviations:** 1 auto-fixed (Rule 1 - bug in test isolation)
**Impact on plan:** Necessary fix to maintain test isolation; no scope creep.

## Issues Encountered

None beyond the auto-fixed test isolation issue above.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- `procchain.Walk` is ready for use in Plan 02 (TUI) detail pane and Plan 03 (D-Bus handler) to build process chain context for approval requests
- VT ioctl helpers (`OpenVT`, `LockVT`, etc.) are ready for Plan 02 TUI integration
- `LockMode` enum is ready for Plan 02 TUI model state
- Companion provisioning now ensures tty group membership; Plan 02 can rely on companion user having /dev/ttyN write access

Blockers carried forward (pre-existing):
- VT_SETMODE race with display manager (Ubuntu Bug #290197) — still open, mitigated by defaulting to LockModeNone

---
*Phase: 05-core-flow*
*Completed: 2026-02-27*
