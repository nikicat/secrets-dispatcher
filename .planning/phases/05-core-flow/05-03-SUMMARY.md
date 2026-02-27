---
phase: 05-core-flow
plan: 03
subsystem: daemon
tags: [dbus, bubbletea, approval, vt, gpg, activation]

# Dependency graph
requires:
  - phase: 05-core-flow/05-01
    provides: procchain.Walk(), tui.LockMode, tui.OpenVT/CleanupOnSignal
  - phase: 05-core-flow/05-02
    provides: tui.Model, NewRequestMsg, RequestResolvedMsg, tui.Config/NewModel
  - phase: 04-foundation
    provides: approval.Manager, Dispatcher skeleton, daemon.Config/Run, D-Bus infra

provides:
  - Dispatcher.RequestSecret(sender, path): blocks D-Bus caller until VT approval; returns "approved:<path>" or Denied/Timeout error
  - Dispatcher.RequestSign(sender, repo, commit, ...): blocks D-Bus caller until VT approval; invokes real gpg on approve
  - approval.Manager.CreateSecretRequest: non-blocking secret request creation with timeout goroutine
  - approval.Manager.WaitForResult: blocking wait on request done channel, returns (approved bool, err)
  - activation.go: ActivationFileContent(companionUser) for D-Bus service auto-start
  - daemon.Run() wires approval.Manager + tea.Program + tuiObserver + senderResolver + gpgSigner
  - Headless mode (VTPath/VTFile both nil) for integration tests
  - tuiObserver bridges EventRequestApproved/Denied/Expired/Cancelled to tui.RequestResolvedMsg

affects: [05-04, 06-activation, integration-tests]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - Non-blocking request creation (CreateSecretRequest/CreateGPGSignRequest) + WaitForResult for blocking D-Bus handlers
    - senderResolver/gpgSigner interfaces on Dispatcher for testability without real D-Bus/GPG
    - tuiObserver pattern: approval.Observer implementation that bridges Manager events to tea.Program.Send()
    - exportDispatcher helper: consolidates Export + IntrospectData + RequestName into one function
    - Headless mode guard (VTPath == "" && VTFile == nil) enables integration tests without VT hardware

key-files:
  created:
    - internal/daemon/handler.go
    - internal/daemon/activation.go
  modified:
    - internal/daemon/dispatcher.go
    - internal/daemon/daemon.go
    - internal/approval/manager.go

key-decisions:
  - "WaitForResult reads request under RLock then releases before blocking on <-req.done — avoids holding lock while blocked, same pattern as RequireApproval but without the timer.C branch (timeout goroutine already handles expiry)"
  - "CreateSecretRequest timeout goroutine guards with pending-map re-check under Lock before closing done channel — prevents double-close if Approve/Deny races the timeout"
  - "RequestSecret returns placeholder 'approved:<path>' in Phase 5; real gopass fetch deferred to Phase 6"
  - "Headless mode (VTPath+VTFile both nil) preserves all existing Phase 4 integration tests without modification"
  - "tuiObserver skips EventRequestCreated: D-Bus handler already sends NewRequestMsg directly via p.Send(); observer would duplicate it"
  - "defaultGPGSigner placed in handler.go alongside Dispatcher methods (cohesion), not a separate file"

patterns-established:
  - "Pattern: non-blocking create + WaitForResult split for D-Bus handlers — lets TUI receive the request before the handler blocks"
  - "Pattern: resolveOrEmpty fallback when resolver is nil — safe for headless/test mode without special-casing throughout"

requirements-completed: [DBUS-03, DBUS-04, DBUS-06, DBUS-07, GPG-03]

# Metrics
duration: 4min
completed: 2026-02-27
---

# Phase 5 Plan 03: D-Bus Wiring Summary

**RequestSecret/RequestSign D-Bus methods that block until VT approval, tuiObserver bridging Manager events to bubbletea, and daemon.Run() wiring approval.Manager + tea.Program with headless test mode**

## Performance

- **Duration:** 4 min
- **Started:** 2026-02-27T10:58:53Z
- **Completed:** 2026-02-27T11:02:34Z
- **Tasks:** 2
- **Files modified:** 5

## Accomplishments

- `RequestSecret` and `RequestSign` D-Bus methods: block the calling goroutine until the companion user approves/denies on VT; return typed D-Bus errors (`net.mowaka.Error.Denied`, `.Timeout`, `.NotReady`, `.Internal`, `.GPGFailed`)
- `approval.Manager.CreateSecretRequest` + `WaitForResult`: non-blocking creation with timeout goroutine + blocking wait on done channel — the "create then send to TUI then wait" pattern
- `daemon.Run()` fully wired: creates Manager, opens VT, builds tea.Program, subscribes tuiObserver, creates Dispatcher with SetProgram, headless fallback for tests
- `tuiObserver` implements `approval.Observer` and forwards approved/denied/expired/cancelled events as `tui.RequestResolvedMsg` via `p.Send()`
- All existing Phase 4 integration tests (Ping, GetVersion, Introspectable, NameAlreadyTaken, SdNotify) continue to pass unmodified

## Task Commits

1. **Task 1: D-Bus handler methods, activation file, manager helpers** - `b3cf63e` (feat)
2. **Task 2: Rewrite daemon.Run() with TUI startup and headless mode** - `a1a3503` (feat)

**Plan metadata:** (docs commit follows)

## Files Created/Modified

- `internal/daemon/dispatcher.go` - Expanded Dispatcher struct with mgr/program/signer/resolver; senderResolver and gpgSigner interfaces; SetProgram/SetSigner methods
- `internal/daemon/handler.go` - RequestSecret, RequestSign, resolveOrEmpty, makeDBusError, defaultGPGSigner
- `internal/daemon/activation.go` - ActivationFileContent(companionUser), ActivationFilePath constant
- `internal/daemon/daemon.go` - Rewritten Run(): expanded Config, TUI startup, tuiObserver, exportDispatcher helper, headless mode
- `internal/approval/manager.go` - Added CreateSecretRequest (non-blocking with timeout goroutine) and WaitForResult (blocking on done channel)

## Decisions Made

- `WaitForResult` acquires an RLock to get the request pointer, releases it, then blocks on `<-req.done`. This avoids holding a lock while sleeping indefinitely. If the request is already gone from pending (expired before the handler called WaitForResult), it returns ErrNotFound which the handler maps to a Timeout error.
- `CreateSecretRequest`'s timeout goroutine re-checks the pending map under a write lock before closing `req.done`, preventing a double-close panic if `Approve`/`Deny` races with the timer.
- The `tuiObserver.OnEvent` handler skips `EventRequestCreated` because the D-Bus handler sends `NewRequestMsg` directly to the TUI immediately after calling `CreateSecretRequest` — if the observer also sent it, the TUI would see duplicate entries.
- Headless mode is determined by `cfg.VTPath == "" && cfg.VTFile == nil` at the start of `Run()`. When headless, the dispatcher is exported and D-Bus calls work, but no TUI renders — requests timeout. This is the correct behavior for integration tests which do not call RequestSecret/RequestSign.
- `RequestSecret` returns `"approved:" + path` as a Phase 5 placeholder. The real gopass fetch belongs in Phase 6 and is marked with a TODO comment.

## Deviations from Plan

None - plan executed exactly as written. The `PRAGMATIC APPROACH` / `SIMPLEST APPROACH` analysis in the plan guided implementation correctly: `CreateSecretRequest` + `WaitForResult` on Manager, with the timeout goroutine guarded against double-close.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- D-Bus wiring complete: a caller can invoke `RequestSecret` or `RequestSign` on the system bus, the companion daemon will show it in the VT TUI, and block until the user presses y/n
- Phase 6 should replace the `"approved:" + path` placeholder in `RequestSecret` with a real gopass fetch
- `ActivationFileContent(companionUser)` and `ActivationFilePath` are ready for the provisioning phase to write the activation file during companion setup
- All Phase 4 integration tests pass; new handler methods need integration tests in a future plan that tests the full approve/deny flow end-to-end

---
*Phase: 05-core-flow*
*Completed: 2026-02-27*
