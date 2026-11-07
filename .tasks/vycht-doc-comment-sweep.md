---
status: todo
tags: [docs, refactor]
---
# Doc comment sweep

## Goal
- About 70 doc comments open with their own declaration's name, in `config`,
  `api`, `gpgsign`, `service` and `main.go`. The project's docs open with the
  verb ("Returns …", "Serves …").
- "runService handles the service subcommand group" sits above `runTry` in
  `main.go`, so `runService` has no doc.
- `gpgsign.SetupGitConfig` resolves a relative `remoteSocket` against the
  current directory at setup time, and its doc does not say so.

Done when all three are fixed in one docs-only change.

Found by the 2026-11-05 and 2026-11-07 unsmell runs.
