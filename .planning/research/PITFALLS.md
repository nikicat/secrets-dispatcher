# Domain Pitfalls: GPG Commit Signing Proxy

**Domain:** GPG signing proxy / git gpg.program interceptor
**Researched:** 2026-02-24
**Confidence:** HIGH (git source code verified, gpg-agent behavior well-documented)

---

## Critical Pitfalls

Mistakes that cause silent failures, incorrect signatures, or complete commit breakage.

---

### Pitfall 1: Wrong Status-FD Direction Breaks Git's State Parsing

**What goes wrong:** The git source passes `--status-fd=2` (stderr), not `--status-fd=1`. If the proxy calls real gpg with `--status-fd=1` (stdout), the `[GNUPG:] SIG_CREATED` status line gets mixed into the signature output that git reads, corrupting the PGP block. Git never sees a valid signature. Signing silently fails with "gpg failed to sign the data."

**Why it happens:** The proxy naively re-invokes gpg with the same args git passed. Developers glance at docs and see status-fd is commonly `1` in verification commands, assume it's the same for signing, and miss that git deliberately separates the two streams.

**Consequences:** Mangled ASCII-armored signature. Git's `pipe_command` reads stdout expecting only the PGP block, finds garbage, rejects the commit object. The user sees a fatal commit error with no actionable message.

**Prevention:**
- Forward the exact args git passes verbatim to real gpg: `--status-fd=2 -bsau <keyid>`.
- Do not reconstruct the args from scratch — pass `os.Args[1:]` straight through.
- Add an integration test that calls the proxy the same way git does (pipe stdin, capture stdout, verify it parses as a PGP armored block).

**Detection:** Signature output contains `[GNUPG:]` lines mixed with PEM-like headers. Git log shows unsigned commit immediately after a "signed" commit.

**Phase:** Implementation of `gpg-sign` subcommand (daemon-side gpg execution).

---

### Pitfall 2: TTY / "Inappropriate ioctl for device" Breaks Passphrase Entry

**What goes wrong:** The daemon process has no controlling TTY. When real gpg needs a passphrase (cache miss in gpg-agent), pinentry tries to open `/dev/tty` and gets `ENOTTY` ("Inappropriate ioctl for device"). GPG exits non-zero. The signing call that the daemon makes on behalf of the user always fails unless the passphrase is already cached.

**Why it happens:** The daemon is started as a background process (systemd service or manual background fork). It inherits no TTY. GnuPG 2.1+ routes all passphrase prompts through the agent's pinentry program — which requires a TTY or a graphical session to display. The daemon has neither.

**Consequences:** Any signing attempt when the gpg-agent cache is cold (after reboot, after cache TTL) fails with a cryptic error. The user's git commit fails until they manually `gpg --sign` something in an interactive terminal to re-populate the agent cache.

**Prevention:**
- The daemon must set `GPG_TTY` when calling real gpg. Derive it from the *client* process's TTY (passed in the signing request), or use the user's active login session TTY.
- Alternatively, configure `pinentry-program` to `pinentry-curses` or `pinentry-gnome3` in gpg-agent.conf; these can display outside a subprocess context if a graphical session is available.
- For the daemon process itself, ensure it is started in a context where `$DISPLAY` or `$WAYLAND_DISPLAY` is available so graphical pinentry works.
- Document clearly: the daemon must run in the user's graphical session (not a root service), or the user must have passphrase cached.

**Detection:** `gpg: signing failed: Inappropriate ioctl for device` in daemon logs. Signing works after a manual interactive `gpg --sign` in a terminal.

**Phase:** Daemon-side gpg invocation. Must be addressed before any end-to-end testing.

---

### Pitfall 3: Daemon's gpg-agent Socket Not Inherited by Subprocess

**What goes wrong:** The daemon calls `exec.Command("gpg", ...)` but the subprocess cannot find the gpg-agent socket because the `GNUPG_HOME` or socket path environment variables are not set or differ from the user's login session. GPG falls back to launching a new agent (or fails with "no gpg-agent found"), which has no cached keys and no access to the user's keyring.

**Why it happens:** On modern Linux with systemd, the gpg-agent socket lives at `/run/user/<uid>/gnupg/S.gpg-agent`. The daemon process must have the correct `XDG_RUNTIME_DIR` and `GNUPGHOME` environment set. If the daemon was started by systemd without `--user` scope (or without `User=` in the unit), or with a scrubbed environment, these vars are absent.

**Consequences:** GPG invocations use a shadow agent with no keys. Signing fails with "secret key not available" or hangs waiting for a passphrase to a key that doesn't exist.

**Prevention:**
- Start the daemon with the user's full environment (`systemd --user` unit, or source `~/.profile`).
- Pass `GNUPGHOME`, `GPG_AGENT_INFO` (deprecated but still read), `XDG_RUNTIME_DIR`, `HOME`, and `USER` explicitly to the exec'd gpg subprocess.
- Test by running the daemon from a fresh shell session (not a terminal that already has the agent running), and verify signing still works.

**Detection:** `gpg: no secret key` or `gpg: agent: connecting agent failed` in daemon logs when running from a clean systemd service context.

**Phase:** Daemon startup and gpg subprocess invocation.

---

### Pitfall 4: Signature Delivered Over WebSocket Races with Git's stdin-Close

**What goes wrong:** Git writes the commit object to the proxy's stdin, then immediately waits for the proxy to exit and its stdout to close. If the proxy uses WebSocket to wait for the approval+signature, and the WebSocket message (carrying the completed signature bytes) arrives after git has already timed out or given up on the stdin write, the signature is lost and git sees an empty or truncated stdout.

**Why it happens:** The current WebSocket architecture delivers `request_resolved` events. For signing, the resolution event must carry the actual signature bytes — not just "approved." If the design only signals approval and then has the daemon re-send the signature via a separate channel, there's a window where git's read of the proxy's stdout completes before the signature arrives.

**Consequences:** Git receives zero bytes or a partial PGP block on stdout. Commit fails. No indication of which part failed.

**Prevention:**
- The WebSocket message that delivers the approval result for a signing request must carry the complete ASCII-armored signature as a field.
- The `gpg-sign` client must not close its stdout until it has received and written the full signature.
- Use a single, atomic message type: `signing_result` with fields `{ id, signature_b64 }` — not a two-step approve + fetch signature flow.
- Validate: after receiving the signature from WebSocket, write all bytes to stdout, flush, then exit 0.

**Detection:** Git reports "fatal: failed to write commit object" immediately after the approval UI shows the request was approved.

**Phase:** Protocol design for signing request/result flow (before implementation).

---

### Pitfall 5: Git Worktree Changes GIT_DIR, Breaking Working Directory Context

**What goes wrong:** The `gpg-sign` client needs to run `git diff --cached --name-only` to collect the list of changed files for the approval UI. In a normal repo, `cwd` is the worktree root. In a linked worktree, git sets `GIT_DIR` to the private worktree directory (e.g., `.git/worktrees/my-branch`), not the actual `.git`. Naive `git diff --cached` invocations fail or return results relative to the wrong working tree.

**Why it happens:** Git sets `GIT_DIR` in the subprocess environment when calling `gpg.program`. The `GIT_DIR` variable points to the private worktree metadata path, not the common `.git`. A `git diff` call that uses this `GIT_DIR` without also setting `GIT_WORK_TREE` will fail with "not a git repository" or diff against the wrong tree.

**Consequences:** The changed-files list is empty or wrong. The approval UI shows "0 files changed" for commits that affect dozens of files. Users lose context and may approve signing blindly — the opposite of the feature's purpose.

**Prevention:**
- Use `git rev-parse --show-toplevel` (respects worktrees) to find the working tree root, not `os.Getwd()`.
- When running `git diff --cached`, pass both `GIT_DIR` (from environment) and `GIT_WORK_TREE` (from `git rev-parse --show-toplevel`).
- Or: run `git -C <worktree-root> diff --cached --name-only` explicitly.
- Test with `git worktree add` before shipping.

**Detection:** Changed files list is always empty in worktree-based commits. Regular repo commits show files correctly.

**Phase:** `gpg-sign` client context-gathering logic.

---

### Pitfall 6: Commit Object Stdin Must Be Passed Binary-Safe — No String Conversion

**What goes wrong:** Git pipes the raw commit object bytes to `gpg.program` stdin. Commit messages can contain arbitrary bytes (non-UTF-8 encodings are valid). If the `gpg-sign` client reads stdin as a string (or through a text-mode reader), it may mangle non-ASCII content before forwarding to the daemon, and the daemon re-invokes real gpg with subtly different bytes. The signature is over different content than what git hashed. The commit object is written but verification fails ("BAD signature").

**Why it happens:** Go's `io.ReadAll(os.Stdin)` is binary-safe, but if any intermediate JSON serialization or HTTP body handling applies UTF-8 coercion or string normalization, bytes change. Base64 encoding is required for binary-safe JSON transport.

**Consequences:** Commit signs successfully (no error), but `git log --show-signature` shows `BAD signature`. The commit is written with an invalid signature. Users may not notice for days, then discover signed history that doesn't verify.

**Prevention:**
- Read stdin into `[]byte`, never into `string`.
- Transmit to daemon as base64-encoded field in JSON body.
- Daemon decodes base64 before passing to gpg stdin.
- Add a round-trip test: sign a commit object containing emoji, non-ASCII author names, and binary-looking bytes, then verify the signature.

**Detection:** `git log --show-signature` shows `BAD signature` on commits that appeared to sign successfully. Reproducible by committing a file with a non-UTF-8 filename.

**Phase:** `gpg-sign` client stdin handling and HTTP request construction.

---

## Moderate Pitfalls

---

### Pitfall 7: Exit Code Propagation — Proxy Must Relay gpg's Exit Code Exactly

**What goes wrong:** Git treats any non-zero exit from `gpg.program` as a signing failure and aborts the commit. If the proxy always exits 0 (e.g., "we sent the request, that's success from our perspective") even when the real gpg returned non-zero (key not found, expired key, agent error), git silently writes an unsigned commit or corrupts the commit object.

**Prevention:**
- The daemon must capture real gpg's exit code and transmit it back to the `gpg-sign` client.
- The `gpg-sign` client must `os.Exit(exitCode)` with the received code.
- If the daemon is unreachable, exit non-zero (not 0) so the commit is rejected rather than committed unsigned.
- Never swallow gpg errors.

**Detection:** `git verify-commit HEAD` fails on commits that appeared to succeed. The `gpg` field in `git cat-file -p HEAD` is present but contains a bad signature.

**Phase:** End-to-end exit code flow design.

---

### Pitfall 8: Daemon-Unreachable Fails Silently Instead of Hard-Failing

**What goes wrong:** If the `gpg-sign` client cannot connect to the daemon (daemon not running, wrong socket path), it exits 0 (or outputs nothing to stdout), causing git to write a commit with an empty or missing signature. With `commit.gpgsign=true`, git then fails at the commit-object-write stage with a confusing error, or worse, silently commits unsigned.

**Prevention:**
- If daemon connection fails, `gpg-sign` must exit non-zero immediately with a clear message to stderr: `secrets-dispatcher daemon not running — refusing to sign`.
- Never fall back to calling real gpg directly (that defeats the purpose of the proxy).
- Consider a startup check: if `gpg.program` is set to `secrets-dispatcher gpg-sign`, verify the daemon is running and warn loudly at shell startup or via a daemon health check.

**Detection:** Commits succeed when daemon is stopped. `git log --show-signature` shows unsigned commits in history.

**Phase:** `gpg-sign` client startup and connection error handling.

---

### Pitfall 9: Approval Manager Type Collision — GPG Requests Appear as Secret Requests

**What goes wrong:** The existing `RequestType` is `"get_secret"` or `"search"`. Adding a GPG signing request as a new type requires updating every place that renders or filters requests: the web UI, the CLI `list` and `show` commands, desktop notifications, and the history log. If `RequestType` is added but rendering code is not updated, signing requests show as blank or crash the UI.

**Prevention:**
- Add `RequestTypeGPGSign RequestType = "gpg_sign"` in the `approval` package first.
- Audit all switch/case statements on `RequestType` in handlers, CLI format functions, and web UI JS — add explicit cases before shipping.
- Add a test that creates a `gpg_sign` request and verifies all API endpoints return it with correct type and all fields present.

**Detection:** GPG signing requests appear in the UI with no context (empty fields), or the CLI panics on `switch req.Type`.

**Phase:** Approval manager extension and UI update.

---

### Pitfall 10: Signature Size Assumptions — WebSocket Message Size Limit

**What goes wrong:** The existing WebSocket handler has `maxMessageSize = 512` bytes, used for *inbound* client messages. The signing result message carrying the ASCII-armored signature back to the `gpg-sign` client may be several hundred bytes (a 4096-bit RSA signature armor block is ~700 bytes). If the wrong limit applies to outbound messages, the signature is truncated.

**Prevention:**
- Verify that `maxMessageSize` applies only to reads (inbound), not writes (outbound). In the current implementation (`conn.SetReadLimit(maxMessageSize)`), this is read-only — confirm and document explicitly.
- For the `gpg-sign` client's WebSocket connection back to the daemon, set no read limit or a large one (`conn.SetReadLimit(65536)`) since it reads potentially large signing result messages.
- Test with 4096-bit and 8192-bit RSA keys; ed25519 signatures are smaller but RSA is common in the wild.

**Detection:** Signature received by client is truncated at a suspiciously round byte count. `gpg --verify` reports malformed data.

**Phase:** WebSocket result delivery implementation.

---

## Minor Pitfalls

---

### Pitfall 11: gpg-agent Cache TTL Mismatch with Approval Timeout

**What goes wrong:** The approval manager has a configurable timeout (default unclear from codebase, likely minutes). The gpg-agent passphrase cache TTL may be much shorter (default 600 seconds). If a user approves a signing request but the agent cache has expired in the interim, real gpg will prompt for a passphrase — in the daemon context (no TTY), this fails silently.

**Prevention:**
- Set `default-cache-ttl` in `~/.gnupg/gpg-agent.conf` to at least 2x the approval timeout.
- Document this requirement prominently in setup instructions.

**Phase:** Documentation and setup guide.

---

### Pitfall 12: Concurrent Signing Requests — Multiple Claude Sessions Simultaneously

**What goes wrong:** This feature exists specifically to handle multiple concurrent Claude Code sessions. If two sessions commit simultaneously, two signing requests arrive. The approval UI must make clear which commit is which (repo, message, author). Without per-request uniqueness, the user might approve request B thinking it's request A.

**Prevention:**
- Signing requests must display repository path (absolute, not basename — two sessions can have same repo name), commit message, and a git short-SHA of the current HEAD.
- The web UI and CLI must never batch or conflate simultaneous signing requests.
- Each request gets its own ID, same as current approval flow.

**Detection:** Approving request A actually causes request B's commit to be signed.

**Phase:** Signing request context collection and UI display.

---

### Pitfall 13: Git Rebase / Cherry-pick Generates Multiple Signing Calls

**What goes wrong:** `git rebase` with `commit.gpgsign=true` calls `gpg.program` once per commit being replayed. A rebase of 20 commits generates 20 signing requests in rapid succession. Each one blocks until approved. The user is presented with 20 approval dialogs.

**Prevention:**
- This is expected behavior, not a bug, but must be documented.
- The approval UI should clearly indicate "1 of N pending" when multiple signing requests queue up.
- Consider a "bulk approve" option for rebase scenarios where all requests have the same author and sequential timestamps.

**Phase:** UI design — low priority but worth noting before launch.

---

## Phase-Specific Warnings

| Phase Topic | Likely Pitfall | Mitigation |
|-------------|---------------|------------|
| `gpg-sign` subcommand argument forwarding | Reconstructing gpg args instead of forwarding verbatim (Pitfall 1) | Pass `os.Args[1:]` to real gpg; never reconstruct |
| Daemon-side gpg invocation | TTY/pinentry failure (Pitfall 2), socket not found (Pitfall 3) | Set env vars explicitly; test from clean systemd context |
| Stdin reading and HTTP transport | Binary corruption (Pitfall 6) | Use `[]byte` + base64 end-to-end |
| WebSocket result delivery protocol | Race with git's stdout read (Pitfall 4), message size (Pitfall 10) | Atomic message with embedded signature; verify read limit is inbound-only |
| Approval manager extension | Type collision / unhandled cases (Pitfall 9) | Grep all `RequestType` switch statements before shipping |
| Working directory / context gathering | Worktree GIT_DIR issue (Pitfall 5) | Use `git rev-parse --show-toplevel`; test with worktrees |
| Exit code handling | Silent unsigned commits (Pitfalls 7, 8) | Propagate exact exit codes; hard-fail when daemon unreachable |

---

## Sources

- Git source: [git/git — gpg-interface.c](https://github.com/git/git/blob/master/gpg-interface.c) — confirmed `--status-fd=2` and signing argument structure (HIGH confidence)
- [gitformat-signature(5)](https://git.github.io/htmldocs/gitformat-signature.html) — PGP armor format expectations (HIGH confidence)
- GnuPG issue tracker: [T5885 — "Inappropriate ioctl" tty error](https://dev.gnupg.org/T5885) (HIGH confidence)
- [Fixing GPG "Inappropriate ioctl for device" — Daniel15](https://d.sb/2016/11/gpg-inappropriate-ioctl-for-device-errors) (MEDIUM confidence)
- GitHub: [gpg wrapper fails in git worktree — opentimestamps/opentimestamps-client #87](https://github.com/opentimestamps/opentimestamps-client/issues/87) — worktree GIT_DIR pitfall (MEDIUM confidence)
- Go stdlib: [os/exec data race StdinPipe and Wait #9307](https://github.com/golang/go/issues/9307) — pipe closure ordering (HIGH confidence)
- GnuPG: [Agent Forwarding wiki](https://wiki.gnupg.org/AgentForwarding) — socket environment inheritance (HIGH confidence)
- GnuPG documentation: [Common Problems](https://www.gnupg.org/documentation/manuals/gnupg/Common-Problems.html) — GPG_TTY requirements (HIGH confidence)
