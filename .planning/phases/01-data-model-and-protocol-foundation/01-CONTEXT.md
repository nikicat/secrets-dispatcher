# Phase 1: Data Model and Protocol Foundation - Context

**Gathered:** 2026-02-24
**Status:** Ready for planning

<domain>
## Phase Boundary

Establish `gpg_sign` request type in the approval pipeline, define `GPGSignInfo` struct with all commit context fields, define API types (`GPGSignRequest`/`GPGSignResponse`), register API route with a working handler that creates approval requests and blocks. Does NOT include the thin client, gpg invocation, or display layer extensions.

</domain>

<decisions>
## Implementation Decisions

### Phase 1 Scope
- Handler is fully working: creates `gpg_sign` approval request, blocks on user decision, returns result
- No gpg invocation in Phase 1 — handler returns mock/placeholder signature data on approval (real gpg exec comes in Phase 2)
- No thin client in Phase 1 — endpoint is testable but not wired to git yet
- Unit tests only — no manual curl smoke tests required

### gpg.program Installation
- Shell wrapper script approach: a small `.sh` script that calls `secrets-dispatcher gpg-sign -- "$@"`
- User configures `git config --global gpg.program /path/to/sd-gpg-sign.sh`
- Script ships in the repo (e.g., `scripts/sd-gpg-sign.sh`) and gets installed/copied by the user
- This is a Phase 2 deliverable but the decision is locked now so Phase 2 doesn't need to revisit it

### API Contract
- POST + WebSocket hybrid approach for signature delivery:
  1. Client POSTs to `POST /api/v1/gpg-sign/request` with commit data + context → returns request ID
  2. Client listens on existing WebSocket for approval result event carrying the signature
- Reuses the existing WebSocket infrastructure rather than introducing a blocking HTTP pattern
- The WebSocket event for `gpg_sign` must carry the actual signature bytes (atomic delivery — no separate fetch)
- On denial or timeout, WebSocket event carries the error status

### Claude's Discretion
- Exact field naming conventions in `GPGSignInfo` struct
- Whether `GPGSignInfo` is a pointer or embedded on `Request`
- Internal organization of new types across files

</decisions>

<specifics>
## Specific Ideas

- The POST endpoint should follow the existing handler pattern in `internal/api/handlers.go`
- The approval request should flow through the full observer pipeline so Phase 3 display work is unblocked
- WebSocket event type for signing results should be distinct from existing secret-access events

</specifics>

<deferred>
## Deferred Ideas

None — discussion stayed within phase scope

</deferred>

---

*Phase: 01-data-model-and-protocol-foundation*
*Context gathered: 2026-02-24*
