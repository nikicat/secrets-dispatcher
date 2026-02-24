# Project State

## Project Reference

See: .planning/PROJECT.md (updated 2026-02-24)

**Core value:** The user always knows exactly what they're cryptographically signing before it happens.
**Current focus:** Phase 2 — Core Signing Flow

## Current Position

Phase: 2 of 3 (Core Signing Flow)
Plan: 2 of 5 in current phase
Status: In progress
Last activity: 2026-02-24 — Completed 02-02 (thin client pure functions: ParseCommitObject, FindRealGPG, extractKeyID)

Progress: [███░░░░░░░] 30%

## Performance Metrics

**Velocity:**
- Total plans completed: 5
- Average duration: 6 min
- Total execution time: 0.5 hours

**By Phase:**

| Phase | Plans | Total | Avg/Plan |
|-------|-------|-------|----------|
| 01-data-model-and-protocol-foundation | 3 | 24 min | 8 min |
| 02-core-signing-flow | 2 | 10 min | 5 min |

**Recent Trend:**
- Last 5 plans: 01-01 (8 min), 01-02 (8 min), 01-03 (8 min), 02-01 (5 min), 02-02 (5 min)
- Trend: Consistent

*Updated after each plan completion*
| Phase 02-core-signing-flow P01 | 5 | 2 tasks | 5 files |

## Accumulated Context

### Decisions

Decisions are logged in PROJECT.md Key Decisions table.
Recent decisions affecting current work:

- 01-01: CreateGPGSignRequest uses fire-and-forget timeout goroutine (not HTTP context) so dropped connections don't cancel active UI reviews
- 01-01: GPGSignInfo is a pointer on Request (nil for non-gpg_sign requests) to avoid serialization overhead
- 01-01: signature field is unexported; Phase 2 will wire real gpg output; Phase 1 leaves it unset
- 01-02: Used *approval.GPGSignInfo directly in PendingRequest (no field duplication like SenderInfo)
- 01-02: Placeholder signature via base64 literal in OnEvent — real signing in Phase 2
- Pre-Phase 2: Must decide `gpg.program` installation UX (shell wrapper vs. symlink dispatch) before Phase 2 implementation starts — research recommends shell wrapper as simpler
- [Phase 01]: Approve() and Deny() now delete from pending before closing done channel to handle CreateGPGSignRequest cleanup (no blocking goroutine defer)
- [Phase 01]: findEvent() helper used for event assertions — notify() dispatches async goroutines making ordering non-deterministic
- 02-02: First parent hash wins for merge commits (multiple parent lines) — display context only, not security-critical
- 02-02: Trailing newline stripped from commit message body — git appends trailing newline to commit objects
- 02-02: Combined short-flag detection for extractKeyID — arg starts with '-', not '--', ends with 'u' means next arg is key ID
- [Phase 02-01]: signature field promoted from unexported to exported Signature; GPGStatus/GPGExitCode use json:"-" to exclude from JSON pending list
- [Phase 02-01]: ValidateRequest checks cookie then Bearer — fixes thin client WebSocket auth that was cookie-only
- [Phase 02-01]: ApproveGPGFailed signals result=true/closes done channel — gpg failure is resolved request; ExitCode in WSMessage carries failure to thin client
- [Phase 02-01]: Unix socket served by same http.Server via second net.Listener — zero handler duplication

### Pending Todos

None yet.

### Blockers/Concerns

- Phase 2: `gpg.program` installation UX decision (shell wrapper vs. symlink) must be resolved before plan-phase runs — not complex but must be explicit
- Phase 2: Confirm worktree behavior (`GIT_DIR` + `GIT_WORK_TREE`) for changed-files collection via `git diff --cached` in worktree contexts

## Session Continuity

Last session: 2026-02-24
Stopped at: Completed 02-02-PLAN.md (thin client pure functions)
Resume file: None
