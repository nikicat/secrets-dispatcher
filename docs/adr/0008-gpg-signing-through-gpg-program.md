# ADR 0008: Gate git signing at `gpg.program`; the daemon runs gpg

Date: 2026-02-24 · Status: accepted

## Context

`git commit -S` hands gpg whatever bytes it is given, and gpg-agent signs them
with no human-visible context, so an agent or CI step can produce a commit
signed as the user that the user never reviewed. gpg-agent's own prompt is
pinentry, which shows the key, not the commit.

## Decision

- `secrets-dispatcher gpg-sign setup` points git's `gpg.program` at
  `secrets-dispatcher gpg-sign`, a thin client.
- For a signing call, the thin client parses the payload (commit, annotated tag
  or push certificate), collects context from the repository it runs in (repo
  name via `git rev-parse --show-toplevel`, staged files via
  `git diff --cached --name-only`), and submits it to the daemon over the local
  API socket with the master token ([ADR 0006](0006-api-auth-cookie-and-login-token.md)).
- The daemon shows the request, waits for a decision or a `trusted_signers`
  match, then **runs gpg itself** (`--status-fd=2 -bsau <key>`) and returns the
  signature and status lines, which the thin client writes back to git.
- Calls that are not signing (verification, `--list-keys`, …) pass straight
  through to the real gpg.
- `commit.gpgsign true` is recommended, so a plain `git commit` is gated too.

## Consequences

- The approval shows the repository, message and changed files, not a hash.
- Only git's signing goes through the dispatcher: a process that runs `gpg` or
  talks to gpg-agent directly is not gated ([ADR 0002](0002-visibility-not-isolation.md)).
- The thin client needs the API socket and token, so it works only where the
  daemon runs; remote signing needs [ADR 0011](0011-remote-signing-through-per-remote-sockets.md).
