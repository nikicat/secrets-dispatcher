---
status: todo
tags: [unsmell, refactor]
---
# Leftovers of the 2026-11-21 23:24 unsmell run

## Goal
Done when each item below is fixed as its "Fix" says. Items that extend an
existing task (6qbjt, 9pz0t, gm1ah, h5fa0, nhmks, vf553, x9yi5) were added to
that task, not here.

### api

- `internal/api/gpgsign_test.go` TestGPGSignEphemeralRuleMatchesOnInvokerExe
  (misplaced rule). The test exercises only approval.Manager (mustAddRule and
  CheckAutoApproveRules) from the api package. The same exe-over-comm rule is
  already tested for get_secret by TestAutoApproveRule_CommSpoofDoesNotMatch
  in `internal/approval/autoapprove_test.go`.
  Fix: Move the test into internal/approval/autoapprove_test.go as a gpg_sign
  case of TestAutoApproveRule_CommSpoofDoesNotMatch. h5fa0's last item points
  at this test, so update it to the new name. (b1-12)
- `internal/api/resolver.go` Resolver.ApproveAndAutoApprove (garbled
  sentence). Its doc says "Approves the pending request id", which uses the
  parameter name as a noun.
  Fix: "Approves the pending request with the given ID and adds an
  auto-approve rule, ...". (b1-13)
- `internal/api/resolver.go` Resolver.AutoApprove (misleading name). The
  method does not approve anything: it adds an auto-approve rule made from a
  request that was cancelled. The notification Approver interface
  (`internal/notification/desktop.go`, Approver) and the "auto_approve"
  notification action use the same name, and it reads like a sibling of
  Approve and ApproveAndAutoApprove, which do approve.
  Fix: Rename it after what it does, for example AddRuleFromCancelled, in
  Resolver, the notification Approver interface and its mock, and the api
  handler that calls it. The action key and the API route can keep their wire
  names. (u-longdocs)
- `internal/api/websocket.go` wsConnection.writePump (speculative
  generality). writePump receives with `message, ok := <-wsc.send` and returns
  when `!ok`. Nothing ever closes wsc.send (grep for `close(wsc.send` finds
  nothing), so that branch is dead.
  Fix: Receive with `case message := <-wsc.send:` and drop the branch.
  (b2-15)
- `internal/api/websocket.go` wsConnection.sendSnapshot and the two write
  cases of wsConnection.writePump (repeated derivation). Three blocks bound one
  write to the peer in the same way: `context.WithTimeout(wsc.ctx,
  writeWait)`, one conn call (Write or Ping), then cancel. The two in
  writePump also log the error unless isConnClosedErr says the connection was
  torn down.
  Fix: A method wsConnection.write(op func(ctx context.Context) error, what
  string) error that owns the deadline and the teardown check. writePump's two
  cases and sendSnapshot call it. (b2-16)
- `internal/api/websocket.go` WSHandler.HandleWS and wsConnection.close
  (misplaced rule). Registering a connection is written inline in HandleWS
  (h.connsMu, h.conns, h.manager.Subscribe). Unregistering is written in
  wsConnection.close, which reaches into handler.manager, handler.connsMu and
  handler.conns more than it uses its own fields.
  Fix: WSHandler.attach(wsc) and WSHandler.detach(wsc) own the map, the lock
  and the Subscribe and Unsubscribe calls. HandleWS calls attach, and close
  calls wsc.handler.detach(wsc). (b2-17)

### web

- `web/src/lib/types.ts` WSRequestResolvedMessage and WSPingMessage,
  `web/src/lib/websocket.ts` the onRequestResolved callback (algebraic
  blindness). WSRequestResolvedMessage.result is typed "approved" | "denied",
  but api's endedMessages (`internal/api/websocket.go`) also sends
  request_resolved with "auto_approved" for a gpg_sign auto-approval.
  websocket.ts's onRequestResolved repeats the narrow union. WSPingMessage is a
  member of the WSMessage union, but the daemon never sends a "ping" message:
  pings are WebSocket control frames.
  Fix: Type result as Resolution (or an Extract of it that includes
  "auto_approved"), in both places, and delete WSPingMessage. tkncw's
  generated types would cover the keys but not this literal union. (b2-18)
- `web/src/lib/HistoryEntry.svelte` extractCollection and hasMatchingRule,
  `internal/dbus/types.go` CollectionOf (split protocol). The web's
  extractCollection spells the collection and alias path prefixes and the
  split rule again. Package dbus owns them in CollectionsPath, AliasesPath and
  CollectionOf.
  Fix: The API sends each item's collection as a field of api.ItemInfo
  (`internal/api/types.go`), computed with dbustypes.CollectionOf in
  convertRequest, and the web reads that field instead of parsing the path.
  extractCollection goes. (x-15)
- `web/tests/fixtures/test-utils.mts` createGPGSignRequest and
  buildCommitObject (unparsed input). Both take `info: Record<string,
  unknown>` and read parent_hash, author, committer and commit_msg by key with
  typeof checks. Nothing says which keys the daemon reads (commit_object,
  repo_name, key_id, changed_files, fingerprint) or which ones feed the built
  commit object, which is how dead keys got into callers before.
  Fix: A `CommitFields` type (repo_name, key_id, changed_files, fingerprint?,
  parent_hash?, author, committer, commit_msg) for both functions. The typeof
  checks go. (rc3-a-14)

### cli

- `internal/cli/format.go` writeExpiry and formatRemaining (repeated
  derivation). writeExpiry repeats formatRemaining's
  `time.Until(...).Round(time.Second)` and its check that the request has
  expired.
  Fix: writeExpiry prints `Expires: <time> (<formatRemaining> remaining)`, or
  `(expired)`, from formatRemaining's result, so the derivation is written
  once. (b4-22)
- `internal/cli/format.go` writeRequest (long function). writeRequest is
  about 105 lines and writes the request header, the sender, the gpg_sign
  object of each kind (with an inline comment per section), the items and the
  times in one body.
  Fix: Split it into writeSender, writeSignInfo (one function per sign kind),
  writeItems and writeTimes, so that writeRequest reads as its steps. (b4-23)
- `internal/cli/format.go` requestSummary and requestCollection (missing
  receiver). Both are free functions whose only input is a cli RequestRecord
  (`internal/cli/client.go`), which they only read. entrySummary exists only
  to call requestSummary on e.Record().
  Fix: Make them methods RequestRecord.Summary() and
  RequestRecord.Collection(). entrySummary calls e.Record().Summary() or goes.
  (b4-25)

### gpgsign

- `internal/gpgsign/daemon.go` pendingSignRequest.Cancel and
  DaemonClient.CancelSigningRequest (duplicated knowledge). Cancel's doc
  ("Errors when the daemon does not answer or does not find it") restates
  CancelSigningRequest's error contract in other words. The two copies will
  drift.
  Fix: Cancel's doc becomes "Cancels the request on the daemon. Errors as
  DaemonClient.CancelSigningRequest does." (b7-8)
- `internal/gpgsign/daemon.go` (SocketURL, socketWSURL, UnixSocketClient,
  NewDaemonClient, PostSigningRequest, CancelSigningRequest,
  startSignRequest), `internal/gpgsign/remote.go` (SignRemote),
  `internal/gpgsign/run.go` (thinClient.sign, thinClient.signLocal) (garbled
  sentence). Ten docs use parameter names as nouns: "of path", "at
  socketPath", "Files info", "with requestID", "Submits info", "Signs info".
  Fix: Say "the given path", "the given socket", "the given sign info", "the
  pending request with the given ID", or restructure the sentence. (rc2-c-6)
- `internal/cli/client.go` Client, Client.do and Client.Resolve;
  `internal/gpgsign/daemon.go` socketClient (with do and postEmpty) and
  DaemonClient.CancelSigningRequest (missing receiver). cli.Client and
  gpgsign's socketClient are two clients of the same daemon API with the same
  state: a base URL (or socket), a credential (config.AuthToken, whose
  Authorize method both call) and an *http.Client. Both write the same call,
  POST /api/v1/pending/{id}/{action} followed by a 200 check: cli in
  Client.Resolve, gpgsign in CancelSigningRequest through postEmpty. Only the
  transport differs (TCP for cli, the unix socket for gpgsign) and gpgsign's
  remote socket sends no credential (remoteSocketIdentity in daemon.go).
  Fix: One authenticated API client type in a shared package, for example
  internal/apiclient.Client{base URL, credential, *http.Client}, with Do,
  PostJSON and PostAction(id, action) methods, and the 200 check in one
  place. cli.NewClient builds it over TCP, NewDaemonClient and SignRemote
  build it over UnixSocketClient. Client.Resolve and CancelSigningRequest call
  PostAction. The route strings move to hf9n2's shared constants. (x-2)
- `internal/gpgsign/daemon.go` wsMsg and WaitForResolution (algebraic
  blindness). wsMsg flattens every WebSocket message into {Header, ID
  RequestID, SignOutcome}, so the ID's meaning depends on Type. Its comment
  compensates: "A removed auto-approve rule's message carries the rule's id
  here, which matches no request." api's WSMessage is already a sum type
  (`internal/api/websocket.go`), but this reader is not.
  Fix: Decode the envelope's type first, and decode the payload only for the
  request messages that WaitForResolution reads (resolved, expired and
  cancelled carry a RequestID). Other types are skipped without reading an
  id, and the comment goes. Lands with the AutoApproveRuleID entry below.
  (s1-8)
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

- `internal/approval/manager.go` Manager.List (misleading name). Manager.List
  returns the pending requests as `[]Pending`, beside History, GetPending,
  ListAutoApproveRules, ListTrustedSigners and ListTrustRules, so "List" alone
  does not say what it lists. cli's Client.List was already renamed
  Client.Pending for the same reason.
  Fix: Rename Manager.List to Manager.Pending, and update its caller in
  `internal/api/websocket.go` (pendingRequests) and the tests in api,
  approval, sshagent and proxy_test.go. (user, 2026-11-22 review of c27)
- `internal/approval/manager.go` AutoApproveRule.ID,
  Manager.AddAutoApproveRule and Manager.RemoveAutoApproveRule;
  `internal/api/handlers.go` HandleAutoApproveDelete;
  `internal/api/websocket.go` autoApproveRuleRemovedMsg (primitive
  obsession). Request ids have their own RequestID type, but auto-approve rule
  ids are plain strings: the rule's ID field, AddAutoApproveRule's result,
  RemoveAutoApproveRule(id string), the DELETE handler and the
  auto_approve_rule_removed message. A request id can be passed where a rule
  id is expected.
  Fix: An AutoApproveRuleID string type in approval, used by
  AutoApproveRule.ID, both Manager methods, HandleAutoApproveDelete and
  autoApproveRuleRemovedMsg.ID. gpgsign's wsMsg stops reading this id once the
  s1-8 entry above lands. (b5-15, x-12)
- `internal/approval/manager.go` AutoApproveRule.Collection,
  AutoApproveRule.matches and Request.AutoApproveRule (primitive obsession).
  The rule's Collection is a string, and "" means "any collection": matches
  tests `r.Collection != ""`, and AutoApproveRule leaves it "" for an item
  that lies in no collection. The web reads the same "" in
  `web/src/lib/HistoryEntry.svelte` hasMatchingRule and `web/src/lib/types.ts`
  AutoApproveRule. ItemInfo.Collection already returns
  (dbustypes.CollectionRef, bool); the rule is the last place that keeps the
  zero-means-none state.
  Fix: Make the field optional in its type (for example a
  `*dbustypes.CollectionRef` or a collection name with `omitempty`, absent
  meaning any collection), matches tests presence, and the web treats an
  absent collection as any. This changes the auto-approve rule's JSON, so the
  web UI changes with it. (r3-collection)
- `internal/approval/gpgsign.go` GPGSignInfo.Signed and
  GPGSignInfo.MarshalJSON (algebraic blindness). NewGPGSignInfo is the one
  producer that derives Signed from SignedObject, but Signed stays an exported
  field beside the bytes it is parsed from, so a struct literal can still pair
  bytes with a payload that they do not hold. The golden tests do so by hand,
  types_test.go builds one with no Details, and MarshalJSON's "kind is not
  set" error and its doc sentence exist only for that state.
  Fix: Unexport the parsed payload (`signed SignedPayload`, with a Signed()
  accessor), so that only NewGPGSignInfo and UnmarshalJSON set it.
  MarshalJSON's nil check and its doc sentence go, and the golden cases build
  through NewGPGSignInfo. The api tests that forge display fields send raw
  JSON instead of a GPGSignInfo literal. The readers of .Signed in api,
  notification and gpgsign call the accessor. (rc3-b-7)
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

### sshagent

- `internal/sshagent/proxy.go` proxyAgent.keysMu, proxyAgent.List and
  proxyAgent.findKeyComment, `internal/sshagent/proxy_test.go` (oversized
  lock). keysMu guards only the keys slice. List replaces the slice whole, and
  findKeyComment reads it on every Sign. The field comment "cache key list so
  we can look up comments for sign requests" is lowercase and narrates the
  means. The tests reach into keysMu to seed the list (two places).
  Fix: keys becomes atomic.Pointer[[]*agent.Key], with Store in List and Load
  in findKeyComment. keysMu goes, and the tests seed the list by calling
  proxy.List(). The field comment goes, or says in one plain sentence what the
  cache is for. (b6-8)

### notification

- `internal/notification/desktop.go` formatDurationShort (misleading name).
  The doc gives "90s" as an example output, but 90s formats as "1m30s": a
  duration of 60s or more always carries minutes. The function has no test.
  Fix: Drop the false example (for example, "Returns a compact label for d,
  such as \"2m\" or \"1m30s\"."), and add a table test for 0, 45s, 2m and 90s.
  (b8-16)
- `internal/notification/slow_upstream.go` NewSlowUpstreamNotifier and
  newSlowUpstreamNotifier (duplication). NewSlowUpstreamNotifier(*DBusNotifier)
  and newSlowUpstreamNotifier(persistentNotifier) have identical bodies.
  Fix: One exported NewSlowUpstreamNotifier(notifier persistentNotifier).
  main.go passes its *DBusNotifier unchanged, and the test calls the exported
  one. (b8-17)
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
- `internal/proxy/service.go` Service.Unlock (misleading name). Unlock
  describes every object as a collection with caller.collectionInfo, but
  Secret Service Unlock also takes item paths, and those get an empty label in
  the approval history.
  Fix: Choose caller.itemInfo or caller.collectionInfo per object, by whether
  dbustypes.ParseItemPath accepts its path. (b9-21)
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
- `internal/proxy/item.go` ItemHandler.GetSecret and SetSecret,
  `internal/proxy/collection.go` CollectionHandler.CreateItem,
  `internal/proxy/service.go` Service.GetSecrets (duplicated knowledge). The
  comments above the SessionManager.ForClient and ForUpstream calls ("Rewrite
  session path and, for DH sessions, encrypt ..." and "Map the remote session
  to the upstream session, decrypting ...") repeat those methods' docs in
  `internal/proxy/session.go`, and the ForUpstream comment is written twice.
  GetSecrets' "The return type is Dict<ObjectPath, Secret> where Secret is
  (oayays)" restates the type of the Store target.
  Fix: Delete the four call-site comments and the Store-type comment.
  (b9-23)
- `internal/proxy/collection.go`, `internal/proxy/item.go`,
  `internal/proxy/service.go` (split protocol). The Secret Service spec's "no
  object" and "no prompt" path is written as the literal "/" 16 times
  (collection.go 8, item.go 3, service.go 5).
  Fix: A named dbus.ObjectPath constant in internal/dbus (for example
  NoObject), used at every site. (b9-24)
- `internal/proxy/proxy.go` connection.export, `internal/proxy/prompt.go`
  isPromptPath, `internal/proxy/session.go` SessionManager.CreateSession,
  `internal/testutil/mockservice.go` (OpenSession and the prompt path of the
  mock) (split protocol). Package dbus has ServicePath, CollectionsPath and
  AliasesPath, but the prompt root `/org/freedesktop/secrets/prompt` is a
  literal where connection.export exports PromptHandler, again (with a
  trailing slash) in isPromptPath, and again in the mock. The session root
  `/org/freedesktop/secrets/session` is a literal in CreateSession and in the
  mock's OpenSession.
  Fix: Add PromptsPath = ServicePath + "/prompt" and SessionsPath =
  ServicePath + "/session" to package dbus. The export, isPromptPath (via
  strings.CutPrefix on PromptsPath + "/"), CreateSession and the mock use
  them. (b10-14, rc2-d-14)
- `proxy_test.go`, `internal/proxy/collection_test.go`,
  `internal/testutil/mockservice.go` (the CreateItem property reads) (split
  protocol). Package dbus has ItemLabel, ItemAttributes and CollectionLabel,
  and the proxy reads CreateItem properties by those names. The mock backend
  still reads `"org.freedesktop.Secret.Item.Label"` and `".Attributes"` as
  literals, and five CreateItem property maps in proxy_test.go and the maps
  in collection_test.go spell them too.
  Fix: The mock and the tests use dbustypes.ItemLabel and ItemAttributes.
  (b10-15)
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
- `internal/proxy/proxy.go` Proxy.ConnectWith, `internal/proxy/forward.go`
  interfaceOf and callForwarder.forward, `internal/proxy/tracker.go` package
  doc (garbled sentence, split protocol). ConnectWith's doc "Serves the Secret
  Service from backendConn to clients on frontConn" uses parameter names as
  nouns. callForwarder.forward joins `interfaceOf(msg)+"."+memberOf(msg)` by
  hand, which is dbustypes.Interface.Member again, because interfaceOf returns
  a string. tracker.go repeats proxy.go's package doc.
  Fix: ConnectWith: "Serves the Secret Service from the given backend
  connection to the clients on the given front connection." interfaceOf
  returns dbustypes.Interface and forward calls
  interfaceOf(msg).Member(memberOf(msg)). Delete tracker.go's package doc.
  (rc3-d-21)
- `internal/proxy/prompter_test.go` TestPrompterBridge (misleading doc). The
  doc says that the backend's only SystemPrompter is "the display-less
  gcr-prompter fallback, so unlock hangs forever". ADR 0009 says that in the
  local-takeover topology that fallback draws its own GTK dialog, and the
  prompterBridge doc was already corrected to follow the ADR. The test doc
  still makes the old claim, and it opens with the test's name.
  Fix: Rewrite the doc from ADR 0009: the bridge claims SystemPrompter on the
  backend bus so that the unlock prompt reaches gnome-shell on the session
  bus, and the test drives both directions of the exchange. (u-longdocs)

Found by the 2026-11-21 23:24 unsmell run (b1-12, b1-13, b2-15, b2-16, b2-17,
b2-18, x-15, rc3-a-14, b4-22, b4-23, b4-25, b7-8, rc2-c-6, x-2, s1-8, s1-1,
s1-10, b5-15, x-12, rc3-b-7, b4-15, r1-17, rc3-d-20, b6-8, b8-16, b8-17, b8-19,
b9-13, b9-21, b9-22, rc2-b-8, b9-23, b9-24, b10-14, rc2-d-14, b10-15, rc2-d-15,
rc3-d-21, plus the user's Manager.List review note and the r3-collection and
u-longdocs fix notes). Also planned by this run in other tasks: b1-11 (gm1ah),
b4-16 (6qbjt), b4-20 and x-16 (h5fa0), b4-24 and rc2-c-13 (x9yi5), rc2-c-12
(vf553), rc3-c-13 (nhmks), s1-6 and s1-7 (9pz0t). s1-16 is covered by hf9n2
and v6m33.
