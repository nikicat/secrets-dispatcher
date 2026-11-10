---
status: todo
tags: [unsmell]
---
# The Request literal is written six times in approval/manager.go

## Goal
The Request literal (uuid ID, client, items, session, now, type, search attributes, sender) is written six times: three in RequireApproval and once each in RecordPassthrough, RecordIgnored, RecordDenied. Unchanged lines.

Where: internal/approval/manager.go:362-416, internal/approval/manager.go:1141-1185.

Found by the /unsmell run of 2026-11-10 (b1-12).

## Next
- One newRequest(...) constructor (or the RequestInfo of b1-6 with a stamp method); ExpiresAt is the only field that differs.
