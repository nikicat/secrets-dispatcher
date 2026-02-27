---
phase: 05-core-flow
plan: 04
subsystem: testing
tags: [dbus, integration-test, approval, gpg, pinentry, bubbletea, godbus]

# Dependency graph
requires:
  - phase: 05-core-flow/05-03
    provides: RequestSecret/RequestSign handler methods, WaitForResult, daemon.Run() wiring, headless mode
  - phase: 05-core-flow/05-01
    provides: companion provisioning with tty group, directory skeleton

provides:
  - handler_test.go: unit tests for RequestSecret/RequestSign via direct Dispatcher method calls with mock messageSender
  - daemon_test.go: integration tests for RequestSecret/RequestSign over real D-Bus wire protocol
  - MessageSender interface: exported subset of *tea.Program, enables test injection without real bubbletea program
  - ApprovalManager/MessageSender test seams in daemon.Config for integration tests
  - GPG-02: gpg-agent.conf with pinentry-tty + keep-tty written during Provision()
  - GPG-02: systemd unit template has GPG_TTY={{.VTPath}} and GNUPGHOME environment
  - Bug fix: WaitForResult returns ErrTimeout (not nil/denied) when timeout goroutine fires

affects: [06-gopass-integration, vm-e2e, companion-provisioning]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - Export minimal interface (MessageSender) to allow external test packages to mock internal *tea.Program seam
    - ApprovalManager + MessageSender test seams in Config struct enable integration tests without real VT or external observer
    - req.expired bool field on Request: set by timeout goroutine before close(done), read by WaitForResult to distinguish timeout from denial

key-files:
  created:
    - internal/daemon/handler_test.go
  modified:
    - internal/daemon/daemon_test.go
    - internal/daemon/dispatcher.go
    - internal/daemon/daemon.go
    - internal/approval/manager.go
    - internal/approval/gpgsign.go
    - internal/companion/templates.go
    - internal/companion/provision.go
    - internal/companion/provision_test.go

key-decisions:
  - "messageSender interface exported as MessageSender: daemon_test (external package) needs to provide mock implementations; unexported type would be inaccessible"
  - "req.expired bool on Request struct: WaitForResult can't distinguish timeout from denial unless timeout goroutine marks the request; simplest fix without changing public API"
  - "ApprovalManager seam in Config: integration tests must approve/deny in-process; creating a new manager inside Run() prevented that"
  - "keep-tty in gpg-agent.conf: forces gpg-agent to always use configured TTY regardless of which process triggered the signing request"
  - "vtPath passed as '/dev/tty8' hardcoded in Provision(): VT path is a sysconfig concern; future improvement to make it configurable"

patterns-established:
  - "Pattern: messageSender interface for bubbletea decoupling — define minimal Send(tea.Msg) interface, export it, let tests inject noopSender/mockSender"
  - "Pattern: ApprovalManager test seam — inject pre-built manager via Config for integration tests needing approve/deny control"
  - "Pattern: req.expired bool for distinguishing timeout from explicit denial in WaitForResult"

requirements-completed: [TEST-02, GPG-02]

# Metrics
duration: 10min
completed: 2026-02-27
---

# Phase 5 Plan 04: Integration Tests and GPG-02 Pinentry Configuration Summary

**D-Bus integration tests for RequestSecret/RequestSign over real godbus wire protocol, messageSender interface for test injection, and GPG-02 pinentry-tty configuration in systemd unit and gpg-agent.conf**

## Performance

- **Duration:** 10 min
- **Started:** 2026-02-27T13:05:00Z
- **Completed:** 2026-02-27T13:15:00Z
- **Tasks:** 2
- **Files modified:** 9

## Accomplishments

- 6 handler unit tests in `handler_test.go`: TestRequestSecret_Approved/Denied/Timeout/NilProgram and TestRequestSign_Approved/Denied — direct Dispatcher method calls using mock messageSender, no bubbletea program required
- 5 D-Bus integration tests in `daemon_test.go`: TestDaemon_RequestSecret_Approve/Deny/Timeout, TestDaemon_ConcurrentRequests, TestDaemon_RequestSign_Approve — verify the full D-Bus wire protocol path from client.Call() through the daemon to manager approval/denial and back
- `MessageSender` interface exported from dispatcher.go so external test packages can mock the TUI program seam
- `ApprovalManager` and `MessageSender` test seams added to `daemon.Config` enabling integration tests to inject a shared manager and bypass the NotReady guard
- GPG-02 complete: `systemdUnitTemplate` gains `GPG_TTY={{.VTPath}}` + `GNUPGHOME={{.CompanionHome}}/.gnupg`; `Provision()` writes `.gnupg/gpg-agent.conf` with `pinentry-program /usr/bin/pinentry-tty` and `keep-tty`

## Task Commits

1. **Task 1: Handler unit tests and messageSender interface** - `94c9777` (feat)
2. **Task 2: D-Bus integration tests and GPG-02 pinentry-tty config** - `74f6a27` (feat)

**Plan metadata:** (docs commit follows)

## Files Created/Modified

- `internal/daemon/handler_test.go` - 6 unit tests: TestRequestSecret_Approved/Denied/Timeout/NilProgram, TestRequestSign_Approved/Denied; mockSender, mockResolver, mockSigner, pollForPending helper
- `internal/daemon/daemon_test.go` - 5 integration tests + noopSender, testSignerForDaemon, startDaemonForTest helper, pollPendingFromManager helper
- `internal/daemon/dispatcher.go` - MessageSender interface exported; Dispatcher.program changed from *tea.Program to messageSender (= MessageSender)
- `internal/daemon/daemon.go` - ApprovalManager and MessageSender fields added to Config; Run() uses injected manager and wires MessageSender in headless path
- `internal/approval/manager.go` - req.expired bool added to Request; WaitForResult returns ErrTimeout when req.expired; CreateSecretRequest timeout goroutine sets expired=true before closing done channel
- `internal/approval/gpgsign.go` - CreateGPGSignRequest timeout goroutine: added re-check guard (same pattern as CreateSecretRequest) and sets req.expired=true before close
- `internal/companion/templates.go` - systemdUnitTemplate gains GPG_TTY={{.VTPath}} and GNUPGHOME={{.CompanionHome}}/.gnupg
- `internal/companion/provision.go` - writeGPGAgentConf() added; called from Provision() step 4b; writeSystemdUnit() gains vtPath parameter
- `internal/companion/provision_test.go` - TestProvision_WritesGPGAgentConf and TestProvision_SystemdUnitHasGPGTTY added

## Decisions Made

- Exported `MessageSender` interface (not left unexported as `messageSender`): `daemon_test` is an external package that needs to provide mock implementations; unexported interface types are inaccessible from external packages.
- Added `req.expired bool` to `Request` struct: `WaitForResult` has no way to distinguish between a user denying (Deny called, req.result=false) and the timeout goroutine firing (done closed, req.result=false). Setting `expired=true` before closing the done channel gives WaitForResult the information it needs to return `ErrTimeout`.
- The same `expired` fix was applied to `CreateGPGSignRequest` timeout goroutine — it had the same latent bug (no double-close guard and no expiry marker).
- `vtPath` hardcoded as `"/dev/tty8"` in `Provision()` call: VT path is a sysconfig concern that could be made configurable later; the constant is correct for the default installation.
- `keep-tty` in gpg-agent.conf: without this, gpg-agent would reassign its TTY to the controlling terminal of whichever process triggered signing, potentially routing pinentry to an untrusted terminal instead of the companion VT.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] WaitForResult returned ErrDenied (not ErrTimeout) on timeout**
- **Found during:** Task 1 (TestRequestSecret_Timeout failing)
- **Issue:** When the timeout goroutine fired and closed `req.done`, `WaitForResult` read `req.result == false` and returned `(false, nil)` — indistinguishable from a user denial. The handler then returned `net.mowaka.Error.Denied` instead of `net.mowaka.Error.Timeout`.
- **Fix:** Added `expired bool` field to `Request`. Timeout goroutines in `CreateSecretRequest` and `CreateGPGSignRequest` set `req.expired = true` before `close(req.done)`. `WaitForResult` returns `ErrTimeout` when `req.expired` is true.
- **Files modified:** `internal/approval/manager.go`, `internal/approval/gpgsign.go`
- **Verification:** TestRequestSecret_Timeout passes; all approval manager tests pass.
- **Committed in:** `94c9777` (Task 1 commit)

---

**Total deviations:** 1 auto-fixed (Rule 1 - bug in WaitForResult timeout/denial disambiguation)
**Impact on plan:** Essential correctness fix. Callers must be able to distinguish timeout from denial; the original WaitForResult contract promised ErrTimeout but didn't deliver it.

## Issues Encountered

None beyond the auto-fixed WaitForResult bug above.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- All D-Bus handler methods (RequestSecret, RequestSign, Ping, GetVersion) are tested at both unit and integration level
- Integration test infrastructure (`startDaemonForTest`, `ApprovalManager` seam) can be reused in Phase 6 when testing gopass secret fetch
- GPG-02 complete: companion provisioning now writes gpg-agent.conf with pinentry-tty; systemd unit has GPG_TTY pointing to the VT
- Phase 5 is now complete — all 4 plans done
- Phase 6 can replace the `"approved:" + path` placeholder in RequestSecret with a real gopass fetch using the companion's GPG store

---
*Phase: 05-core-flow*
*Completed: 2026-02-27*
