# Phase 2: Core Signing Flow - Context

**Gathered:** 2026-02-24
**Status:** Ready for planning

<domain>
## Phase Boundary

Implement the `gpg-sign` thin client subcommand and daemon-side signing handler so that `git commit -S` with `gpg.program` set to `secrets-dispatcher gpg-sign` blocks until the user approves, then commits with a real GPG signature — or exits non-zero if denied, timed out, or daemon is unreachable. Includes a `gpg-sign setup` command for configuring git.

</domain>

<decisions>
## Implementation Decisions

### Installation & setup
- `gpg-sign` is a subcommand of the `secrets-dispatcher` binary — not a separate binary or shell wrapper
- `secrets-dispatcher gpg-sign setup` command configures git's `gpg.program` setting
- Setup defaults to `--global` scope; user can pass `--local` for per-repo
- Setup only sets `gpg.program` — does NOT enable `commit.gpgsign=true` (user opts in to auto-signing separately)

### Daemon discovery
- Daemon listens on a Unix socket at `$XDG_RUNTIME_DIR/secrets-dispatcher/api.sock`
- Thin client connects to the same Unix socket using HTTP-over-Unix-socket (reuses existing HTTP API handlers)
- Thin client opens WebSocket connection FIRST, then POSTs the signing request, then waits for `request_resolved` event matching the request ID — ensures no events are missed

### Real GPG invocation
- Daemon finds real gpg by scanning PATH, skipping its own binary
- Args are parsed and reconstructed (not passed verbatim) — thin client extracts key ID etc. from git's gpg args, daemon builds its own gpg invocation
- Commit object (raw data from stdin) is sent to the daemon in the API request body; daemon feeds it to real gpg's stdin — all signing happens daemon-side
- Daemon assumes the user's gpg-agent is reachable (inherits environment); if gpg-agent isn't running, real gpg fails and the error propagates naturally

### Error UX
- Named errors with actionable guidance — messages identify `secrets-dispatcher` by name and suggest fixes
- Daemon unreachable: `secrets-dispatcher: daemon unreachable at /run/user/.../api.sock. Is secrets-dispatcher running?` (exit 2)
- User denied: `secrets-dispatcher: signing request denied by user` (exit 1)
- Timeout: `secrets-dispatcher: signing request timed out (no response within Xs)` — distinct from denial
- Debug mode: `SECRETS_DISPATCHER_DEBUG=1` environment variable enables verbose stderr logging for connection troubleshooting

### Claude's Discretion
- Exact gpg arg parsing strategy (how to extract key ID, status-fd handling)
- WebSocket connection timeout and retry logic
- Unix socket directory creation and permissions
- How to detect "self" when scanning PATH for real gpg

</decisions>

<specifics>
## Specific Ideas

- WebSocket-before-POST ordering is explicit: connect first, then create request, so no race between request creation and event subscription
- Error exit codes: 1 for denial/timeout (user-caused), 2 for system errors (daemon unreachable, gpg failed)

</specifics>

<deferred>
## Deferred Ideas

None — discussion stayed within phase scope

</deferred>

---

*Phase: 02-core-signing-flow*
*Context gathered: 2026-02-24*
