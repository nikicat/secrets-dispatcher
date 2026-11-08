---
status: todo
tags: [web, notification, approval, bug]
---
# Don't offer auto-approve on remote requests

## Goal
The web UI (`web/src/lib/RequestCard.svelte`) and the desktop notification
(`internal/notification/desktop.go`) offer "approve and auto-approve" on
`remote:<name>` requests. The rule that `Manager.AddAutoApproveRule` creates has
an empty `InvokerExe`, so it can never match: the offer does nothing. Done when
neither surface offers it for a request without a local exe, and
`AddAutoApproveRule` refuses such a request.

## Next
Hide the action in both places, make `AddAutoApproveRule` return an error for an
empty exe, and rebuild the embedded web bundle (`make embed-frontend`).

Found by the 2026-11-05 unsmell run.
