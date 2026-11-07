---
status: todo
tags: [external-pr, approval, web]
---
# Review PR #29: attribute policy decisions

## Goal
Decide on [#29](https://github.com/nikicat/secrets-dispatcher/pull/29) by
timrichardson. It records which rule resolved a request: a config rule, a
temporary rule, a trusted signer, or built-in policy. That attribution reaches
REST, WebSocket, the CLI, history and the web UI. It also logs the matching
rule and the request's context.

## State (2026-11-07)
- The PR opened on 2026-08-04 from a fork: +410/-77 in 21 files, 3 commits, no
  reviews.
- It conflicts with `master`. It predates the Prettier reformat, the Vite 8 and
  ES2025 move, and Go 1.27, and it carries an old embedded web bundle.
- PRs #30, #34, #35 and #36 build on it.

## Next
Review the design first. If it's wanted, ask the author to rebase, or rebase it
with their consent. The embedded bundle must be rebuilt (`make embed-frontend`).
