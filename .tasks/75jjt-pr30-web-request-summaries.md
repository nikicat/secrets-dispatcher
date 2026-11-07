---
status: todo
deps: [Twhz82]
tags: [external-pr, web]
---
# Review PR #30: align approval summaries across pending and history

## Goal
Decide on [#30](https://github.com/nikicat/secrets-dispatcher/pull/30) by
timrichardson. It adds one shared request overview for pending and historical
requests. The overview shows the application, the action, the secret and the
process chain, and tells commit, tag and push signatures apart. It never shows
secret values.

## State (2026-11-07)
- The PR opened on 2026-08-04 from a fork: +348/-97 in 12 files, 2 commits, no
  reviews.
- It conflicts with `master`. Since then, the web UI has been reformatted by
  Prettier and moved to Vite 8 and ES2025, and the test fixtures have been
  rewritten (`pendingRequest`, `routeSnapshot`). Its Playwright tests will need
  porting to those fixtures.
- It is stacked on #29 (Twhz82).

## Next
Review after Twhz82. Rebasing it needs the new test fixtures and a rebuilt
embedded bundle, plus a fresh `docs/screenshots/webui-overview.png`.
