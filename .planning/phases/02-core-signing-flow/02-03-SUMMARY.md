---
phase: 02-core-signing-flow
plan: "03"
subsystem: api
tags: [go, gpg, signing, approval, exec]

# Dependency graph
requires:
  - phase: 02-core-signing-flow
    plan: "01"
    provides: "GetPending, ApproveWithSignature, ApproveGPGFailed on Manager; exported Signature/GPGStatus/GPGExitCode on Request"
  - phase: 02-core-signing-flow
    plan: "02"
    provides: "gpgsign.FindRealGPG pure function"
provides:
  - "GPGRunner interface (FindGPG, RunGPG) for testability of real gpg invocation"
  - "defaultGPGRunner implementing GPGRunner using os/exec and gpgsign.FindRealGPG"
  - "HandleApprove wired to call real gpg for gpg_sign requests"
  - "Signature/status/exitCode stored on Request via ApproveWithSignature or ApproveGPGFailed"
affects:
  - 02-core-signing-flow/02-04 (thin client reads ExitCode from WSMessage delivered after ApproveGPGFailed)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "GPGRunner interface injected into Handlers — real exec.Command in production, mock in tests"
    - "Separate stdout/stderr buffers in RunGPG (--status-fd=2 writes to stderr; mixing corrupts signature)"
    - "HandleApprove reads request type via GetPending before deciding approve path — non-gpg_sign flow unchanged"

key-files:
  created: []
  modified:
    - internal/api/gpgsign.go
    - internal/api/handlers.go

key-decisions:
  - "GPGRunner interface on Handlers rather than inline exec.Command — enables unit test mocking without a real gpg binary"
  - "Separate stdout/stderr buffers in RunGPG — critical correctness: --status-fd=2 sends status to stderr; mixing with stdout corrupts PGP signature"
  - "HTTP response is always approved regardless of gpg exit code — web UI action succeeded; ExitCode in WSMessage carries gpg failure to thin client"
  - "ApproveGPGFailed with exit code 2 when gpg binary not found — consistent failure path without a special case"
  - "defaultGPGRunner.FindGPG delegates to gpgsign.FindRealGPG — avoids duplicating PATH scan logic"

# Metrics
duration: 2min
completed: 2026-02-24
---

# Phase 2 Plan 03: Real GPG Invocation Summary

**GPGRunner interface and HandleApprove integration calling real gpg with exec.Command, capturing signature from stdout and status from stderr via separate buffers**

## Performance

- **Duration:** 2 min
- **Started:** 2026-02-24T13:06:10Z
- **Completed:** 2026-02-24T13:07:47Z
- **Tasks:** 2
- **Files modified:** 2

## Accomplishments
- Added `GPGRunner` interface (`FindGPG`, `RunGPG`) to `internal/api/gpgsign.go` for testability
- Implemented `defaultGPGRunner` struct: `FindGPG` delegates to `gpgsign.FindRealGPG`; `RunGPG` uses `exec.Command` with separate stdout/stderr buffers
- Added `gpgRunner GPGRunner` field to `Handlers` struct, initialized to `&defaultGPGRunner{}` in both `NewHandlers` and `NewHandlersWithProvider`
- Modified `HandleApprove` to detect `gpg_sign` requests via `GetPending`, call real gpg, and store results via `ApproveWithSignature` (success) or `ApproveGPGFailed` (failure)
- Non-gpg_sign approve flow is completely unchanged

## Task Commits

Each task was committed atomically:

1. **Task 1: Add GPGRunner interface and defaultGPGRunner with gpgRunner field on Handlers** - `df38dc8` (feat)
2. **Task 2: Wire real GPG invocation into HandleApprove for gpg_sign requests** - `2cf6b3e` (feat)

## Files Created/Modified
- `internal/api/gpgsign.go` - Added `GPGRunner` interface, `defaultGPGRunner` struct (FindGPG + RunGPG), kept existing `HandleGPGSignRequest`
- `internal/api/handlers.go` - Added `gpgRunner` field + `log/slog` import; replaced `HandleApprove` with GPG-aware implementation

## Decisions Made
- `GPGRunner` interface on `Handlers` rather than inline `exec.Command` — enables unit test mocking without a real gpg binary; `defaultGPGRunner` is the production implementation
- Separate `stdout`/`stderr` buffers in `RunGPG` — critical: `--status-fd=2` sends status lines to stderr; mixing with stdout corrupts the PGP armored signature
- HTTP response is always `"approved"` regardless of gpg exit code — the web UI approve action succeeded; `ExitCode != 0` in `WSMessage` carries the gpg failure signal to the thin client
- `ApproveGPGFailed` with exit code 2 when gpg binary not found — consistent failure path using same signaling mechanism, no special case needed
- `defaultGPGRunner.FindGPG` delegates to `gpgsign.FindRealGPG` — avoids duplicating the PATH scan + self-exclusion logic that Plan 02 already implemented

## Deviations from Plan

None — plan executed exactly as written.

## Issues Encountered
None.

## Next Phase Readiness
- `HandleApprove` now calls real gpg and stores `Signature`/`GPGStatus`/`GPGExitCode` on `Request`
- `OnEvent` in websocket.go (Plan 01) already reads `Request.Signature` and `Request.GPGExitCode` to populate `WSMessage`
- Plan 04 (thin client): can receive `WSMessage.ExitCode` and `WSMessage.Signature` from the WebSocket and act accordingly

---
*Phase: 02-core-signing-flow*
*Completed: 2026-02-24*

## Self-Check: PASSED

- internal/api/gpgsign.go: FOUND
- internal/api/handlers.go: FOUND
- .planning/phases/02-core-signing-flow/02-03-SUMMARY.md: FOUND
- Commit df38dc8: FOUND
- Commit 2cf6b3e: FOUND
