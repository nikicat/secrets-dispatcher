---
gsd_state_version: 1.0
milestone: v1.0
milestone_name: Privilege Separation
status: unknown
last_updated: "2026-02-27T10:50:42.737Z"
progress:
  total_phases: 2
  completed_phases: 1
  total_plans: 6
  completed_plans: 3
---

# Project State

## Project Reference

See: .planning/PROJECT.md (updated 2026-02-25)

**Core value:** The user always knows exactly what they're approving before it happens, and no process in their desktop session can observe or tamper with the approval.
**Current focus:** v2.0 Privilege Separation — Phase 5: Core Flow

## Current Position

Phase: 5 of 8 (Core Flow)
Plan: 1 of 4 in current phase (completed)
Status: In progress
Last activity: 2026-02-27 — completed 05-01 (procchain package, VT ioctl helpers, companion tty group)

Progress: [██░░░░░░░░░░░░░░░░░░] 15% (v2.0 phases, 3 plans complete)

## Performance Metrics

**Velocity:**
- Total plans completed (v2.0): 3
- Average duration: 5.7 min
- Total execution time: 17 min

**By Phase:**

| Phase | Plans | Total | Avg/Plan |
|-------|-------|-------|----------|
| 04-foundation | 2 | 12 min | 6 min |
| 05-core-flow | 1 | 5 min | 5 min |

*Updated after each plan completion*

## Accumulated Context

### Decisions

Decisions are logged in PROJECT.md Key Decisions table.
Key decisions affecting v2.0 work:
- Companion user (not system user): needs real session for session D-Bus and systemd --user
- VT for trusted I/O: only kernel-enforced isolated I/O path on Linux; Wayland compositor is untrusted
- System D-Bus only: drop HTTP/WS/Web UI to minimize attack surface
- Standard Secret Service protocol between dispatcher and gopass-secret-service

Key decisions from 04-01 (companion provisioning):
- Regular user (no --system flag): systemd --user requires normal UID range; nologin shell prevents interactive login
- PROV-03 Phase 4 scope = directory skeleton only; gopass init + GPG key generation deferred to Phase 5
- geteuidFunc injectable for root-check testability without running tests as root
- check.go implements 10 checks (not 9 as plan stated) — home-exists and home-mode-0700 are separate for better diagnostics (extended to 11 in 05-01 with tty group check)

Key decisions from 04-02 (daemon skeleton):
- Config.BusAddress is the test seam: empty = ConnectSystemBus() (prod), non-empty = Connect(addr) (tests with private bus)
- godbus introspect subpackage (introspect.Methods + introspect.NewIntrospectable) — dbus.DefaultIntrospectHandler does not exist
- Numeric UID string (os.Getuid()) in dbus-daemon policy config user= attribute — avoids username lookup issues in test environments
- [Phase 05-core-flow]: userInGroup uses real user.LookupGroup + u.GroupIds() (not injectable); test coverage via fake UID with no OS groups
- [Phase 05-core-flow]: vtMode struct ABI verified via unsafe.Sizeof (8 bytes) + Offsetof tests to catch kernel ABI drift at compile time
- [Phase 05-core-flow]: Walk stops at PID <= 1 (not just == 1) to correctly handle PID 0 as sentinel

### Critical Pitfalls (from research)

- D-Bus policy: write and test policy file BEFORE writing daemon code (missing `<allow own>` → silent exit)
- VT_SETMODE crash: install defer + signal handler for VT_AUTO cleanup; test with kill -9
- PAM hook: fire-and-forget only (`systemctl start --no-block`); blocking hangs all logins
- Secret Service proxy: must maintain bidirectional path-mapping table, not thin-forward

### Pending Todos

None.

### Blockers/Concerns

- Phase 5: VT_SETMODE race with display manager (Ubuntu Bug #290197) — needs validation during Phase 5 planning
- Phase 6: PAM + systemd --user cross-user timing constraints — sparse docs, plan for iteration
- Phase 8: VM E2E harness selection (systemd-nspawn vs QEMU vs NixOS) — worth a research pass

## Session Continuity

Last session: 2026-02-27
Stopped at: Completed 05-01-PLAN.md (procchain package, VT ioctl helpers, companion tty group)
Resume file: None
