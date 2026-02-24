# Feature Landscape

**Domain:** GPG commit signing proxy / approval gate for autonomous AI agents
**Researched:** 2026-02-24
**Confidence:** HIGH (core protocol behavior from git source/docs), MEDIUM (UX patterns from hardware wallet analogy and codebase analysis)

---

## Context: The Problem Being Solved

When multiple Claude Code sessions run parallel commits, the standard gpg-agent/pinentry flow
presents a dialog that says "Approve GPG signing" with no context about which session is
committing what. The user must blindly approve or deny with no visibility into what is being
cryptographically signed.

This is the same problem hardware wallets call "blind signing" — the user clicks OK without
being certain what the signature authorizes. Ledger's solution ("clear signing") is the
direct analogy: present every meaningful field in human-readable form before the user
commits their cryptographic attestation.

This feature adds a second request type (alongside the existing `get_secret` and `search`
secret service types) to the existing approval pipeline. The gpg.program interface is the
integration point: git calls `gpg --status-fd=2 -bsau <key-id>` with the raw commit object
on stdin.

---

## Table Stakes

Features where their absence makes the tool non-functional or untrustworthy.

| Feature | Why Expected | Complexity | Notes |
|---------|--------------|------------|-------|
| Intercept gpg.program call and block until approval | Core function — without this the whole feature doesn't exist | Med | `secrets-dispatcher gpg-sign` subcommand acts as `gpg.program`; sends signing data to daemon via HTTP; blocks on WebSocket waiting for approval result |
| Parse raw commit object from stdin | User cannot evaluate what they're signing without this | Low | Commit object format is stable: `tree`, `parent`, `author`, `committer`, blank line, message. Parse before sending to daemon |
| Show commit message | Primary signal for evaluating "what is this commit?" | Low | Extracted from parsed commit object |
| Show author and committer | Distinguish AI agent identity from repo owner | Low | Extracted from parsed commit object; both fields since they can differ |
| Show repository name | Disambiguates parallel sessions on different repos | Low | Derive from `git rev-parse --show-toplevel` run by the `gpg-sign` client in the signing directory (git sets `GIT_DIR`) |
| Show key fingerprint being used | User must know whose key is vouching for this commit | Low | Passed as `-u <key-id>` argument by git to gpg.program |
| Approve / deny decision flow | Without both options the gate is not meaningful | Low | Reuses existing `approval.Manager.Approve()` / `Deny()` |
| Return signature to git on approve | git requires the PGP-armored detached signature on stdout | Med | Daemon calls real `gpg --status-fd=2 -bsau <key-id>` with commit object, returns stdout to `gpg-sign` client |
| Return non-zero exit on deny | git must see failure to abort the commit | Low | `gpg-sign` exits non-zero; git surfaces error to user |
| Desktop notification on incoming request | User is doing other things while agent runs | Low | Reuses existing `notification.Handler` observer |
| Web UI display of signing request | Primary approval UI — must show all signing context | Med | New request type card/section in existing UI |
| CLI display of signing request | Secondary approval UI for terminal users | Low | Extend `format.go` to handle `gpg_sign` request type |
| Request expiry | Daemon cannot block forever if user walks away | Low | Reuses existing `ExpiresAt` / timeout mechanism |

---

## Differentiators

Features that add meaningful value beyond the functional minimum.

| Feature | Value Proposition | Complexity | Notes |
|---------|-------------------|------------|-------|
| Show changed files list | Biggest context signal — "what code am I signing?" | Med | `gpg-sign` client runs `git diff --cached --name-only` before calling daemon; passes as `Files []string` in request payload. Requires knowing the working directory, which is available because git sets `GIT_DIR` |
| Show number of files changed as summary in list view | Quick scan across multiple pending requests | Low | Derive from `Files` list length for compact display |
| Show parent commit hash (short) | Anchors the signing request to a specific point in history; lets user cross-check in their IDE | Low | Extracted from commit object `parent` line |
| Distinguish `gpg_sign` request type visually in web UI | Signing requests warrant a different visual weight than secret access | Low | Different color/icon/label in UI — not just the type string |
| Timestamp of signing request | When did the agent try to commit? Useful if reviewing a backlog of requests | Low | Already captured as `CreatedAt` in existing request structure |
| Session/client identity shown prominently | With parallel Claude Code sessions the "which agent?" question is critical | Low | Already captured as `Client` field; must be prominent in signing request UI, not buried |
| History of signing decisions | Audit trail of what the user approved, denied, or let expire | Low | Reuses existing history mechanism — `gpg_sign` requests appear alongside secret requests |
| JSON output for `gpg-sign` context in CLI | Scriptable for power users / automation | Low | `--json` flag already exists in CLI formatter |

---

## Anti-Features

Things to deliberately NOT build in this milestone.

| Anti-Feature | Why Avoid | What to Do Instead |
|--------------|-----------|-------------------|
| Showing full diff content | Massive payloads for large commits; UI becomes unreadable; diff format requires significant rendering logic | Show filenames only; user can open their IDE to see the diff |
| Replacing pinentry | Passphrase caching is gpg-agent's responsibility; intercepting Assuan protocol adds significant complexity and fragility | Let pinentry handle passphrase if not cached; sign the request first, then let gpg-agent prompt for passphrase if needed |
| Handling GPG private key directly | Fundamental security boundary violation | gpg-agent owns key protection; daemon calls real `gpg`, never touches key material directly |
| GPG tag signing (non-commit objects) | Different object format, different context needed; adds complexity for a rare operation | Add as a separate follow-on milestone; scope this to commits only |
| SSH commit signing | Different signing mechanism, different gpg.program behavior; would require parallel implementation | Scope note in PROJECT.md: GPG only |
| Passphrase capture/relay | Security anti-pattern; breaks gpg-agent trust model | Daemon passes signing request to real `gpg`; gpg-agent handles passphrase entirely |
| Bulk approve all pending signing requests | Defeats the purpose of the gate | Approve one at a time; force the user to evaluate each commit |
| Policy-based auto-approval (e.g., "always approve from this repo") | Introduces complex policy surface; undermines the human-in-the-loop model | If the user wants auto-approval, they can simply not configure `gpg.program` to use the dispatcher |
| Non-blocking "notify only" mode | A non-blocking gate is not a gate | The entire value is the blocking approval; notification-only is already handled by gpg-agent/pinentry |

---

## Feature Dependencies

```
Parse commit object from stdin
  → Show commit message
  → Show author / committer
  → Show parent commit hash

Run git diff --cached --name-only (in repo working dir)
  → Show changed files list
  → Show file count summary in list view

Approval pipeline (existing)
  → Request create / approve / deny / expire / cancel
  → Desktop notification (existing observer)
  → WebSocket real-time update (existing)
  → History record (existing)

Daemon calls real gpg after approval
  → Return PGP signature to gpg-sign client via API
  → gpg-sign writes signature to stdout (git reads it)

gpg-sign exits non-zero on deny/expire
  → git aborts commit with error message to user
```

---

## Information Hierarchy for the Approval UI

Ordered by importance to the user's decision:

1. **Request type label** — "GPG Commit Signing Request" (not "get_secret")
2. **Client/session identity** — which Claude Code session is asking
3. **Repository name** — which repo is being committed to
4. **Commit message** — what the agent claims this commit does
5. **Author / Committer** — who is attributed in the commit metadata
6. **Changed files list** — what code is actually being committed
7. **Key fingerprint** — whose GPG key will vouch for this
8. **Parent commit (short hash)** — anchors to history
9. **Expires in** — urgency signal

Items 1-4 must be visible without scrolling. Items 5-9 can be in an expandable section or below the fold.

---

## MVP Recommendation

Prioritize:

1. **Parse commit object** — author, committer, commit message, parent hash (from stdin)
2. **Derive repository name** — from `GIT_DIR` or `git rev-parse --show-toplevel`
3. **Changed files list** — run `git diff --cached --name-only` in the repo dir (highest-value differentiator)
4. **New `gpg_sign` request type** in approval manager — minimal new fields on `Request`
5. **Daemon signs after approval** — calls real `gpg`, pipes result back to client
6. **`gpg-sign` CLI subcommand** — thin client, sends context, waits on WebSocket for result or denial
7. **CLI formatter extension** — handle `gpg_sign` type in `format.go`
8. **Web UI card** — display the signing context distinctly from secret access requests

Defer:
- Git tag signing (different object format — later milestone)
- Short parent hash display (nice but not critical for MVP)
- Key fingerprint display beyond what is already in the commit object (key ID is enough for MVP)

---

## Sources

- Git commit object format: [git-commit-tree(1)](https://git-scm.com/docs/git-commit-tree), [linux.die.net/man/1/git-commit-tree](https://linux.die.net/linux.die.net/man/1/git-commit-tree)
- git gpg.program interface (`-bsau <key-id>`, commit object on stdin): confirmed via git tracing and [how to understand gpg failed to sign the data](https://gist.github.com/paolocarrasco/18ca8fe6e63490ae1be23e84a7039374)
- GPG wrapper pattern (pass `"$@"`, intercept stdin): [atom/github gpg-wrapper.sh](https://github.com/atom/github/blob/master/bin/gpg-wrapper.sh), [romanz/gpg-git-wrapper](https://github.com/romanz/gpg-git-wrapper)
- "Clear signing" UX principle (show all fields in human-readable form before approval): [Ledger — What Is Clear Signing?](https://www.ledger.com/academy/topics/ledgersolutions/what-is-clear-signing), [Ledger — Messages, Transactions, and Clear-Signing](https://www.ledger.com/blog/securing-message-signing)
- Transaction verification / blind signing problem: [Blockaid — Transaction Verification: A Solution to Blind Signing](https://www.blockaid.io/blog/transaction-verification-a-solution-to-blind-signing-in-hardware-wallets)
- AI agent commit safety and parallel agent risks: [Zed discussion — AI agent accidentally making commits](https://github.com/zed-industries/zed/discussions/31762), [git worktrees for parallel AI agents](https://dev.to/mashrulhaque/git-worktrees-for-ai-coding-run-multiple-agents-in-parallel-3pgb)
- Existing codebase: `internal/approval/manager.go`, `internal/api/types.go`, `internal/cli/format.go`, `.planning/PROJECT.md`
