---
status: todo
tags: [external-pr, notification]
---
# Review PR #31: durable request details action on notifications

## Goal
Decide on [#31](https://github.com/nikicat/secrets-dispatcher/pull/31) by
timrichardson. Desktop approvals show the application, the request, the secret
and the process context. A Details action opens the request in the web UI
without resolving it. When GNOME dismisses the notification after that action,
the PR issues it again while the request is still pending.

## State (2026-11-07)
- The PR opened on 2026-08-04 from a fork: +348/-90 in 2 files, 1 commit, no
  reviews.
- It still merges cleanly into `master`, but CI last ran in August.
- It is related to Thaxg6. Both deal with a pending request whose notification
  disappears. Re-issuing on dismissal may cover part of Thaxg6's eviction
  problem, or conflict with its fix.

## Next
Review it alongside Thaxg6's design. Rerun CI on current `master` before merging.
