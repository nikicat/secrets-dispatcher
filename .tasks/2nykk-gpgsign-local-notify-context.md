---
status: todo
tags: [refactor, gpgsign]
---
# Cancel the local signing path with signal.NotifyContext

## Goal
`runLocal` (`internal/gpgsign/run.go`) hand-rolls a goroutine that races a
signal channel against its context, where `runRemote` uses
`signal.NotifyContext`. Done when `runLocal` derives its context from
`NotifyContext` and sends the best-effort `CancelSigningRequest` when the
context ends by a signal.

## Related
T46zzr also touches `runLocal`. Tyw2nj is the same fix in `runServe`.

Found by the 2026-11-05 unsmell run.
