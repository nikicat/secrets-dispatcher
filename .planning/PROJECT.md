# Secrets Dispatcher — GPG Commit Signing

## What This Is

An extension to secrets-dispatcher that intercepts GPG commit signing requests, shows the user full context about what they're signing (repo, commit message, author, changed files), and requires explicit approval before producing the signature. Solves the problem of blindly approving GPG pinentry dialogs when multiple Claude Code sessions run parallel commits.

## Core Value

The user always knows exactly what they're cryptographically signing before it happens.

## Requirements

### Validated

- :white_check_mark: Approval request pipeline with pending/approve/deny/expire/cancel flow — existing
- :white_check_mark: REST API with auth for request management — existing
- :white_check_mark: WebSocket real-time event propagation — existing
- :white_check_mark: Web UI for viewing and approving requests — existing
- :white_check_mark: CLI for viewing and approving requests — existing
- :white_check_mark: Desktop notifications for incoming requests — existing
- :white_check_mark: Observer pattern for event subscribers — existing

### Active

- [ ] GPG signing requests flow through the existing approval pipeline as a new request type
- [ ] `secrets-dispatcher gpg-sign` subcommand acts as `gpg.program` — thin client that sends signing data + context to the daemon and receives the signature back
- [ ] Signing request shows: repository name, commit message, author/committer, changed files list
- [ ] Daemon produces the signature by calling real `gpg` after approval
- [ ] `gpg-sign` client uses WebSocket to wait for approval result and receive the signature
- [ ] Web UI displays signing request context (repo, message, author, files) distinctly from secret access requests
- [ ] CLI displays signing request context in list/show commands

### Out of Scope

- Handling the GPG private key directly — gpg-agent manages key protection
- Replacing pinentry — pinentry still handles passphrase if not cached
- Non-git GPG signing — only git commit signing for now (tags can be added later)
- SSH commit signing — staying with GPG signatures
- Passphrase caching — gpg-agent handles this

## Context

- secrets-dispatcher already proxies Secret Service D-Bus requests with user approval
- The approval manager, API server, WebSocket, web UI, CLI, and desktop notifications are all in place
- The new GPG signing feature is a second "channel" into the same approval pipeline
- Git calls `gpg.program` with `-bsau <key-id>` args and commit object data on stdin
- The commit object contains: tree hash, parent hash(es), author, committer, commit message
- Changed files can be determined by running `git diff --cached --name-only` from the repo directory
- The daemon calls real `gpg` to produce the signature, so gpg-agent/pinentry handle passphrase as usual
- User sees two layers: (1) context-aware approval in secrets-dispatcher, (2) passphrase in pinentry if not cached

## Constraints

- **Protocol**: Must produce valid GPG/PGP signatures that git accepts — faithful proxying of gpg args and stdin
- **Compatibility**: Must work alongside existing D-Bus proxy functionality — same daemon, same approval manager
- **Latency**: The HTTP/WebSocket roundtrip adds latency to every signed commit — acceptable since user approval is inherently interactive
- **Dependency**: Requires secrets-dispatcher daemon to be running for commits to succeed

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| Custom `gpg.program` over pinentry wrapper | Full access to commit context at the git-gpg boundary; avoids Assuan protocol interception and process tree issues with gpg-agent | -- Pending |
| Daemon calls gpg, not the CLI client | Clean separation: CLI is a thin pipe, daemon owns the signing flow and gpg interaction | -- Pending |
| WebSocket for result delivery | Already implemented; avoids HTTP long-polling; consistent with existing real-time update pattern | -- Pending |
| New request type, not new approval system | Reuses existing approval manager, observers, web UI, CLI — minimal new code | -- Pending |

---
*Last updated: 2026-02-24 after initialization*
