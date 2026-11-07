---
status: todo
tags: [refactor, cli]
---
# runServe stops with signal.NotifyContext

## Goal
`runServe` (`main.go`) hand-rolls a signal channel and a goroutine, where
`runTry` and `runDaemon` use `signal.NotifyContext`. Done when `runServe` does
the same.

## Related
T2nykk is the same fix in the gpg-sign thin client.

Found by the 2026-11-07 unsmell run.
