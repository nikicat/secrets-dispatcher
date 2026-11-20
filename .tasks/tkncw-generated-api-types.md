---
status: todo
tags: [web, api]
---
# web/src/lib/types.ts mirrors the Go API types by hand

## Goal
Done when each item below is fixed as its "Fix" says.

- `internal/api/types.go` and `internal/sender/sender.go` (SenderInfo), `web/src/lib/types.ts:44-65` (split
  protocol). web/src/lib/types.ts mirrors every api DTO (PendingRequest,
  SenderInfo, HistoryEntry, WSMessage, …) by hand, so every JSON key is
  spelled once in a Go tag and again in TS; auto_approvable (x-1) is the
  newest. Unchanged lines apart from the one field.
  Fix: Generate types.ts (or a generated part of it) from the Go structs with
  a generator such as tygo, run by the existing make embed-frontend flow, so
  that each key has one spelling.

Found by the 2026-11-19 unsmell run (x-2).
