---
status: todo
tags: [refactor, api, approval]
---
# Typed resolution in history entries

## Goal
`wsConnection.OnEvent` (`internal/api/websocket.go`) passes `"approved"`,
`"denied"` and the other results to `makeHistoryEntry` as string literals, and
`HistoryEntry.Resolution` is a bare `string`, while the live messages use
`approval.Resolution`. Done when history entries carry `approval.Resolution`
and no result is spelled as a literal.

Found by the 2026-11-08 unsmell run.
