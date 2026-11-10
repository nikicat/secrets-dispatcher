---
status: todo
tags: [unsmell]
---
# runServe is 370 lines with an inline reconnect loop

## Goal
runServe is 370 lines mixing flag merging, logger setup, rule conversion, notifier wiring, a reconnect loop with backoff (inline retry machinery, main.go:463-500) and topology assembly; the changeset's two lines land in it.

Where: main.go:258-631.

Found by the /unsmell run of 2026-11-10 (b3-10).

## Next
- Extract the rule conversion (b3-6), logger setup (shared with runDaemon), the session-bus reconnect loop as a retry-with-backoff helper, and the per-downstream builders.
