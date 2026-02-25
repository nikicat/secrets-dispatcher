# Project State

## Project Reference

See: .planning/PROJECT.md (updated 2026-02-25)

**Core value:** The user always knows exactly what they're approving before it happens, and no process in their desktop session can observe or tamper with the approval.
**Current focus:** v2.0 Privilege Separation — Phase 4: Foundation

## Current Position

Phase: 4 of 8 (Foundation)
Plan: 1 of ? in current phase
Status: In progress
Last activity: 2026-02-25 — completed 04-01 (companion provisioning subcommand)

Progress: [░░░░░░░░░░░░░░░░░░░░] 5% (v2.0 phases, 1 plan complete)

## Performance Metrics

**Velocity:**
- Total plans completed (v2.0): 1
- Average duration: 7 min
- Total execution time: 7 min

**By Phase:**

| Phase | Plans | Total | Avg/Plan |
|-------|-------|-------|----------|
| 04-foundation | 1 | 7 min | 7 min |

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
- check.go implements 10 checks (not 9 as plan stated) — home-exists and home-mode-0700 are separate for better diagnostics

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

Last session: 2026-02-25
Stopped at: Completed 04-01-PLAN.md (companion provisioning subcommand)
Resume file: None
