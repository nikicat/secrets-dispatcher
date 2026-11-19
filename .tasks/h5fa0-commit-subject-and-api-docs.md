---
status: todo
tags: [refactor, api, web]
---
# The commit subject is derived three times, and api has pass-throughs

## Goal
Done when each item below is fixed as its "Fix" says.

- `internal/api/resolver.go:117-132` (misplaced rule). gpgSignItems derives a
  commit's subject from approval.GPGSignInfo.CommitMsg (first line, cut at 60
  bytes) in api, and notification/desktop.go:645 commitSubject derives the
  first line again. The cut at byte 60 can split a multi-byte UTF-8 rune, and
  the label goes to a desktop notification over D-Bus, which requires valid
  UTF-8 strings. `if i := len(subject); i > 60` binds a variable it never
  uses.
  Fix: approval.GPGSignInfo.Subject() returns the first line of CommitMsg.
  Shortening for display cuts at a rune boundary. Both sites call it.
- `internal/api/resolver.go:117-132`,
  `internal/notification/desktop.go:645-650`,
  `web/src/lib/RequestCard.svelte:55-57` (repeated derivation). A commit's
  subject (first line of the message) is derived in api (gpgSignItems),
  notification (commitSubject) and web (RequestCard's commitSubject). b3-8
  reports the two Go copies; the TS copy is a third. All unchanged.
  Fix: As b3-8 (approval.GPGSignInfo.Subject()); the plan entry also names the
  TS copy, which the API could make redundant by sending the subject.
- `internal/api/handlers.go:71-82`, `internal/api/websocket.go:276-282`
  (missing receiver). Handlers and WSHandler both hold manager,
  clientProvider, auth, remoteSocket and clientName. Each writes the
  single-socket rule "no provider means one static client {clientName,
  remoteSocket}" (HandleStatus, sendSnapshot).
  Fix: One client source that both hold, which returns []proxy.ClientInfo. A
  static provider is built in main for single-socket mode, so clientName,
  remoteSocket and the nil check leave both handlers. HandleStatus keeps the
  deprecated fields from that source.
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
