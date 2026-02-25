# Phase 4: Foundation - Context

**Gathered:** 2026-02-25
**Status:** Ready for planning

<domain>
## Phase Boundary

Companion user exists, system D-Bus policy is verified, and the provisioning tool creates the full deployment skeleton — all before any companion-side daemon code beyond stubs is written. Delivers: companion user creation, D-Bus policy, systemd units, provisioning subcommand, structured daemon skeleton with D-Bus registration.

</domain>

<decisions>
## Implementation Decisions

### Provisioning tool UX
- Subcommand of main binary: `secrets-dispatcher provision` and `secrets-dispatcher provision --check`
- Non-interactive — scriptable, no prompts, fails with clear messages on precondition failures
- Requires root — must be run as root (sudo secrets-dispatcher provision), fails immediately if not root
- `--check` output: human-readable checklist with pass/fail per component and actionable fix hints on failures

### Companion user placement
- Home directory: `/var/lib/secret-companion/{username}` (e.g., `/var/lib/secret-companion/nb`)
- Parent `/var/lib/secret-companion/` owned by root with 0755
- User home subdir owned by companion user with 0700
- Username: configurable via `--companion-name` flag, default `secrets-{username}`
- Shell: /usr/sbin/nologin — no interactive login, companion session managed by systemd

### Daemon skeleton scope
- Structured skeleton: D-Bus name registration + empty handler interfaces + slog logging + signal handling + sd-notify readiness notification
- Runs as systemd user service under companion user (unit file installed by provisioning)
- Same binary, subcommand: `secrets-dispatcher daemon`
- Extends existing cobra CLI alongside provisioning subcommand
- Stub methods return canned responses — proves D-Bus policy works, Phase 5 adds real logic

### HTTP/WebSocket handling
- Keep existing HTTP/WS/Web UI code untouched
- New `daemon` subcommand simply does not initialize HTTP listeners
- Old `serve` command continues to work as before
- HTTP/WS kept as opt-in alternative permanently (not planned for removal)

### Testing approach
- All tests fully automated — no manual steps, no user-assisted testing, no "run this and verify"
- Three test layers: unit (mocks), integration (private D-Bus daemon), VM E2E
- Every layer runs with `go test` or equivalent CI-compatible command
- Tests must prove success criteria without human intervention

### Claude's Discretion
- D-Bus policy XML structure and exact method signatures for stubs
- systemd unit file details (dependencies, ordering, environment)
- Signal handler implementation
- Test framework and mock strategy for Phase 4 tests
- Exact cobra command tree restructuring

</decisions>

<specifics>
## Specific Ideas

- Provisioning should detect desktop user from SUDO_USER environment variable (since it runs as root)
- The `--companion-name` flag allows non-default companion usernames for multi-user setups or testing
- systemd linger must be enabled for companion user (COMP-05) so systemd --user persists

</specifics>

<deferred>
## Deferred Ideas

- SSH agent forwarding to companion — move SSH private keys to companion user's vault, forward desktop user SSH requests via proxy. Same privilege separation pattern as GPG. Capture as future milestone (v2.1 or v3).

</deferred>

---

*Phase: 04-foundation*
*Context gathered: 2026-02-25*
