---
status: todo
tags: [unsmell, refactor]
---
# Leftovers of the 2026-11-21 23:24 unsmell run

## Goal
Done when each item below is fixed as its "Fix" says. Items that extend an
existing task (6qbjt, 9pz0t, gm1ah, h5fa0, nhmks, vf553) were added to that
task, not here.

## State
The 2026-11-22 batch fixed every other entry. Left: b4-15 and b9-13 wait on
the user (body bound; reasons for the passthrough and Chrome-dummy rules),
r1-17 waits on 9pz0t, b8-19 on vf553 and 1m5wn; the rest are redesigns.

### gpgsign

- `internal/gpgsign/outcome.go` SignOutcome and SignOutcome.check,
  `internal/approval/manager.go` Request.GPGResult,
  `internal/approval/gpgsign.go` HistoryEntry.GPGOutput (algebraic
  blindness). Every approval.Request carries a `GPGResult GPGResult` field
  that means something only for an approved or auto-approved gpg_sign
  request, and its doc says "HistoryEntry.GPGOutput says when it is set".
  GPGOutput re-derives that from Type and Resolution. gpgsign.SignOutcome
  pairs `Result Resolution` with `*GPGResult`, its doc points at GPGOutput
  again, and check() rejects an approving result without gpg output at run
  time. One fact (a result is present exactly when a gpg_sign request was
  approved) is stated three times and enforced by a run-time check, because
  no type carries it.
  Fix: On the daemon side, Request loses its GPGResult field. ApproveGPGSign
  and RecordAutoApprovedGPGSign store the result in the settled History entry
  (an unexported `gpgResult *GPGResult` set only by those two), and
  GPGOutput() reads it without re-deriving. On the client side, SignOutcome
  becomes a sum decoded by UnmarshalJSON into either Signed{GPGResult} (for a
  result that approves) or Refused{Resolution}. That replaces check() and the
  embedded pointer, and report() switches on the variant. Land it together
  with the GPGResult entry below, which rebuilds the same type. (s1-1)
- `internal/approval/gpgsign.go` GPGResult, GPGExitCode, GPGNotStarted and
  GPGNotFound; `internal/api/gpgsign.go` GPGRunner.Sign and
  defaultGPGRunner.Sign (algebraic blindness). GPGExitCode is "the gpg
  process's own code, or one of the codes below when gpg did not run".
  GPGNotStarted = 1 collides with gpg's own failure code 1, so a result
  cannot say whether gpg ran. GPGResult's "Sent even when zero" field comment
  compensates on the same type. GPGRunner.Sign returns both an error and a
  meaningful result when gpg cannot run, and its doc has to say which
  ExitCode the result then carries.
  Fix: GPGResult becomes a sum, Ran{Signature, Status, ExitCode} or
  NotRun{Reason} (not found, not started), with a MarshalJSON that keeps the
  exit_code wire key. Succeeded and the thin client's emit switch on it.
  GPGRunner.Sign returns an error type that carries the exit code to report
  (or the NotRun reason), so its doc no longer describes the result that comes
  with the error. Land it with the SignOutcome entry above. (s1-10,
  u-longdocs)

### approval

- `internal/approval/signedobject.go` SignedObject.Parse and maxSignedLine
  (inline generic machinery). Parse runs a bufio.Scanner, with a line bound of
  maxSignedLine (1 MiB), over bytes that are already in memory, only to split
  the header block from the body at the first empty line. The bound is the
  only reason Parse can fail with something other than ErrNotSignable.
  Fix: Split with bytes.Cut(o, []byte("\n\n")) and strings.Split for the
  headers. maxSignedLine, its error, and the oversized-line cases in
  `internal/approval/signedobject_test.go`,
  `internal/approval/gpgsign_json_test.go` and `internal/api/gpgsign_test.go`
  (TestHandleGPGSignRequest_OversizedLineRejected) go. This changes
  behaviour: a long line is then accepted. Decide first whether the API should
  bound the request body instead, since nothing else limits it. (b4-15)
- `internal/api/gpgsign.go` signAndRecordAutoApproved and signLogAttrs,
  `internal/api/resolver.go` Resolver.approveGPGSign (data clump).
  signAndRecordAutoApproved(w, sign, request, reason) and approveGPGSign(req,
  sign) each take a gpg_sign request and its own payload as two parameters
  (sign is the request's Payload), so the pair can disagree. signLogAttrs(sign,
  sender) carries the other half of the same pair. This shape was chosen to
  avoid `sign, _ := request.GPGSign()`.
  Fix: A typed gpg_sign request in approval, for example
  GPGSignRequest{*GPGSignInfo; SenderInfo} with RequestInfo() and LogAttrs()
  methods, that the handlers build once and pass alone. Do it after 9pz0t,
  which derives Type from Payload. (r1-17)

### sender

- `internal/sender/sender.go` Info, `internal/proxy/senderinfo.go`
  SenderInfoResolver.Resolve (algebraic blindness). Resolve's doc says it
  returns "what the bus reports about the sender", but on a local bus with a
  /proc chain it replaces info.PID with the invoker's PID. sender.Info.PID's
  doc says "The PID of the invoker (procutil.Invoker)", which is false on a
  forwarded bus or when /proc has no chain: the field then holds the D-Bus
  connection's PID. One field holds two kinds of PID. Separately, Resolve
  leaves a field at its zero value when the bus query for it fails, and only
  UID (a pointer) can say "not resolved"; a zero PID, an empty InvokerName or
  an empty SystemdUnit cannot be told apart from a real value.
  Fix: sender.Info keeps the connection's PID and the invoker separately (for
  example ConnectionPID plus an optional Invoker{PID, Name}), so that the
  process matchers and the UI read the one they mean. The fields that a query
  can fail to fill say so in their types (optional values), and Resolve's doc
  no longer has to explain partial results. (rc3-d-20, u-longdocs)

### notification

- `internal/notification/desktop.go` (long file). desktop.go holds three
  concepts in about 750 lines: the D-Bus notifier (connection, signals,
  reconnect), the approval Handler (tracking, actions, follow-ups) and the
  markup body formatting.
  Fix: Split by concept into dbus_notifier.go (DBusNotifier, Action, Closed),
  handler.go (Handler and its actions) and body.go (escapeMarkup, formatBody,
  senderHeader, formatProcessChain, commitSubject). Best done after vf553 and
  1m5wn, which rewrite much of the same code. (b8-19)

### proxy

- `internal/proxy/handler.go` caller.passthrough,
  `internal/approval/manager.go` Manager.Passthrough and
  Manager.dropsChromeDummyWrite (restated code). Two decisions in the proxy
  are recorded in no design doc. SearchItems (on the service and on a
  collection) and Unlock go upstream without a prompt while deny and ignore
  rules still apply, but ADR 0004 says every access is a separate request,
  and ADR 0005, which Manager.Passthrough cites, does not cover this. Chrome
  dummy-schema writes are dropped without asking, and nothing cites a reason.
  Fix: Record both decisions in ADR 0004 (or a new ADR), then link the
  passthrough and Chrome-dummy docs to them. The reasons have to come from the
  user. (b9-13)
- `internal/proxy/collection.go` CollectionHandler.Delete, SearchItems and
  CreateItem; `internal/proxy/item.go` ItemHandler.Delete, GetSecret and
  SetSecret; `internal/proxy/service.go` Service.GetSecrets;
  `internal/logging/audit.go` Logger.logMethod and Logger.LogCreateItem
  (concept mixing, algebraic blindness). ADR 0004 says every attempt is
  logged, but CollectionHandler.Delete, SearchItems and CreateItem and
  ItemHandler.Delete and SetSecret return upstream failures (and SearchItems
  its denial) without an audit line. GetSecret and GetSecrets instead write
  the same log call at every exit. The logging is written per exit, not once
  per call. In the logger, every Log* method takes `result Result, err error`
  side by side, though err goes only with denied or error, and LogCreateItem
  (for a CreateItem call that created no item) still accepts ResultOK.
  Fix: One outcome type that the proxy builds once per call (ok, ignored,
  denied with its error, error with its error), which logMethod renders. Each
  D-Bus method is split into an inner function that returns its result and
  error, and the method logs once from that outcome. LogCreateItem then takes
  only a failure outcome. (b9-22, rc2-b-8)
- `internal/proxy/slow_upstream.go` UpstreamCallContext, RequestCall,
  PromptedCall, SenderCall and ResolvedSenderCall (algebraic blindness).
  UpstreamCallContext holds three shapes in one struct: a request call, a
  prompted request call (prompted true) and a sender-only call whose sender is
  resolved lazily (resolveSender set, request empty). ResolvedSenderCall fakes
  a fourth as a RequestInfo that holds only a sender, although its doc says
  the call serves no approval request. WithSlowNotify branches on
  resolveSender != nil.
  Fix: An interface with named implementations (a request call that knows
  whether it was prompted, and a sender call with a lazy or resolved sender),
  each returning the RequestInfo to show and whether SlowUpstream reports it.
  (rc2-d-15)

Found by the 2026-11-21 23:24 unsmell run (b1-12, b1-13, b2-15, b2-16, b2-17,
b2-18, x-15, rc3-a-14, b4-22, b4-23, b4-25, b7-8, rc2-c-6, x-2, s1-8, s1-1,
s1-10, b5-15, x-12, rc3-b-7, b4-15, r1-17, rc3-d-20, b6-8, b8-16, b8-17, b8-19,
b9-13, b9-21, b9-22, rc2-b-8, b9-23, b9-24, b10-14, rc2-d-14, b10-15, rc2-d-15,
rc3-d-21, plus the user's Manager.List review note and the r3-collection and
u-longdocs fix notes). Also planned by this run in other tasks: b1-11 (gm1ah),
b4-16 (6qbjt), b4-20 and x-16 (h5fa0), b4-24 and rc2-c-13 (x9yi5), rc2-c-12
(vf553), rc3-c-13 (nhmks), s1-6 and s1-7 (9pz0t). s1-16 is covered by hf9n2
and v6m33.
