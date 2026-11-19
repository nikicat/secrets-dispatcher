---
status: todo
tags: [refactor, api]
---
# api converts requests and items in several copies

## Goal
Every `PendingRequest` is now built by `convertRequest` (`websocket.go`), but
`HandleTestInjectHistory` (`handlers.go`) hand-writes the inverse conversion and
drops `SystemdUnit` and `GPGSignInfo`. Done when paired conversions live
together with a round-trip test.

Found by the 2026-11-19 unsmell run. (b3-9, b3-10)
