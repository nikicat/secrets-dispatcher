# Project Research Summary

**Project:** secrets-dispatcher — GPG Commit Signing Approval Gate
**Domain:** GPG signing proxy / human-in-the-loop commit approval for autonomous AI agents
**Researched:** 2026-02-24
**Confidence:** HIGH

## Executive Summary

The GPG commit signing milestone adds a second approval-gated request type to the existing secrets-dispatcher daemon. Git's `gpg.program` configuration allows the binary to intercept every `git commit -S` call — receiving the raw commit object on stdin, blocking until the user approves or denies, then returning the PGP-armored signature (from the real gpg binary) on stdout. The design solves the "blind signing" problem: today, gpg-agent/pinentry shows only "approve GPG signing" with no context about which Claude Code session is committing what. The feature presents the full commit context (message, author, changed files, repo, key ID) before asking for approval, mirroring Ledger's "clear signing" principle for hardware wallets.

The implementation requires zero new Go dependencies. Every component is covered by the existing stdlib plus the project's current dependencies. The thin `gpg-sign` subcommand acts as the `gpg.program` entrypoint, collects context, POSTs a synchronous blocking request to the daemon, and relays the signature back to git. The daemon invokes the real gpg binary after user approval — it never touches private key material. The existing `approval.Manager`, observer pipeline, WebSocket handler, desktop notifications, CLI, and web UI all extend naturally to the new `gpg_sign` request type with additive changes.

The critical risks are all implementation-level protocol details: wrong status-fd routing corrupts the PGP output silently, binary-unsafe stdin transport produces BAD signatures, and a daemon running without a TTY breaks pinentry. All three are well-understood and preventable with specific patterns documented in PITFALLS.md. The overall implementation confidence is HIGH — the git gpg.program interface is verified against the git source, the existing codebase patterns are well-suited for extension, and the required changes are surgical.

---

## Key Findings

### Recommended Stack

No new dependencies are needed. The feature is implemented entirely with Go stdlib (`os/exec`, `bufio`, `bytes`, `strings`, `encoding/base64`, `net/http`) plus the existing project dependencies. The `gpg-sign` client parses the raw git commit object format (a stable, well-documented plaintext format) in approximately 50 lines of stdlib code — go-git, ProtonMail/go-crypto, and Assuan protocol libraries were evaluated and all rejected as unnecessary or incompatible with the project's design.

The only external runtime dependency is the system `gpg` binary, which users must already have for commit signing. The daemon shells out to real gpg after approval, which keeps gpg-agent, pinentry, and passphrase caching working exactly as the user has configured them.

One unresolved installation UX question: `gpg.program` in git config must be a path to a single executable, not a command with arguments. Three options exist — a shell wrapper script, a symlink dispatched by `filepath.Base(os.Args[0])`, or a separate installed binary. The shell wrapper is simplest. This decision must be made before Phase 1 implementation.

**Core technologies:**
- `os/exec` (stdlib): gpg invocation in daemon, git subcommands in client — no PGP library needed
- `bufio`/`bytes`/`strings` (stdlib): commit object parsing from stdin — stable text format, ~50 lines
- `encoding/base64` (stdlib): binary-safe transport of raw commit bytes over JSON/HTTP
- `net/http` (stdlib): synchronous blocking POST from client to daemon — simpler than WebSocket for this flow
- Existing `internal/approval`, `internal/api`, `internal/cli`: extended additively, interfaces unchanged

### Expected Features

The feature divides cleanly into table-stakes (required for functional correctness) and differentiators (highest-value context signals for the user's decision). Everything else is explicitly deferred.

**Must have (table stakes):**
- Intercept `gpg.program` call and block until user decision — without this the feature does not exist
- Parse raw commit object from stdin: author, committer, message, parent hash
- Show repository name — disambiguates parallel Claude Code sessions
- Show key fingerprint / key ID — user must know whose key is vouching
- Approve / deny gate with return signature on approve, non-zero exit on deny
- Desktop notification on incoming request — user is away while agent runs
- Web UI display of signing request context
- CLI display extension for `gpg_sign` type
- Request expiry via existing timeout mechanism

**Should have (differentiators):**
- Changed files list (`git diff --cached --name-only`) — highest-value context signal; what code is being signed
- Session/client identity shown prominently — critical for parallel-session disambiguation
- Visual distinction of `gpg_sign` requests in web UI (different color/icon/label)
- File count summary in list view for quick scan across pending requests
- Parent commit short hash — anchors signing request to history

**Defer to v2+:**
- GPG tag signing (different object format, separate milestone)
- SSH commit signing (different mechanism entirely)
- Bulk approve for rebase scenarios (defeats the purpose of the gate for single commits)
- Policy-based auto-approval (undermines human-in-the-loop model)
- Full diff content display (payload too large; user can open IDE)

### Architecture Approach

The architecture is a clean extension of the existing layered design. The central `approval.Manager` is used unchanged — it already handles blocking, timeouts, and observer notifications. New components are: a `gpg-sign` subcommand (thin client, `main.go` dispatch + `internal/gpgsign/`), a GPG context collector package (`internal/gpgsign/context.go`), and a new API handler (`HandleGPGSign` in `internal/api/handlers.go`). Modified components are additive: a new `GPGSignInfo` struct on `approval.Request`, new types in `internal/api/types.go`, a new route in `server.go`, and display extensions in the CLI formatter, notification handler, and web UI.

The key architectural decision is synchronous HTTP for signature delivery: the client POSTs and blocks; the daemon returns the signature in the HTTP response after approval. This is simpler than a WebSocket round-trip and matches the inherently blocking nature of git's `gpg.program` subprocess call.

**Major components:**
1. `gpg-sign` subcommand (thin client) — receives git's args, collects commit context, calls daemon, relays signature to stdout
2. `internal/gpgsign/context.go` (NEW) — parses raw commit object from stdin; runs `git rev-parse` and `git diff --cached` for repo context and changed files
3. `internal/api` handler extension — `HandleGPGSign` endpoint: creates `gpg_sign` approval request, blocks on manager, invokes real gpg on approval, returns signature
4. `approval.Request` extension — adds `GPGSignInfo` struct with all commit context fields; raw `CommitObject []byte` stored in-memory only (not serialized to observers)
5. Web UI card extension — displays GPG sign context distinctly from secret requests

**Build order (dependency graph):**
1. Approval types (`internal/approval/types.go`) — `RequestTypeGPGSign` + `GPGSignInfo`
2. API types (`internal/api/types.go`) — `GPGSignRequest`, `GPGSignResponse`, `GPGSignInfo` on `PendingRequest`
3. `internal/gpgsign/context.go` (new package) — commit parser + context collector
4. `internal/api/handlers.go` — `HandleGPGSign` (depends on steps 1-3)
5. Route registration in `server.go` (depends on step 4)
6. Notification and CLI formatting extensions (depends on step 1)
7. `gpg-sign` subcommand in `main.go` + `internal/gpgsign/client.go` (depends on steps 2-5)
8. Web UI updates (depends on steps 2, 5)

Steps 3 and 7 can be developed independently once step 2 types are stable.

### Critical Pitfalls

1. **Wrong `--status-fd` direction corrupts PGP output** — Forward git's exact args verbatim via `os.Args[1:]`; never reconstruct them. If `--status-fd=2` is changed to `1`, GPG status lines mix into the signature on stdout and git sees a mangled PGP block. Silent commit failure.

2. **Binary-unsafe stdin handling produces BAD signatures** — Read stdin into `[]byte` only (never string). Transmit to daemon as base64-encoded JSON field. Daemon decodes before passing to gpg. Any UTF-8 coercion or string normalization changes the bytes being signed; the commit writes successfully but `git verify-commit` shows BAD signature.

3. **Daemon without TTY breaks pinentry on cache miss** — The daemon runs without a controlling TTY. When gpg-agent passphrase cache is cold, pinentry fails with "Inappropriate ioctl for device." Mitigation: ensure daemon runs in user's graphical session, has `$DISPLAY`/`$WAYLAND_DISPLAY` set, and document the `gpg-agent.conf` cache TTL requirement.

4. **gpg-agent socket not inherited** — If daemon is started by systemd without the user's environment, it may not find the gpg-agent socket. Explicitly pass `GNUPGHOME`, `XDG_RUNTIME_DIR`, `HOME`, `USER` to the exec'd gpg subprocess. Test from a clean systemd service context.

5. **Silent unsigned commits when daemon is unreachable** — `gpg-sign` must exit non-zero immediately with a clear stderr message if it cannot connect to the daemon. Never exit 0 on connection failure and never fall back to calling real gpg directly.

---

## Implications for Roadmap

Based on the build-order dependency graph from ARCHITECTURE.md and the critical pitfall phases from PITFALLS.md, the work naturally divides into three phases:

### Phase 1: Data Model and Protocol Foundation

**Rationale:** All subsequent work depends on stable type definitions. Approval manager extension, API types, and the new package structure must be established first. No external unknowns — these are pure in-repo changes with direct codebase knowledge.

**Delivers:** New `gpg_sign` request type in approval pipeline; `GPGSignInfo` struct; `GPGSignRequest`/`GPGSignResponse` API types; new `internal/gpgsign` package skeleton; route registered in server.

**Addresses:** Table-stakes features: new request type, approval manager extension, request expiry (inherited).

**Avoids:** Pitfall 9 (type collision) — audit all `RequestType` switch statements before any rendering code ships.

**Research flag:** None needed. Standard patterns, direct codebase extension.

---

### Phase 2: Core Signing Flow (Client + Daemon)

**Rationale:** The thin client and daemon handler are the functional core. This phase implements the full happy path: git calls `gpg-sign`, context is collected, daemon blocks on approval, real gpg is invoked, signature is returned. Critical pitfalls 1, 2, 3, 4, and 5 all live here and must be addressed during implementation.

**Delivers:** Working end-to-end signing flow. `git commit -S` with `gpg.program = sd-gpg` blocks until user approves in the daemon, then commits with a valid signature.

**Addresses:** All table-stakes features: intercept gpg.program, parse commit object, block/approve/deny, return signature, non-zero on deny, changed files list (highest-value differentiator), repository name, key ID.

**Avoids:**
- Pitfall 1: pass `os.Args[1:]` verbatim to real gpg
- Pitfall 2: `[]byte` + base64 throughout
- Pitfall 3: TTY/pinentry setup
- Pitfall 4: gpg-agent socket inheritance
- Pitfall 5: non-zero exit when daemon unreachable
- Pitfall 6: exit code propagation

**Uses:** `os/exec`, `bufio`/`bytes`/`strings`, `encoding/base64`, `net/http` (all stdlib).

**Research flag:** Needs validation of gpg.program installation approach (shell wrapper vs. symlink) before implementation starts. The decision is not complex but must be made explicitly.

---

### Phase 3: UI and Observability Extensions

**Rationale:** Notification, CLI, and web UI updates are straightforward display-layer changes once the data model (Phase 1) is stable. They do not block the core signing flow but are required for a usable product.

**Delivers:** Web UI displays `gpg_sign` requests distinctly with all commit context. CLI `list` and `show` handle the new type. Desktop notifications fire with commit summary. Session identity is shown prominently.

**Addresses:** Differentiator features: visual distinction in web UI, session identity prominence, file count summary, parent hash display. Pitfall 12 mitigation (parallel sessions confusion) lives here.

**Avoids:** Pitfall 9 (unhandled type in switch statements) — all rendering code updated together.

**Research flag:** None. Standard display-layer patterns, no novel integration.

---

### Phase Ordering Rationale

- Phase 1 before Phase 2: `HandleGPGSign` and the thin client depend on stable `GPGSignInfo` and API types.
- Phase 2 before Phase 3: Web UI and CLI need real data flowing through the pipeline before display logic is meaningful to test.
- Phases 1-3 are tightly scoped to this milestone. There is no deferred complexity requiring a Phase 4 — all table-stakes and differentiators fit in three phases.
- The `gpg.program` installation UX decision (shell wrapper vs. symlink) should be resolved in Phase 1 planning, not deferred to Phase 2, because it affects the `main.go` dispatch logic and README instructions.

### Research Flags

Phases needing deeper research during planning:
- **Phase 2:** Validate the `gpg.program` installation approach (shell wrapper vs. symlink via `filepath.Base(os.Args[0])`). Confirm which git versions set `GIT_WORK_TREE` alongside `GIT_DIR` in worktree contexts (affects changed-files collection). Both are low-stakes decisions but must be made explicitly before code is written.
- **Phase 3:** WebSocket message size — confirm `conn.SetReadLimit` applies only to reads in the current codebase before implementing signature delivery. One-line check, not a research task.

Phases with standard patterns (skip research-phase):
- **Phase 1:** Pure data model extension with direct codebase knowledge. No unknowns.
- **Phase 3:** Standard display-layer extension. All patterns established in existing CLI formatter and web UI.

---

## Confidence Assessment

| Area | Confidence | Notes |
|------|------------|-------|
| Stack | HIGH | git gpg.program interface verified against git source code; stdlib approach confirmed correct; all "do not use" libraries verified as inappropriate |
| Features | HIGH | git commit object format stable and well-documented; feature set derived from direct codebase analysis; "clear signing" UX principle well-established |
| Architecture | HIGH | Based on direct codebase analysis; existing patterns are well-suited for additive extension; data flow verified step-by-step |
| Pitfalls | HIGH | Critical pitfalls verified against git source, GnuPG issue tracker, Go stdlib issues; all are real, documented failure modes with known mitigations |

**Overall confidence:** HIGH

### Gaps to Address

- **gpg.program installation UX**: Shell wrapper vs. symlink dispatch. Research has identified both options and their tradeoffs. Decision needed before Phase 2 implementation. Recommendation: shell wrapper (simpler, no binary changes).
- **Worktree GIT_DIR + GIT_WORK_TREE interaction**: STACK.md recommends `git rev-parse --show-toplevel` and `git -C <root> diff --cached`. PITFALLS.md confirms worktree risk. Implementation should use `--show-toplevel` approach and include a worktree test before shipping Phase 2.
- **gpg-agent cache TTL guidance**: The correct value for `default-cache-ttl` in `gpg-agent.conf` relative to the approval timeout needs documentation. Low-risk but must appear in setup instructions.

---

## Sources

### Primary (HIGH confidence)

- [git/git — gpg-interface.c](https://github.com/git/git/blob/master/gpg-interface.c) — exact `--status-fd=2 -bsau <key-id>` invocation, `[GNUPG:] SIG_CREATED` success detection string
- [gitformat-signature(5)](https://git-scm.com/docs/gitformat-signature) — raw commit object format (tree/parent/author/committer/message), pre-signature payload structure
- [GnuPG T5885](https://dev.gnupg.org/T5885) — "Inappropriate ioctl for device" TTY error, confirmed behavior
- [GnuPG Agent Forwarding wiki](https://wiki.gnupg.org/AgentForwarding) — socket environment inheritance requirements
- [GnuPG Common Problems](https://www.gnupg.org/documentation/manuals/gnupg/Common-Problems.html) — GPG_TTY requirements
- Existing codebase: `internal/approval/manager.go`, `internal/api/handlers.go`, `internal/api/websocket.go`, `internal/api/server.go`, `internal/cli/format.go`, `internal/notification/desktop.go`, `main.go`

### Secondary (MEDIUM confidence)

- [Ledger — What Is Clear Signing?](https://www.ledger.com/academy/topics/ledgersolutions/what-is-clear-signing) — UX principle for showing full context before cryptographic approval
- [atom/github gpg-wrapper.sh](https://github.com/atom/github/blob/master/bin/gpg-wrapper.sh) — gpg.program wrapper pattern (pass `"$@"`, intercept stdin)
- [opentimestamps-client #87](https://github.com/opentimestamps/opentimestamps-client/issues/87) — git worktree GIT_DIR pitfall
- [Daniel15 — GPG "Inappropriate ioctl"](https://d.sb/2016/11/gpg-inappropriate-ioctl-for-device-errors) — pinentry failure in non-TTY context
- [go-git v5 object package](https://pkg.go.dev/github.com/go-git/go-git/v5/plumbing/object) — evaluated and rejected as overkill for commit object parsing

### Tertiary (LOW confidence)

- [Zed discussion — AI agent accidentally making commits](https://github.com/zed-industries/zed/discussions/31762) — parallel agent commit risks (context only)
- [git worktrees for parallel AI agents](https://dev.to/mashrulhaque/git-worktrees-for-ai-coding-run-multiple-agents-in-parallel-3pgb) — parallel session usage patterns

---
*Research completed: 2026-02-24*
*Ready for roadmap: yes*
