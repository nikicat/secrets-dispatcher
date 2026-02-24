---
phase: 01-data-model-and-protocol-foundation
plan: "01"
subsystem: api
tags: [go, approval, gpg, signing]

requires: []
provides:
  - "RequestTypeGPGSign constant in package approval"
  - "GPGSignInfo struct with 8 commit-context fields"
  - "CreateGPGSignRequest non-blocking method on *Manager"
  - "Request.GPGSignInfo and Request.signature fields"
affects:
  - 01-02
  - 01-03
  - 01-04

tech-stack:
  added: []
  patterns:
    - "Non-blocking request creation via fire-and-forget timeout goroutine (mirrors RequireApproval timer.C branch)"
    - "Unexported signature []byte field on Request for future gpg output wiring in Phase 2"

key-files:
  created:
    - internal/approval/gpgsign.go
  modified:
    - internal/approval/manager.go

key-decisions:
  - "CreateGPGSignRequest does not use HTTP request context for timeout goroutine — fire-and-forget so a dropped connection does not cancel an active UI review"
  - "signature field is unexported; Phase 1 leaves it unset (placeholder); Phase 2 will wire real gpg output"
  - "GPGSignInfo is a pointer on Request so it is nil for non-gpg_sign requests with zero serialization overhead"

patterns-established:
  - "Non-blocking approval request: register in pending map, notify observers, launch timeout goroutine, return ID immediately"

requirements-completed:
  - SIGN-06
  - SIGN-09
  - ERR-03

duration: 8min
completed: 2026-02-24
---

# Phase 1 Plan 01: GPGSign Data Model Summary

**GPGSignInfo struct, RequestTypeGPGSign constant, and non-blocking CreateGPGSignRequest method on Manager — foundation for all Phase 1 gpg_sign work**

## Performance

- **Duration:** 8 min
- **Started:** 2026-02-24T00:00:00Z
- **Completed:** 2026-02-24T00:08:00Z
- **Tasks:** 2
- **Files modified:** 2

## Accomplishments

- Created `internal/approval/gpgsign.go` with GPGSignInfo struct (8 fields), RequestTypeGPGSign constant, and CreateGPGSignRequest method
- Extended `Request` struct in manager.go with `GPGSignInfo *GPGSignInfo` and unexported `signature []byte` fields
- Non-blocking request creation mirrors existing RequireApproval pattern but returns ID immediately instead of blocking

## Task Commits

1. **Task 1+2: GPGSignInfo struct, constant, signature field, and CreateGPGSignRequest** - `daa6e98` (feat)

**Plan metadata:** (docs commit follows)

## Files Created/Modified

- `internal/approval/gpgsign.go` - New file: RequestTypeGPGSign constant, GPGSignInfo struct, CreateGPGSignRequest method on *Manager
- `internal/approval/manager.go` - Added GPGSignInfo *GPGSignInfo and signature []byte fields to Request struct

## Decisions Made

- Fire-and-forget timeout goroutine (not HTTP request context) so a dropped connection cannot cancel a request being reviewed in the web UI
- GPGSignInfo pointer on Request (nil for non-gpg_sign requests); no serialization overhead for existing request types
- signature is unexported; Phase 2 will write real gpg output into it; Phase 1 leaves it unset

## Deviations from Plan

None - plan executed exactly as written. Tasks 1 and 2 were implemented together in a single file write since CreateGPGSignRequest was specified for the same file as the struct. Combined into one commit (both tasks completed before first commit opportunity).

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- Foundation types are in place; all dependent Phase 1 plans (01-02 API handler, 01-03 WebSocket events, 01-04 serialization) can proceed
- go build ./... and go vet ./... both exit 0
- No new external dependencies added to go.mod

---
*Phase: 01-data-model-and-protocol-foundation*
*Completed: 2026-02-24*
