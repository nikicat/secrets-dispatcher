# Project State

## Project Reference

See: .planning/PROJECT.md (updated 2026-02-25)

**Core value:** The user always knows exactly what they're approving before it happens, and no process in their desktop session can observe or tamper with the approval.
**Current focus:** v2.0 Privilege Separation — Phase 4: Foundation

## Current Position

Phase: 4 of 8 (Foundation)
Plan: 0 of ? in current phase
Status: Ready to plan
Last activity: 2026-02-25 — v2.0 roadmap created, ready to plan Phase 4

Progress: [░░░░░░░░░░░░░░░░░░░░] 0% (v2.0 phases)

## Performance Metrics

**Velocity:**
- Total plans completed (v2.0): 0
- Average duration: —
- Total execution time: —

**By Phase:**

| Phase | Plans | Total | Avg/Plan |
|-------|-------|-------|----------|
| v2.0 not started | — | — | — |

*Updated after each plan completion*

## Accumulated Context

### Decisions

Decisions are logged in PROJECT.md Key Decisions table.
Key decisions affecting v2.0 work:
- Companion user (not system user): needs real session for session D-Bus and systemd --user
- VT for trusted I/O: only kernel-enforced isolated I/O path on Linux; Wayland compositor is untrusted
- System D-Bus only: drop HTTP/WS/Web UI to minimize attack surface
- Standard Secret Service protocol between dispatcher and gopass-secret-service

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
Stopped at: Roadmap created for v2.0 (Phases 4-8), ready to plan Phase 4
Resume file: None
