---
status: todo
tags: [refactor, api, web]
---
# The commit subject is derived three times, and api has pass-throughs

## Goal
Done when each item below is fixed as its "Fix" says.

- `internal/approval/gpgsign.go` (GPGSignInfo.RequestInfo),
  `internal/api/gpgsign.go` signLogAttrs (misplaced rule).
  approval.GPGSignInfo.RequestInfo derives a commit's subject from the message
  (first line, cut at 60 bytes) for the item label, notification/desktop.go
  commitSubject derives the first line again, and api's signLogAttrs derives
  it a third time for the gpg-sign log lines. The cut at byte 60 can split a
  multi-byte UTF-8 rune, and the label goes to a desktop notification over
  D-Bus, which requires valid UTF-8 strings.
  Fix: approval.GPGSignInfo.Subject() returns the first line of the signed
  message. Shortening for display cuts at a rune boundary. RequestInfo,
  notification and signLogAttrs call it.
- `internal/approval/gpgsign.go` (GPGSignInfo.RequestInfo),
  `internal/notification/desktop.go` (commitSubject),
  `internal/cli/format.go` (commitSubject and commitBody),
  `web/src/lib/RequestCard.svelte` (commitSubject),
  `web/src/lib/HistoryEntry.svelte` (historyItemsSummary) (repeated
  derivation). A commit's subject (first line of the message) is derived in
  approval, notification, cli and twice in web. b3-8 reports the Go copies in
  approval and notification. cli cannot import approval, and the TS copies
  cannot call Go.
  Fix: As b3-8 (approval.GPGSignInfo.Subject()) for the Go copies in the
  daemon. The API then sends the subject (and the body, for cli's
  writeSignMessage), so that the cli and web copies go too.
- `internal/api/websocket.go:468-476` (pass-through). OnClientConnected and
  OnClientDisconnected (proxy.ClientObserver) each only call
  BroadcastClientConnected and BroadcastClientDisconnected. The Broadcast
  methods have no other callers outside tests.
  Fix: Put the broadcast bodies in OnClientConnected and OnClientDisconnected,
  and delete the Broadcast methods. The tests call the On methods.
- `internal/api/resolver.go:32-33` (narrating doc comment). Resolver.Approve's
  doc names no error (ErrNotFound, and Manager.ApproveGPGSign's errors for
  gpg_sign). It says the function "runs the real gpg binary to produce the
  signature", which is mechanism, the same sentence that b3-4 removes from
  ApproveAndAutoApprove.
  Fix: "Approves the pending request id. A gpg_sign request is approved with
  gpg's result, failure included. Errors with approval.ErrNotFound when no
  request with that id is pending."
- `internal/api/websocket.go` sendSnapshot, `internal/api/handlers.go`
  HandleAutoApproveList: the "a list encodes as [] not null" rule is written
  five times in two forms (`pendingRequests`/`historyEntries` and inline nil
  checks). Fix: Manager list methods that never return nil, or one `nonNil`
  helper in api.
- `internal/api/gpgsign_test.go` TestHandleGPGSignRequest_NoMatchingRuleStaysPending
  says its rule is "for a different invoker name", but rules match on exe and
  the httptest caller has none, so it repeats UnverifiableInvokerStaysPending.
  Fix: give the caller a different exe, or drop the test for the manager-level
  TestGPGSignEphemeralRuleMatchesOnInvokerExe.

Found by the 2026-11-19 unsmell run (b3-8, x-9, b3-9, b3-11, b3-19), and the
2026-11-21 23:24 run (b4-20, x-16).
