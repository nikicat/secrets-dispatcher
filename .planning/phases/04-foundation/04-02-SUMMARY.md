---
phase: 04-foundation
plan: 02
subsystem: infra
tags: [dbus, godbus, sd-notify, systemd, daemon, integration-tests, privsep]

# Dependency graph
requires:
  - phase: 04-01
    provides: "internal/companion package and provision subcommand (directory skeleton, D-Bus policy template)"
provides:
  - "internal/daemon package: Run() loop with D-Bus registration, signal handling, context cancellation"
  - "internal/daemon/Dispatcher: Ping() and GetVersion() D-Bus stubs"
  - "internal/daemon/SdNotify(): sd-notify READY=1 implementation (no new dependency)"
  - "secrets-dispatcher daemon subcommand wired into main.go"
  - "Integration test infrastructure: startDBusDaemonWithPolicy() with numeric UID policy"
affects: [05-daemon-skeleton, 06-pam-integration, 07-dbus-dispatcher, 08-vm-e2e]

# Tech tracking
tech-stack:
  added:
    - "github.com/godbus/dbus/v5/introspect subpackage (already in module, new import path)"
  patterns:
    - "BusAddress field in Config for test seam: empty = system bus, non-empty = private test bus"
    - "introspect.NewIntrospectable(node) with introspect.Methods(dispatcher) for D-Bus introspection"
    - "startDBusDaemonWithPolicy() with numeric UID (os.Getuid()) in policy user= attribute"
    - "dot-import (. package) in _test package for concise test access to daemon constants"

key-files:
  created:
    - internal/daemon/daemon.go
    - internal/daemon/dispatcher.go
    - internal/daemon/notify.go
    - internal/daemon/daemon_test.go
  modified:
    - main.go

key-decisions:
  - "BusAddress in Config (not global var) is the testing seam: empty = ConnectSystemBus(), non-empty = Connect(addr)"
  - "introspect subpackage (godbus/dbus/v5/introspect) instead of non-existent dbus.DefaultIntrospectHandler"
  - "dot-import in daemon_test.go package for Run, Config, BusName, ObjectPath, Interface — reduces test noise"
  - "Numeric UID string in policy config (os.Getuid()) matches dbus-daemon user= attribute behavior"

patterns-established:
  - "Config.BusAddress testing seam pattern: reuse in Phase 5+ whenever daemon needs to connect to a test bus"
  - "startDBusDaemonWithPolicy() template: copy into Phase 5+ integration tests that need policy-enforced D-Bus"
  - "introspect.Methods(v) + introspect.NewIntrospectable(node): standard introspect registration for all daemon objects"

requirements-completed:
  - DBUS-01
  - INFRA-01
  - INFRA-02
  - TEST-04

# Metrics
duration: 5min
completed: 2026-02-25
---

# Phase 4 Plan 02: Daemon Skeleton Summary

**godbus daemon skeleton registering net.mowaka.SecretsDispatcher1 on D-Bus with Ping/GetVersion stubs, sd-notify READY=1, and 5 integration tests using a private policy-enforced dbus-daemon (no root)**

## Performance

- **Duration:** 5 min
- **Started:** 2026-02-25T21:16:25Z
- **Completed:** 2026-02-25T21:21:30Z
- **Tasks:** 2
- **Files modified:** 5 (4 created, 1 modified)

## Accomplishments

- `internal/daemon` package with `Run()` loop: connects to D-Bus (system bus in prod, custom address in tests), exports `Dispatcher` with `Ping`/`GetVersion` stubs and `Introspectable`, requests `net.mowaka.SecretsDispatcher1`, sends `READY=1` via sd-notify, blocks on context
- `SdNotify()` — 15-line sd-notify implementation using Unix datagram socket, silent no-op when `NOTIFY_SOCKET` is unset; fire-and-forget with warn on dial failure
- 5 integration tests prove end-to-end D-Bus wire protocol with a private policy-enforced `dbus-daemon`; all run without root

## Task Commits

Each task was committed atomically:

1. **Task 1: Create daemon package with D-Bus skeleton and sd-notify** - `9ed0e7d` (feat)
2. **Task 2: Add integration tests with private dbus-daemon** - `6255d3a` (feat)

**Plan metadata:** see final docs commit below

## Files Created/Modified

- `internal/daemon/dispatcher.go` — `Dispatcher` struct with `Ping()` and `GetVersion()` stubs; `BusName`, `ObjectPath`, `Interface` constants
- `internal/daemon/notify.go` — `SdNotify()` writes to `NOTIFY_SOCKET` Unix datagram socket; silent no-op when unset
- `internal/daemon/daemon.go` — `Run(ctx, Config)`: connect, export dispatcher + introspectable, RequestName, SdNotify("READY=1"), block on ctx.Done()
- `internal/daemon/daemon_test.go` — `startDBusDaemonWithPolicy()` helper + 5 tests: RegistersAndServesStubs, NameAlreadyTaken, Introspectable, SdNotify_NoSocket, SdNotify_WithSocket
- `main.go` — added `daemon` import, `case "daemon"` in switch, `runDaemon()` with flag parsing and signal context, usage text

## Decisions Made

- `Config.BusAddress` string is the test seam: empty string connects to the real system bus in production; non-empty string (e.g., `unix:path=/tmp/test.sock`) connects to a private bus in tests. This avoids any global state or injectable function vars for the D-Bus connection.
- Used `github.com/godbus/dbus/v5/introspect` subpackage for introspection export. The plan referenced a non-existent `dbus.DefaultIntrospectHandler`. The correct API is `introspect.Methods(v)` + `introspect.NewIntrospectable(node)`. See deviation below.
- Used dot-import (`. "github.com/.../internal/daemon"`) in `daemon_test.go` to import `Run`, `Config`, `BusName`, `ObjectPath`, `Interface` without verbose `daemon.` prefix in every test assertion.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] Replaced non-existent dbus.DefaultIntrospectHandler with introspect subpackage**
- **Found during:** Task 1 (build verification)
- **Issue:** Plan specified `dbus.DefaultIntrospectHandler(dispatcher, ObjectPath)` but this function does not exist in godbus/dbus/v5. Build failed with `undefined: dbus.DefaultIntrospectHandler`.
- **Fix:** Used the correct API: `introspect.NewIntrospectable(&introspect.Node{Interfaces: [...]})` from `github.com/godbus/dbus/v5/introspect`. `introspect.Methods(dispatcher)` reflects the Dispatcher's exported methods automatically.
- **Files modified:** `internal/daemon/daemon.go`
- **Verification:** `go build ./internal/daemon/` passed; `TestDaemon_Introspectable` confirms introspection XML contains Ping and GetVersion
- **Committed in:** `9ed0e7d` (Task 1 commit)

---

**Total deviations:** 1 auto-fixed (Rule 3 - blocking build failure)
**Impact on plan:** The introspect subpackage produces identical runtime behavior to what the plan intended. No scope change.

## Issues Encountered

None beyond the blocking deviation above.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness

- `daemon.Run(ctx, Config{})` is the entry point for Phase 5; Phase 5 replaces stub methods with real Secret Service dispatch logic
- `startDBusDaemonWithPolicy()` test helper in `daemon_test.go` is the integration test template for Phase 5+ — copy the policy config template and helper pattern
- The D-Bus wire protocol is proven working: policy enforcement, name registration, method calls, introspection all verified
- Phase 5 can immediately extend `Dispatcher` with real methods without revisiting D-Bus plumbing

## Self-Check: PASSED

All created files confirmed on disk. All task commits confirmed in git history.

| Item | Status |
|------|--------|
| `internal/daemon/daemon.go` | FOUND |
| `internal/daemon/dispatcher.go` | FOUND |
| `internal/daemon/notify.go` | FOUND |
| `internal/daemon/daemon_test.go` | FOUND |
| `.planning/phases/04-foundation/04-02-SUMMARY.md` | FOUND |
| Commit `9ed0e7d` (Task 1) | FOUND |
| Commit `6255d3a` (Task 2) | FOUND |

---
*Phase: 04-foundation*
*Completed: 2026-02-25*
