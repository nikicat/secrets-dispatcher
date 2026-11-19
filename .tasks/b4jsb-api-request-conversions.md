---
status: todo
tags: [refactor, api]
---
# api converts requests and items in several copies

## Goal
In `internal/api/handlers.go` the `ItemInfo` slice conversion is written
three times, a `PendingRequest` is built from an `approval.Request` twice
(`HandlePendingList`, `convertHistoryEntry`), and `HandleTestInjectHistory`
hand-writes the inverse conversion and drops `SystemdUnit` and `GPGSignInfo`.
Done when paired conversions live together with a round-trip test.

Found by the 2026-11-19 unsmell run. (b3-9, b3-10)
