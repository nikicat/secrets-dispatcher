# Requirements: Secrets Dispatcher — GPG Commit Signing

**Defined:** 2026-02-24
**Core Value:** The user always knows exactly what they're cryptographically signing before it happens.

## v1 Requirements

Requirements for initial release. Each maps to roadmap phases.

### Signing Flow

- [x] **SIGN-01**: `gpg-sign` subcommand intercepts git's `gpg.program` call and blocks until user approves or denies
- [x] **SIGN-02**: Thin client parses raw commit object from stdin to extract author, committer, message, and parent hash
- [x] **SIGN-03**: Thin client resolves repository name from working directory via `git rev-parse --show-toplevel`
- [x] **SIGN-04**: Thin client collects changed files list via `git diff --cached --name-only`
- [x] **SIGN-05**: Thin client sends commit data + context to daemon via API as JSON (commit object as string field)
- [x] **SIGN-06**: Daemon creates `gpg_sign` approval request with full commit context and blocks on user decision
- [x] **SIGN-07**: Daemon calls real `gpg` with original args verbatim after approval, captures signature and status output
- [x] **SIGN-08**: Signature and gpg status output returned to thin client; client writes signature to stdout and status to stderr
- [x] **SIGN-09**: Key ID / fingerprint extracted from gpg args and shown in approval context

### Display & Notifications

- [x] **DISP-01**: Desktop notification fires when a signing request arrives with commit summary
- [ ] **DISP-02**: Web UI displays signing request context (repo, message, author, files, key ID)
- [ ] **DISP-03**: Web UI visually distinguishes `gpg_sign` requests from `get_secret` requests (different color/icon/label)
- [x] **DISP-04**: CLI `list` and `show` commands display `gpg_sign` request type with commit context
- [ ] **DISP-05**: Session/client identity shown prominently for parallel session disambiguation
- [x] **DISP-06**: File count summary shown in list view for quick scanning

### Error Handling

- [x] **ERR-01**: Thin client exits non-zero with clear stderr message when daemon is unreachable
- [x] **ERR-02**: Exit code from real gpg failures propagated through daemon to thin client
- [x] **ERR-03**: Signing requests expire via existing timeout mechanism

## v2 Requirements

Deferred to future release. Tracked but not in current roadmap.

### Extended Signing

- **ESIGN-01**: GPG tag signing support (different object format)
- **ESIGN-02**: SSH commit signing support (different mechanism)

## Out of Scope

| Feature | Reason |
|---------|--------|
| Full diff content display | Payload explosion, rendering complexity — user can open IDE |
| Passphrase capture | Breaks gpg-agent trust model — key protection is gpg-agent's job |
| Bulk approve for rebase | Defeats the purpose of the approval gate |
| Policy-based auto-approval | Undermines human-in-the-loop model |
| Direct PGP key handling | gpg-agent manages keys; daemon never touches private key material |
| Base64 encoding of commit data | Commit objects are UTF-8 text; plain JSON string preserves bytes and aids debugging |

## Traceability

Which phases cover which requirements. Updated during roadmap creation.

| Requirement | Phase | Status |
|-------------|-------|--------|
| SIGN-01 | Phase 2 | Complete |
| SIGN-02 | Phase 2 | Complete |
| SIGN-03 | Phase 2 | Complete |
| SIGN-04 | Phase 2 | Complete |
| SIGN-05 | Phase 2 | Complete |
| SIGN-06 | Phase 1 | Complete |
| SIGN-07 | Phase 2 | Complete |
| SIGN-08 | Phase 2 | Complete |
| SIGN-09 | Phase 1 | Complete |
| DISP-01 | Phase 3 | Complete |
| DISP-02 | Phase 3 | Pending |
| DISP-03 | Phase 3 | Pending |
| DISP-04 | Phase 3 | Complete |
| DISP-05 | Phase 3 | Pending |
| DISP-06 | Phase 3 | Complete |
| ERR-01 | Phase 2 | Complete |
| ERR-02 | Phase 2 | Complete |
| ERR-03 | Phase 1 | Complete |

**Coverage:**
- v1 requirements: 18 total
- Mapped to phases: 18
- Unmapped: 0

---
*Requirements defined: 2026-02-24*
*Last updated: 2026-02-24 after roadmap creation*
