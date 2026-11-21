---
status: todo
tags: [refactor, api, web]
---
# The commit subject is derived three times, and api has pass-throughs

## Goal
Done when each item below is fixed as its "Fix" says.

- `internal/approval/gpgsign.go:104-122`, `internal/api/gpgsign.go:70-73`,
  `internal/api/gpgsign.go:135` (misplaced rule).
  approval.GPGSignInfo.RequestInfo derives a commit's subject from CommitMsg
  (first line, cut at 60 bytes) for the item label, and
  notification/desktop.go:645 commitSubject derives the first line again.
  HandleGPGSignRequest derives it a third time as commitSubject and passes it
  to signAndRecordAutoApproved beside the request, whose commitSubject
  parameter is a further copy. The cut at byte 60 can split a multi-byte
  UTF-8 rune, and the label goes to a desktop notification over D-Bus, which
  requires valid UTF-8 strings. `if i := len(subject); i > 60` binds a
  variable it never uses.
  Fix: approval.GPGSignInfo.Subject() returns the first line of CommitMsg.
  Shortening for display cuts at a rune boundary. RequestInfo and
  notification call it. It replaces the handler's commitSubject, and
  signAndRecordAutoApproved loses its commitSubject parameter.
- `internal/approval/gpgsign.go:104-122`,
  `internal/notification/desktop.go:645-650`,
  `web/src/lib/RequestCard.svelte:55-57` (repeated derivation). A commit's
  subject (first line of the message) is derived in approval
  (GPGSignInfo.RequestInfo), notification (commitSubject) and web
  (RequestCard's commitSubject). b3-8 reports the two Go copies; the TS copy
  is a third. All unchanged.
  Fix: As b3-8 (approval.GPGSignInfo.Subject()); the plan entry also names the
  TS copy, which the API could make redundant by sending the subject.
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

Found by the 2026-11-19 unsmell run (b3-8, x-9, b3-9, b3-11, b3-19).
