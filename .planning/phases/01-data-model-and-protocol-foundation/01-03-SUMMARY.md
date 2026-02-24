---
phase: 01-data-model-and-protocol-foundation
plan: "03"
subsystem: testing
tags: [go, testing, gpg-sign, approval, httptest]

requires:
  - phase: 01-01
    provides: GPGSignInfo struct, RequestTypeGPGSign constant, CreateGPGSignRequest method
  - phase: 01-02
    provides: HandleGPGSignRequest HTTP handler, GPGSignInfo on PendingRequest, Signature on WSMessage
provides:
  - "12 unit tests verifying gpg_sign manager and HTTP handler behavior"
  - "internal/approval/gpgsign_test.go: 7 manager-level tests"
  - "internal/api/gpgsign_test.go: 5 HTTP handler tests"
affects: [02-thin-client, 03-web-ui]

tech-stack:
  added: []
  patterns:
    - "findEvent helper for type-safe, order-independent event assertion against async observer"
    - "wsEventObserver stub replicates wsConnection.OnEvent logic without WebSocket — tests WSMessage.Signature construction in isolation"

key-files:
  created:
    - internal/approval/gpgsign_test.go
    - internal/api/gpgsign_test.go
  modified:
    - internal/approval/manager.go

key-decisions:
  - "Event assertions use findEvent() instead of index checks — notify() dispatches via 'go o.OnEvent()' so order is non-deterministic when Created and Approved/Denied fire close together"
  - "Approve() and Deny() now delete from pending map before closing done channel — CreateGPGSignRequest has no blocking goroutine defer to handle cleanup, leaving stale entries in pending"

patterns-established:
  - "findEvent(events, EventType): search events by type, not by index position — required for async observer patterns"

requirements-completed:
  - SIGN-06
  - SIGN-09
  - ERR-03

duration: 5min
completed: 2026-02-24
---

# Phase 1 Plan 03: GPGSign Unit Tests Summary

**12 unit tests verifying gpg_sign manager (CreateGPGSignRequest, expiry, approve, deny) and HTTP handler (POST validation, pending list KeyID visibility, WSMessage Signature on approval), with a bug fix for Approve/Deny not cleaning up CreateGPGSignRequest entries from pending**

## Performance

- **Duration:** 5 min
- **Started:** 2026-02-24T11:06:04Z
- **Completed:** 2026-02-24T11:11:00Z
- **Tasks:** 1 (combined TDD: tests + bug fix)
- **Files modified:** 3

## Accomplishments

- Created `internal/approval/gpgsign_test.go` with 7 tests: valid info returns non-empty ID (Case 1), nil info returns error (Case 2), expiry fires EventRequestExpired and cleans pending (Case 3/ERR-03), approve fires EventRequestApproved and removes from pending (Case 4), deny fires EventRequestDenied (Case 5), GPGSignInfo fields preserved on pending request, concurrent requests produce unique IDs
- Created `internal/api/gpgsign_test.go` with 5 tests: valid POST returns 200 + request_id (Case 6), missing gpg_sign_info returns 400 (Case 7), wrong method returns 405 (Case 8), key_id visible in pending list via HandlePendingList (Case 9/SIGN-09), wsEventObserver confirms WSMessage.Signature is non-empty on gpg_sign approval (Case 10)
- Fixed bug in Approve() and Deny(): CreateGPGSignRequest requests were not removed from the pending map after resolution

## Task Commits

1. **Task 1: gpgsign_test.go files + Approve/Deny pending cleanup fix** - `8240d2f` (test)

**Plan metadata:** (docs commit follows)

## Files Created/Modified

- `internal/approval/gpgsign_test.go` - 7 manager-level tests for CreateGPGSignRequest behavior
- `internal/api/gpgsign_test.go` - 5 HTTP handler tests for HandleGPGSignRequest
- `internal/approval/manager.go` - Approve() and Deny() now delete from pending before closing done channel

## Decisions Made

- Used `findEvent()` helper to search events by type rather than by index — the existing `notify()` dispatches via `go o.OnEvent()` making event ordering non-deterministic when events fire in quick succession
- `wsEventObserver` replicates the `wsConnection.OnEvent` Signature logic directly rather than wiring a real WebSocket — tests the construction logic in isolation without network infrastructure
- Auto-fixed the Approve/Deny cleanup bug (Rule 1) rather than weakening the test assertions — the pending map retaining resolved requests was a correctness issue affecting any code that inspects pending state

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Approve() and Deny() did not remove CreateGPGSignRequest entries from pending**
- **Found during:** Task 1 (writing TestCreateGPGSignRequest_Approve)
- **Issue:** `CreateGPGSignRequest` has a timeout goroutine that only cleans up pending on the `time.After` branch. When `Approve()` or `Deny()` closes `req.done`, the goroutine exits the `select` via `<-req.done` without deleting the request from `pending`. `RequireApproval` avoided this because its blocking goroutine has a `defer delete(m.pending, req.ID)`. Non-blocking `CreateGPGSignRequest` had no equivalent cleanup.
- **Fix:** Added `delete(m.pending, id)` in both `Approve()` and `Deny()` before `close(req.done)`. Safe for `RequireApproval` requests because the defer `delete` is a no-op on a missing key.
- **Files modified:** internal/approval/manager.go
- **Verification:** `go test ./internal/approval/... ./internal/api/... -timeout 30s` — all existing tests pass, no regressions
- **Committed in:** 8240d2f (combined with test files)

---

**Total deviations:** 1 auto-fixed (Rule 1 - Bug)
**Impact on plan:** Necessary correctness fix. The pending map would accumulate stale resolved entries indefinitely for all CreateGPGSignRequest calls. No scope creep.

## Issues Encountered

- Event ordering non-determinism: initial tests assumed `events[0]` = Created, `events[1]` = Approved/Denied. Since `notify()` uses `go o.OnEvent()`, both goroutines race. With `Deny()` called immediately after `CreateGPGSignRequest`, the Denied goroutine could be scheduled before the Created goroutine. Fixed by using `findEvent()` helper.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- All 12 tests pass: `go test ./internal/approval/... ./internal/api/... -run "GPGSign|GPGsign|gpgsign" -timeout 10s`
- `go build ./...` exits 0
- Phase 1 contracts verified; Phase 2 can build on this foundation with confidence
- The Approve/Deny cleanup fix prevents memory leaks in long-running server instances

---
*Phase: 01-data-model-and-protocol-foundation*
*Completed: 2026-02-24*
