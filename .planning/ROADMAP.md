# Roadmap: Secrets Dispatcher — GPG Commit Signing

## Overview

Three phases extend the existing approval pipeline with a second request type: GPG commit signing. Phase 1 lays the data model and API types that every subsequent component depends on. Phase 2 implements the functional core — the `gpg-sign` thin client and daemon handler that make `git commit -S` block until the user approves. Phase 3 extends the display layer so the web UI, CLI, and desktop notifications present signing context distinctly and usefully.

## Phases

**Phase Numbering:**
- Integer phases (1, 2, 3): Planned milestone work
- Decimal phases (2.1, 2.2): Urgent insertions (marked with INSERTED)

Decimal phases appear between their surrounding integers in numeric order.

- [ ] **Phase 1: Data Model and Protocol Foundation** - Establish `gpg_sign` request type, `GPGSignInfo` struct, and API types that all other components depend on
- [ ] **Phase 2: Core Signing Flow** - Implement `gpg-sign` thin client and daemon handler for end-to-end commit signing with approval gate
- [ ] **Phase 3: UI and Observability** - Extend web UI, CLI, and desktop notifications to display `gpg_sign` request context distinctly

## Phase Details

### Phase 1: Data Model and Protocol Foundation
**Goal**: The `gpg_sign` request type exists in the approval pipeline with all its context fields, and the API contract is defined so all subsequent components have a stable foundation to build on
**Depends on**: Nothing (first phase)
**Requirements**: SIGN-06, SIGN-09, ERR-03
**Success Criteria** (what must be TRUE):
  1. A `gpg_sign` approval request can be created in the approval manager and flows through the observer pipeline (pending, approve, deny, expire, cancel)
  2. The `GPGSignInfo` struct carries all commit context fields: repo name, commit message, author, committer, key ID/fingerprint, changed files list, parent hash
  3. The API types `GPGSignRequest` and `GPGSignResponse` are defined and the route is registered in the server (even if the handler is a stub)
  4. Signing requests expire via the existing timeout mechanism without any code change to the approval manager
**Plans**: TBD

Plans:
- [ ] 01-01: TBD

### Phase 2: Core Signing Flow
**Goal**: Running `git commit -S` with `gpg.program` set to the `gpg-sign` subcommand blocks until the user approves in the daemon, then commits with a valid GPG signature — or exits non-zero if denied or daemon is unreachable
**Depends on**: Phase 1
**Requirements**: SIGN-01, SIGN-02, SIGN-03, SIGN-04, SIGN-05, SIGN-07, SIGN-08, ERR-01, ERR-02
**Success Criteria** (what must be TRUE):
  1. `git commit -S` with `gpg.program = /path/to/gpg-sign` sends a signing request to the daemon and blocks until the user approves or denies
  2. After approval, the commit lands in `git log` with a valid signature verified by `git verify-commit HEAD`
  3. After denial, `git commit` exits non-zero and no commit is created
  4. If the daemon is unreachable, `gpg-sign` exits non-zero immediately with a clear stderr message (never falls back to calling real gpg)
  5. GPG exit codes from signing failures (e.g., key not found) propagate through the daemon to `git commit` as non-zero exits
**Plans**: TBD

Plans:
- [ ] 02-01: TBD

### Phase 3: UI and Observability
**Goal**: All display surfaces — web UI, CLI, and desktop notifications — present `gpg_sign` request context in a way that lets the user immediately understand what they are signing and which session is requesting it
**Depends on**: Phase 2
**Requirements**: DISP-01, DISP-02, DISP-03, DISP-04, DISP-05, DISP-06
**Success Criteria** (what must be TRUE):
  1. A desktop notification fires when a signing request arrives and includes the repo name and first line of the commit message
  2. The web UI displays a `gpg_sign` card that shows repo, commit message, author, changed files list, and key ID — visually distinct from `get_secret` cards (different color, icon, or label)
  3. `secrets-dispatcher list` shows `gpg_sign` requests with file count summary; `secrets-dispatcher show <id>` displays full commit context
  4. The session or client identity is shown prominently enough that a user with two parallel Claude Code sessions can tell which session is requesting the signature
**Plans**: TBD

Plans:
- [ ] 03-01: TBD

## Progress

**Execution Order:**
Phases execute in numeric order: 1 → 2 → 3

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 1. Data Model and Protocol Foundation | 0/? | Not started | - |
| 2. Core Signing Flow | 0/? | Not started | - |
| 3. UI and Observability | 0/? | Not started | - |
