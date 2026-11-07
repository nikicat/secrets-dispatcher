---
status: todo
deps: [Twhz82]
tags: [external-pr, approval, web, security]
---
# Review PR #34: persist exact saved approvals

## Goal
Decide on [#34](https://github.com/nikicat/secrets-dispatcher/pull/34) by
timrichardson. It turns a manually approved `get_secret` request in history
into a durable, approve-only grant:
- The grant binds the kernel-resolved executable, the collection and every
  observed attribute. For an interpreter, it also binds the script and its
  working directory.
- Grants are stored in `state_dir/approval-rules.json` with owner-only
  permissions.
- Config deny and ignore rules take precedence over every grant.
- It adds list, create and delete APIs, WebSocket events and a web UI to manage
  the grants.

## State (2026-11-07)
- The PR opened on 2026-08-04 from a fork: +2487/-116 in 28 files, 3 commits,
  no reviews.
- It conflicts with `master`, and it is stacked on #29 (Twhz82).
- It overlaps T7k9fb, which plans durable rules from a request's actions. This
  PR is one concrete design for part of that task.

## Next
Review it against T7k9fb and ADR 0005 (trust rules on the process chain). The
security model needs a careful read before any merge. Then decide whether it
becomes T7k9fb's implementation, and who rebases it.
