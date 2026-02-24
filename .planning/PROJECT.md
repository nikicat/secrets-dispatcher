# Secrets Dispatcher

## What This Is

A D-Bus Secret Service approval proxy with GPG commit signing support. Intercepts secret access and GPG signing requests, shows the user full context (secret path, or repo/commit/author/files for signing), and requires explicit approval before proceeding. Solves the problem of blindly approving credential and signing dialogs when multiple automated sessions run in parallel.

## Core Value

The user always knows exactly what they're approving before it happens — whether it's a secret access or a cryptographic signature.

## Requirements

### Validated

- :white_check_mark: Approval request pipeline with pending/approve/deny/expire/cancel flow — existing
- :white_check_mark: REST API with auth for request management — existing
- :white_check_mark: WebSocket real-time event propagation — existing
- :white_check_mark: Web UI for viewing and approving requests — existing
- :white_check_mark: CLI for viewing and approving requests — existing
- :white_check_mark: Desktop notifications for incoming requests — existing
- :white_check_mark: Observer pattern for event subscribers — existing
- :white_check_mark: GPG signing requests flow through approval pipeline as `gpg_sign` request type — v1.0
- :white_check_mark: `gpg-sign` thin client intercepts `gpg.program`, sends commit data to daemon, blocks until approval — v1.0
- :white_check_mark: Signing context shows repo name, commit message, author/committer, changed files, key ID — v1.0
- :white_check_mark: Daemon calls real `gpg` after approval, returns signature to thin client — v1.0
- :white_check_mark: WebSocket delivers approval result and signature to thin client — v1.0
- :white_check_mark: Web UI displays signing requests distinctly from secret access requests — v1.0
- :white_check_mark: CLI displays signing request context in list/show/history — v1.0
- :white_check_mark: Desktop notifications with commit summary for signing requests — v1.0
- :white_check_mark: Session/client identity shown for parallel session disambiguation — v1.0
- :white_check_mark: Error propagation: daemon unreachable, GPG failures, request expiry — v1.0

### Active

(None — define with next milestone)

### Out of Scope

- Handling the GPG private key directly — gpg-agent manages key protection
- Replacing pinentry — pinentry still handles passphrase if not cached
- Non-git GPG signing — only git commit signing (tags can be added later)
- SSH commit signing — staying with GPG signatures
- Passphrase caching — gpg-agent handles this
- Full diff content display — payload explosion, rendering complexity
- Policy-based auto-approval — undermines human-in-the-loop model

## Context

Shipped v1.0 with 12,520 LOC Go + Svelte.
Tech stack: Go, D-Bus (godbus), Svelte 5, WebSocket, GPG.
Two request channels: D-Bus Secret Service proxy and GPG commit signing.
Desktop notifications via D-Bus org.freedesktop.Notifications.
Web UI with real-time updates, browser notifications, and session identity.
CLI with git-log-style formatting for both request types.

## Constraints

- **Protocol**: Must produce valid GPG/PGP signatures that git accepts
- **Compatibility**: D-Bus proxy and GPG signing coexist in same daemon
- **Latency**: HTTP/WebSocket roundtrip acceptable for interactive approval
- **Dependency**: Daemon must be running for signed commits and secret access

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| Custom `gpg.program` over pinentry wrapper | Full access to commit context at the git-gpg boundary; avoids Assuan protocol interception | :white_check_mark: Good — clean separation, rich context display |
| Daemon calls gpg, not the CLI client | CLI is a thin pipe; daemon owns signing flow and gpg interaction | :white_check_mark: Good — enables server-side error handling and signature caching |
| WebSocket for result delivery | Already implemented; avoids long-polling; consistent with existing pattern | :white_check_mark: Good — reuses infrastructure, real-time updates |
| New request type, not new approval system | Reuses existing approval manager, observers, web UI, CLI | :white_check_mark: Good — minimal new code, consistent UX |
| Shell wrapper for gpg.program | git uses execvp not shell-split; "binary subcommand" fails without wrapper | :white_check_mark: Good — reliable, simple `~/.local/bin/secrets-dispatcher-gpg` |
| Unix socket for thin client communication | User-scoped, no network exposure, no port conflicts | :white_check_mark: Good — secure by default |
| GPGRunner interface for testability | Enables unit test mocking without real gpg binary | :white_check_mark: Good — fast tests, no external deps |

---
*Last updated: 2026-02-24 after v1.0 milestone*
