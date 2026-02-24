---
phase: 02-core-signing-flow
plan: "01"
subsystem: api
tags: [go, approval, websocket, unix-socket, bearer-auth, gpg]

# Dependency graph
requires:
  - phase: 01-data-model-and-protocol-foundation
    provides: "approval.Manager, Request struct, GPGSignInfo, WSMessage, Server infrastructure"
provides:
  - "CommitObject string field on GPGSignInfo for raw git commit data passed to real gpg stdin"
  - "Exported Signature/GPGStatus/GPGExitCode fields on Request for real gpg output storage"
  - "Manager.GetPending(id) for pre-approval request lookup by ID"
  - "Manager.ApproveWithSignature(id, sig, status) for wiring real gpg output on approval"
  - "Manager.ApproveGPGFailed(id, status, exitCode) for gpg failure propagation"
  - "Auth.ValidateRequest(r) combining cookie and Bearer token validation"
  - "HandleWS accepts Bearer tokens (thin client auth fix)"
  - "WSMessage.GPGStatus and WSMessage.ExitCode for thin client consumption"
  - "Server Unix socket listener alongside existing TCP listener"
affects:
  - 02-core-signing-flow/02-03 (real gpg invocation uses GetPending, ApproveWithSignature, ApproveGPGFailed)
  - 02-core-signing-flow/02-04 (thin client uses CommitObject, Bearer WebSocket auth, Unix socket, WSMessage fields)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Combined auth validator (ValidateRequest) checks cookie then Bearer — reusable for any handler needing both browser and CLI auth"
    - "Unix socket served by same http.Server as TCP via second net.Listener — zero code duplication in handlers"
    - "Exported output fields on Request (Signature, GPGStatus, GPGExitCode) with json:\"-\" to keep them out of JSON serialization"

key-files:
  created: []
  modified:
    - internal/approval/gpgsign.go
    - internal/approval/manager.go
    - internal/api/auth.go
    - internal/api/websocket.go
    - internal/api/server.go

key-decisions:
  - "signature field promoted from unexported to exported (Signature) — consistent with other exported fields on Request"
  - "GPGStatus/GPGExitCode use json:\"-\" to exclude from JSON pending list (irrelevant before approval)"
  - "UnixSocketPath is exported on Server struct for main.go to read in Plan 04 (not wired yet)"
  - "os.Remove before net.Listen(unix) as standard stale-socket cleanup (Pitfall 5 from RESEARCH.md)"
  - "ApproveGPGFailed sets result=true and closes done channel — gpg failure is still 'resolved', ExitCode carries the failure info to thin client"

patterns-established:
  - "ValidateRequest pattern: session cookie first, Bearer header fallback — use in any new handlers needing thin client access"

requirements-completed:
  - SIGN-05
  - SIGN-07
  - SIGN-08

# Metrics
duration: 4min
completed: 2026-02-24
---

# Phase 2 Plan 01: Daemon Infrastructure Summary

**CommitObject field, exported gpg output fields, Bearer WebSocket auth, Unix socket listener, and WSMessage extensions enabling real GPG signing pipeline**

## Performance

- **Duration:** 4 min
- **Started:** 2026-02-24T12:58:25Z
- **Completed:** 2026-02-24T13:02:25Z
- **Tasks:** 2
- **Files modified:** 5

## Accomplishments
- Extended `GPGSignInfo` with `CommitObject string` so thin client can pass raw commit bytes; daemon feeds them to real gpg stdin on approval
- Promoted `signature` to exported `Signature []byte` and added `GPGStatus []byte` / `GPGExitCode int` on `Request`; added `GetPending`, `ApproveWithSignature`, `ApproveGPGFailed` on `Manager`
- Fixed `HandleWS` to accept Bearer tokens via new `Auth.ValidateRequest` (was cookie-only, blocking thin client WebSocket connections)
- Added optional Unix socket listener to `Server` — same HTTP mux served over both TCP (browser) and Unix socket (thin client)
- Extended `WSMessage` with `GPGStatus` and `ExitCode` fields; `OnEvent` now reads real `Request.Signature` instead of placeholder

## Task Commits

Each task was committed atomically:

1. **Task 1: Extend approval model with CommitObject, GetPending, and signature fields** - `6ac44db` (feat)
2. **Task 2: Fix WebSocket auth, add Unix socket listener, extend WSMessage** - `78048a6` (feat)

## Files Created/Modified
- `internal/approval/gpgsign.go` - Added `CommitObject string` field to `GPGSignInfo`
- `internal/approval/manager.go` - Promoted `signature` to `Signature`, added `GPGStatus`/`GPGExitCode`, added `GetPending`/`ApproveWithSignature`/`ApproveGPGFailed` methods
- `internal/api/auth.go` - Added `ValidateRequest(r *http.Request) bool` combining cookie and Bearer auth
- `internal/api/websocket.go` - `HandleWS` uses `ValidateRequest`; `WSMessage` gains `GPGStatus`/`ExitCode`; `OnEvent` uses real `Request.Signature`
- `internal/api/server.go` - `Server` struct gains `unixListener`/`UnixSocketPath`; `newServerWithHandlers` accepts `unixSocketPath`; `Start`/`Shutdown` manage Unix listener lifecycle

## Decisions Made
- `signature` field promoted to exported `Signature` — consistent naming with other exported Request fields; `json:"-"` keeps it out of the pending list JSON response (irrelevant before approval resolves)
- `GPGStatus`/`GPGExitCode` also use `json:"-"` — only meaningful after signing, carried to thin client via WSMessage fields, not JSON REST responses
- `UnixSocketPath` exported on Server so `main.go` (Plan 04) can read the actual path for logging/cleanup
- `ApproveGPGFailed` signals `result=true` and closes `done` channel — treating gpg failure as a resolved request so the thin client unblocks; `ExitCode != 0` in WSMessage signals the failure

## Deviations from Plan

None — plan executed exactly as written.

## Issues Encountered
None.

## Next Phase Readiness
- `Manager.GetPending`, `ApproveWithSignature`, `ApproveGPGFailed` ready for Plan 03 (real gpg invocation in `HandleApprove`)
- Unix socket listener ready for Plan 04 (`main.go` passes socket path via `newServerWithHandlers`)
- `ValidateRequest` and Bearer WebSocket auth ready for Plan 04 (thin client WebSocket dial with Bearer token)
- `WSMessage.GPGStatus` and `WSMessage.ExitCode` ready for Plan 04 (thin client reads them and exits accordingly)

---
*Phase: 02-core-signing-flow*
*Completed: 2026-02-24*

## Self-Check: PASSED

- internal/approval/gpgsign.go: FOUND
- internal/approval/manager.go: FOUND
- internal/api/auth.go: FOUND
- internal/api/websocket.go: FOUND
- internal/api/server.go: FOUND
- .planning/phases/02-core-signing-flow/02-01-SUMMARY.md: FOUND
- Commit 6ac44db: FOUND
- Commit 78048a6: FOUND
