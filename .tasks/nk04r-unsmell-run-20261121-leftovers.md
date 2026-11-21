---
status: todo
tags: [unsmell, refactor]
---
# Leftovers of the 2026-11-21 unsmell run (gpg signing, approval, proxy)

## Goal
Done when each item below is fixed as its "Fix" says.

### api

- `internal/api/websocket.go:104-108` (security). HandleWS accepts any Origin
  (OriginPatterns ["*"]), and the only gate is the SameSite=Strict session
  cookie (ADR 0006). SameSite ignores ports, so a page served by another app on
  another localhost port gets the cookie, can open the socket and reads every
  approval event. No record says why "*" is there (commit 13a7f6a added it).
  Fix: drop OriginPatterns so coder/websocket enforces same origin, with a test
  that a foreign Origin is refused, and run the e2e and Playwright suites; or
  record in ADR 0006 why cross-origin upgrades must stay allowed. (b3-4, d6)
- `internal/api/gpgsign.go:17-55`, `internal/api/resolver.go:97`
  (temporal coupling). GPGRunner splits one operation into FindGPG and
  RunGPG(gpgPath, keyID, commitObject). Every caller has to find the binary
  first and then hand its path back, and RunGPG takes two adjacent strings
  (gpgPath and keyID) that can be swapped without a compile error.
  Fix: One method, `Sign(keyID string, commitObject []byte)
  (approval.GPGResult, error)`, that reports GPGNotFound itself. signGPG and
  fakeGPGRunner (gpgsign_test.go:324) shrink to match. (b1-19)
- `internal/api/resolver.go:117` (primitive obsession). approveGPGSign takes
  id beside req. Both callers (:37 and :83) got req from GetPending(id), so id
  is only req.ID under a second name.
  Fix: `approveGPGSign(req *approval.Request)` reads req.ID. (b2-9)
- `internal/api/websocket.go:146-227`, `internal/api/websocket.go:441-459`
  (concept mixing). OnEvent decides which messages an event produces, builds
  them, and in the same body marshals and sends each one, dropping it when the
  buffer is full. broadcast writes the same marshal-and-try-send again.
  Fix: A pure `eventMessages(event) []WSMessage` builds the messages. A
  `wsConnection.trySend(data []byte) bool` owns the send that drops when the
  buffer is full, and OnEvent and broadcast both call it. (b3-6)
- `internal/api/websocket.go:31-63` (algebraic blindness). WSMessage is one
  struct for twelve message types. Each payload field matters only for some
  values of Type, and the group comments ("For snapshot", "For
  request_resolved") say which. ID holds a request id for most types but a
  rule id for auto_approve_rule_removed (:196-200). A message with the wrong
  fields for its Type can be built.
  Fix: One payload struct per message type, and WSMessage becomes Type plus a
  payload, with a MarshalJSON that keeps today's flat JSON. This could go
  together with .tasks/tkncw, which generates the TS types. (b3-11)
- `internal/api/websocket.go:98-140`, `internal/api/websocket.go:239-275`,
  `internal/api/websocket.go:336-352`, `internal/api/websocket.go:401-403`
  (narrating comments). The body comments in HandleWS ("Register
  connection", "Subscribe to manager events", "Send initial snapshot"),
  sendSnapshot ("Get auto-approve rules", "Get trust rules") and close
  ("Unsubscribe from manager", "Close the WebSocket") read the next line back.
  The bare word "client" at :219, :229, :326, :336 and :441 means the browser
  peer, in a file that also handles proxy.ClientInfo. isConnClosedErr's doc
  says "returns true if" instead of the house form. The docs that open with
  their own names are .tasks/vycht's.
  Fix: Delete the step comments. "client" becomes "the browser" or "the
  WebSocket peer". isConnClosedErr's doc says "Checks whether err is expected
  teardown of the connection". (b3-5)
- `internal/api/gpgsign.go:26-27`, `internal/api/gpgsign.go:32-34`,
  `internal/api/gpgsign.go:57-59`, `internal/api/gpgsign_test.go:179-184`,
  `internal/api/gpgsign_test.go:235-239`,
  `internal/api/gpgsign_test.go:335-342` (restated code). FindGPG's doc
  narrates the function it calls. RunGPG's doc repeats the gpg flags that
  ADR 0008 records. HandleGPGSignRequest's doc names its route and leaves out
  the branch that signs without a prompt for a trusted signer or an
  auto-approve rule. Three test docs run 6 to 8 lines.
  Fix: FindGPG's doc goes (it implements an interface method). RunGPG keeps
  the reason for separate stdout and stderr buffers as a body comment and
  cites ADR 0008 for the flags. HandleGPGSignRequest's doc states both
  outcomes. The long test docs keep the property tested and the
  vulnerability name. (b1-11)

### approval

- `internal/approval/manager.go:178-188`, `internal/api/types.go:77`,
  `internal/api/handlers.go:335-374` (unparsed input). approval.Resolution is
  a string type with no parser, so any string decodes into it.
  HandleTestInjectHistory passes the decoded value straight to
  Manager.AddHistoryEntry, so an unknown resolution reaches the history and
  the WebSocket clients. The edge is test-mode only.
  Fix: Resolution gets an UnmarshalText that accepts only its six constants,
  with a round-trip test. The test-inject handler then answers 400 for an
  unknown value through decodePost. (b2-6)
- `internal/approval/gpgsign.go:28-50`, `internal/gpgsign/commit.go:12-32`
  (algebraic blindness, duplicated knowledge). GPGSignInfo and
  gpgsign.SignedPayload are both flat over three kinds of signed object.
  Committer and ParentHash are commit-only, TagName and Target are tag-only,
  and Pushee is push-only, so the types allow a tag with a ParentHash or a
  commit with a Pushee. CommitMsg and Author hold a tag's or a push's values
  under commit names. Which field means what for each kind is written as
  field comments on both types, under different field names.
  Fix: Per-kind payloads (a commit with Committer and ParentHash, a tag with
  Name and Target, a push with Pushee, and unknown) under the shared fields,
  with field names that fit every kind, defined once and used by both
  packages. The per-kind meaning is then stated once, on that type. The JSON
  is either kept by a custom marshaller or changed together with web
  (.tasks/rt113 has the TS side) and with cli's copy (cli/client.go:41).
  (b4-6, b6-14, x-4)
- `internal/approval/gpgsign.go:181-199`, `internal/approval/manager.go:452-459`
  (repeated derivation). The expiry of a pending request is written twice.
  createGPGSignRequest's goroutine says it "mirrors the timer.C branch in
  RequireApproval", but the copies have drifted: the gpg copy deletes the
  request, sets ResolutionExpired and closes done, while RequireApproval's
  only notifies. The resolution and the event type are also set as pairs by
  hand (gpgsign.go:135 with :137, :194 with :197), although
  EventType.Resolution (manager.go:322) owns that mapping. The goroutine also
  races done against time.After by hand.
  Fix: One Manager method ends a request from an event. It sets the
  resolution from EventType.Resolution, deletes the request, closes done and
  notifies. It is the resolve that .tasks/nmzpt plans, extended to expiry.
  Both paths arm it with time.AfterFunc(m.timeout). (b4-7, x-20)
- `internal/approval/gpgsign.go:128-130`,
  `internal/approval/gpgsign.go:170-172`, `internal/api/gpgsign.go:107-118`,
  `internal/api/remote.go:46` (unparsed input). The api edge checks the
  signed bytes with requireSignedBytes in both gpg-sign handlers, and
  RecordAutoApprovedGPGSign and createGPGSignRequest check them again. The
  second check can no longer fail, yet the api maps its error to a 500
  (api/gpgsign.go:92, :146 and remote.go:54). CommitObject stays a string
  that each use converts to []byte.
  Both handlers also repeat three edge steps in an order they keep by hand:
  decodePost, requireSignedBytes, then boundSignRequest, which dereferences
  info and writes its own 400 for a payload it cannot parse.
  Fix: One edge function, such as decodeSignRequest(w, r, sender)
  (approval.RequestInfo, bool), checks the signed bytes, parses and binds
  them, and returns the request, so no handler orders the steps. The request
  holds the checked bytes as a type, such as SignedObject []byte, and the
  Manager methods lose the check and the error return that only it produced.
  (b4-8, x-21, r2-3)
- `internal/approval/gpgsign.go:10` (ambiguous noun). RequestTypeGPGSign's
  doc reads its name back and calls the requests "GPG commit signing"
  requests. The GPGSignKind doc below it says that the signed bytes are not
  always a commit.
  Fix: Delete the doc, since the type and the constant's name say it, or
  write "git object signing" if a lint wants a doc. (b4-11)
- `internal/approval/manager.go:67-71`, `internal/approval/manager.go:115-139`,
  `internal/approval/manager.go:196-200`, `internal/approval/types.go:331-337`
  (speculative generality). Request, HistoryEntry, ItemInfo and SenderInfo
  carry json tags, but nothing encodes them. The api package converts them to
  its own DTOs (api/types.go), and the `json:"-"` tags on Request.GPGResult
  and SenderInfo.Client hide fields from an encoding that never happens.
  AutoApproveRule and GPGSignInfo are encoded directly, so their tags stay.
  Fix: Delete the json tags of those four types, so that only the api DTOs
  spell the wire keys. Or keep them and plan the merge of the api DTOs
  (.tasks/tkncw, .tasks/b4jsb). (b5-1)
- `internal/approval/manager.go:395-397` (misplaced rule). The rule that
  delete and write requests never use the approval cache is a fact about
  RequestType. It is written inline in RequireApproval's condition, beside
  the approvalWindow and empty-items checks.
  Fix: A RequestType method, such as `(t RequestType) cacheable() bool`, owns
  the rule, and the body comment that explains it moves onto that method.
  (b5-5)
- `internal/approval/manager.go:697-703`, `internal/config/config.go:25`
  (duplicated knowledge). The default lifetime of an auto-approve rule, two
  minutes, is spelled twice: as config.DefaultAutoApproveDuration and as the
  fallback inside Manager.AutoApproveDuration. The fallback only serves
  managers built without the config (tests and NewDisabledManager).
  Fix: approval owns the default as a constant that config uses, and
  NewManager applies it once with cmp.Or, so AutoApproveDuration becomes a
  plain getter. (b5-6)
- `internal/approval/manager.go:397`, `internal/approval/manager.go:628`,
  `internal/approval/manager.go:657`, `internal/approval/manager.go:970`
  (repeated derivation). "Caching is on" is written four times as
  approvalWindow > 0 or <= 0: in RequireApproval and again in the
  checkApprovalCache it calls, in CacheItemForSender, and in cacheApproval.
  CacheItemForSender also checks `|| m.disabled`, which is redundant, since a
  disabled manager has a zero window.
  Fix: One `(m *Manager) cachesApprovals() bool`. checkApprovalCache,
  CacheItemForSender and cacheApproval each guard with it, and
  RequireApproval drops its copy. (b5-7)
- `internal/approval/manager.go:387-389` (incomplete doc). RequireApproval's
  doc lists ErrDenied, ErrTimeout, ErrDeniedByRule and ErrIgnored. It also
  returns ctx.Err() when ctx ends, and a request cancelled through
  Manager.Cancel comes back as ErrDenied (manager_test.go:195 has to say so).
  Callers handle a cancelled context differently, so the doc leaves out a
  fact they need.
  Fix: The error sentence adds "or with ctx's error when ctx ends", and says
  that a request cancelled by Manager.Cancel errors with ErrDenied. (b5-8)
- `internal/approval/manager.go:865-888` (inline generic machinery).
  RemoveAutoApproveRule writes find-and-delete by hand, with a found flag, a
  copy and an append splice.
  Fix: `i := slices.IndexFunc(...)`, then `removed := m.autoApproveRules[i]`
  and slices.Delete, under the same lock. (b5-9)
- `internal/approval/manager.go:98-143`, `internal/approval/manager.go:431-432`,
  `internal/approval/manager.go:963-966`, `internal/approval/gpgsign.go:132-136`
  (algebraic blindness). Request is two things. A pending request has a done
  channel, a resolution and a real ExpiresAt. A settled record, built by
  record() with newRequest(0), has no done channel and an ExpiresAt equal to
  its CreatedAt. RecordAutoApprovedGPGSign builds yet another shape by hand,
  with a closed done channel and a resolution. RequireApproval and
  createGPGSignRequest make done themselves after newRequest, so the order is
  kept by hand. The doc "a secret access request awaiting approval" is false
  for history records.
  Fix: Split the pending state off. A pending wrapper (or newPendingRequest)
  owns done, resolution and ExpiresAt, and the settled record is the plain
  description. One constructor builds each, and RecordAutoApprovedGPGSign
  uses it. The doc states what the type is in both cases. (b5-10)
- `internal/approval/manager.go:57`, `internal/approval/manager.go:93-96`,
  `internal/approval/manager.go:126-133`, `internal/api/resolver.go:36`,
  `internal/api/resolver.go:78` (algebraic blindness). Several fields matter
  only for one kind of value, and their docs say so. Event.Rule is set only
  for the rule events, where Request is nil. SearchAttributes is set for
  search requests only, and GPGSignInfo for gpg_sign requests only. Because
  the type does not hold that rule, api writes
  `req.Type == approval.RequestTypeGPGSign && req.GPGSignInfo != nil` twice.
  Fix: The rule events get their own event type (or Observer method), so an
  Event always carries a Request. The per-type payloads become one typed
  payload per request kind. Until then, a `Request.GPGSign() (*GPGSignInfo,
  bool)` method serves both Resolver methods. (b5-11, b2-8)
- `internal/approval/manager.go:145-147`, `internal/approval/manager.go:901-902`,
  `internal/approval/manager.go:607-608`, `internal/approval/manager.go:684-685`,
  `internal/approval/manager_test.go:1114-1115`,
  `internal/approval/manager_test.go:1146-1147` (duplicated knowledge). Two
  facts each have several copies. "Rules match only on the invoker's exe
  (ADR 0005)" is on AutoApprovable and again on invokerExe. "Each bus numbers
  its own senders" is on approvalCacheKey, again on ReleaseClient, and again
  on two tests.
  Fix: invokerExe's doc keeps only that it returns the invoker's
  /proc/PID/exe, or false. ReleaseClient points to approvalCacheKey for the
  reason, and the two test docs say only what each test guards. (b5-13)
- `internal/approval/manager.go:706-707`,
  `internal/approval/manager_test.go:1422-1429`,
  `internal/approval/autoapprove_test.go:400` (speculative generality).
  extractCollection is a package variable only so that TestCheckTrustRules can
  swap it. The swap reimplements dbus.ExtractCollection with a "simple
  parser" that returns the same collection for every path the test uses, so
  the seam tests nothing the real function would not, and the copy can drift
  from it.
  Fix: The test drops the swap, and manager.go calls
  dbustypes.ExtractCollection directly. TestExtractCollection then tests the
  dbus function, or moves to internal/dbus. (b5-14)
- `internal/approval/manager.go:437`, `internal/approval/manager.go:440`,
  `internal/approval/manager.go:447`, `internal/approval/manager.go:474`,
  `internal/approval/manager.go:579`,
  `internal/approval/manager_test.go:1206-1209` (narrating comments). The
  body comments "Notify observers of new request", "Ensure cleanup when we
  exit", "Create timeout timer" and "Only include non-expired requests" read
  the next line back. GetPending's doc describes its read lock. A regression
  test explains a bug of an asynchronous notify ("go o.OnEvent") that no
  longer exists.
  Fix: Delete the body comments and GetPending's lock sentence. The
  regression test's comment keeps one sentence: the created event must reach
  observers before the cancelled one. (b5-15)
- `internal/gpgsign/daemon.go:98`, `internal/gpgsign/daemon.go:114`,
  `internal/gpgsign/daemon.go:138`, `internal/gpgsign/daemon.go:177`,
  `internal/gpgsign/daemon.go:185`, `internal/approval/gpgsign.go:144`,
  `internal/api/resolver.go:30`, `internal/notification/desktop.go:36-41`
  (primitive obsession). An approval request's id is a bare string all the
  way from the Manager to the wire. Manager.CreateGPGSignRequest returns one,
  Resolver.Approve and its siblings and the notification Approver take one,
  and the thin client carries one in PostSigningRequest,
  CancelSigningRequest, pendingSignRequest.id, wsMsg.ID and
  WaitForResolution. Nothing links the id the daemon returns to the one the
  WebSocket reports.
  Fix: An approval.RequestID type, used from the Manager out to the API DTOs
  and the thin client's wire structs. (b6-8, x-27)

### gpgsign

- `internal/gpgsign/commit.go:124-130` (pass-through). ParseCommitObject only
  reshapes ParseSignedPayload's result into four strings, and its doc says it
  is kept "for callers and tests". Its only callers are in commit_test.go.
  Fix: Delete ParseCommitObject, and move commit_test.go's cases onto
  ParseSignedPayload in payload_test.go. (b6-7)
- `internal/gpgsign/daemon.go:84-86`, `internal/gpgsign/daemon.go:100-102`,
  `internal/gpgsign/daemon.go:119`, `internal/gpgsign/daemon.go:36`
  (repeated derivation). "A request to the daemon carries the token" is
  written three times: twice as a new http.Header plus c.token.Authorize,
  and once on req.Header. The WebSocket URL is built inline as
  "ws://"+socketHost+path, beside SocketURL's "http://"+socketHost+path.
  Fix: A DaemonClient method that returns the authorized header, used by all
  three, and a helper for the ws URL next to SocketURL. (b6-9)
- `internal/gpgsign/commit.go:92-120` (duplication). Each header case spells
  its name twice, once in strings.HasPrefix and again in strings.TrimPrefix
  ("author ", "tagger ", "pusher ", "committer ", "parent ", "tag ",
  "object ", "pushee "), and "object " a third time in the kind switch. A
  typo in one half silently breaks that field.
  Fix: Split each header once with strings.Cut(h, " ") and switch on the
  key, or use strings.CutPrefix in each case. (b6-10)
- `internal/gpgsign/run.go:77-78` (misleading name). collectSignInfo calls
  stdin a "commit object" in its comment and names the bytes commitBytes. The
  bytes are any payload that git signs, a tag or a push certificate
  included, as the comment a few lines below says.
  Fix: Name the local for the signed payload (payloadBytes) and drop the
  comment. GPGSignInfo.CommitObject's Go name raises the same question in
  approval; its JSON key stays. (b6-12)

### notification

- `internal/notification/desktop.go:754`, `internal/sshagent/proxy.go:74`
  (split protocol). The destination of an SSH sign request travels as the
  item attribute key "destination". sshagent writes it and the notification
  reads it by the same literal. The reader also checks presence twice
  (`ok && dest != ""`).
  Fix: One exported constant (or an accessor such as
  ItemInfo.SSHDestination()) in approval, used by the writer and the reader,
  with a round-trip test. The reader keeps only `dest != ""`. (b7-4)
- `internal/notification/desktop.go:92-94`, `internal/notification/desktop.go:143`,
  `internal/notification/desktop.go:220`,
  `internal/notification/desktop_test.go:1010-1011` (split protocol). The
  channel buffer size 16 is written four times in desktop.go and twice in the
  test helper, and processSignals' comment depends on it ("after 16
  closes").
  Fix: A named constant (such as signalBuffer = 16) beside notifyInterface,
  used by NewDBusNotifier and reconnect. The test helper goes with
  .tasks/vf553's fix (newTestDBusNotifier calls NewDBusNotifier). (b7-6)
- `internal/notification/desktop.go:761` (restated code). The comment "New
  format: item label, then process chain" narrates history ("new format")
  and reads the next lines back.
  Fix: Delete it. (b7-7)
- `internal/notification/desktop.go:268-273` (restated code). The trailing
  comments `// summary`, `// body` and `// actions` read the argument names
  back. `// app_name` and `// app_icon` name the spec's parameters, and
  `// replaces_id` adds what 0 means, so those stay.
  Fix: Delete the three comments that repeat the argument's own name. (b7-9)
- `internal/notification/desktop.go:457-462` (long comment). handleAction's
  6-line comment explains an invariant that the code could hold itself: the
  request is forgotten before Approve or Deny, so the synchronous
  handleResolved callback does not Close the notification, and the
  duplicate ActionInvoked signal does not reach the approver.
  Fix: With .tasks/1m5wn's bimap, a named `forgetNotification(id)
  (requestID, bool)` call states the step, and the comment keeps one
  sentence on the duplicate ActionInvoked signal. (b7-10)
- `internal/notification/slow_upstream.go:18-20`,
  `internal/notification/slow_upstream.go:25`,
  `internal/notification/slow_upstream.go:30`,
  `internal/notification/slow_upstream.go:52` (restated code). The two
  constructors' docs name their parameter and their users back ("(for
  testing)"), and slowUpstreamSummary's doc restates its switch.
  SlowUpstreamNotifier's doc repeats the pinentry fact that the proxy item on
  that fact (b9-12) keeps in one place. That these docs, and
  persistentNotifier's (:12), proxy.UpstreamNotifier's
  (proxy/slow_upstream.go:44) and mockNotifier's
  (proxy/slow_upstream_test.go:14), open with their own names is
  .tasks/x9yi5's.
  Fix: Delete the constructor docs and slowUpstreamSummary's doc.
  SlowUpstreamNotifier's doc keeps only what the type is for. (b8-8)

### proxy

- `internal/proxy/collection.go:95-104`, `internal/proxy/item.go:90-99`,
  `internal/proxy/item.go:136-145`, `internal/proxy/item.go:195-204`,
  `internal/proxy/service.go:116-128`, and the `SenderCall(X.senderResolver(
  sender))` sites in collection.go, item.go and service.go (request-scoped
  value threaded, repeated derivation). The approval-gated handlers fetch
  item info with a lazy SenderCall context, then resolve the same sender
  eagerly two statements later, so a slow fetch before the approval resolves
  the sender twice. Apart from that, `SenderCall(X.senderResolver(sender))`
  is built at 19 sites, always from senderOf(msg), and senderResolver is
  written three times (collection.go:46, item.go:46, service.go:44).
  Fix: The five approval-gated handlers resolve senderInfo first and fetch
  item info with a call context built from it, so their senderCtx locals go.
  The rest get one method on .tasks/36odh's shared handler receiver,
  `callContext(msg dbus.Message) UpstreamCallContext`, which replaces
  senderResolver and every SenderCall literal. (b9-3, b9-4)
- `internal/proxy/collection.go:50-54`, `internal/proxy/item.go:50-54`,
  `internal/proxy/service.go:48-52`, and the 22 upstreamWithContext calls
  (repeated derivation). Every upstream method call is `obj :=
  X.upstream(path)` followed by `X.upstreamWithContext(ctx, func() *dbus.Call
  { return obj.Call(Iface+".Method", 0, args...) })`.
  Fix: `callUpstream(ctx UpstreamCallContext, path dbus.ObjectPath, method
  string, args ...any) *dbus.Call` on .tasks/36odh's shared receiver,
  replacing upstreamWithContext and the closures. (b9-5)
- `internal/proxy/item.go:305-325`, `internal/proxy/service.go:423-447`,
  `internal/proxy/collection.go:131-165`, `internal/proxy/service.go:468-480`
  (duplication). getItemInfo is written twice with identical bodies, in
  ItemHandler and in Service. The collection-label fetch is written in
  getCollectionInfo and again in getUnlockInfo's loop. The Label and
  Attributes Variant type assertions appear seven times, including
  extractItemInfo's string-key access into the CreateItem properties.
  Fix: One itemInfo(path, ctx) and one collectionInfo(path, ctx) on the
  shared receiver, and one function that reads Label and Attributes from a
  map[string]dbus.Variant, used by both the upstream fetch and
  extractItemInfo. (b9-6)
- `internal/proxy/collection.go:281-345`, `internal/proxy/item.go:244-304`,
  `internal/proxy/service.go:287`, `internal/proxy/service.go:304`,
  `internal/proxy/service.go:326`, `internal/proxy/subtree.go:58`,
  `internal/proxy/subtree.go:80` (duplication). CollectionHandler and
  ItemHandler carry identical Get, GetAll and Set bodies except for the path
  check. "org.freedesktop.DBus.Properties.GetAll" and ".Set" are spelled in
  collection.go, item.go, service.go and subtree.go, and
  "org.freedesktop.DBus.Error.UnknownInterface" three times in service.go.
  Fix: Constants for the Properties interface and the UnknownInterface error
  in internal/dbus/types.go, and one properties implementation parameterised
  by the path predicate. (b9-7)
- `internal/proxy/collection.go:67-85`, `internal/proxy/item.go:67-80`,
  `internal/dbus/types.go:86-98` (split protocol). The collection and alias
  path prefixes are spelled in isCollectionPath, isItemPath and
  dbus.ExtractCollection, so three parsers of one path scheme must agree.
  Fix: One path parser in internal/dbus that classifies an object path as a
  collection or an item (and gives the collection), used by all three.
  (b9-8)
- `internal/proxy/collection.go:56-58`, `internal/proxy/item.go:56-58`,
  `internal/proxy/service.go:54-56`, `internal/proxy/service.go:423-426`,
  and the step comments in the approval-gated handlers (duplicated
  knowledge, restated code). That the caller's context reaches the slow
  upstream notification, so the user sees what triggered a pinentry prompt,
  is stated four times (three upstreamGetProperty docs and Service.getItemInfo),
  and again on notification.SlowUpstreamNotifier. "Get a context that will be
  cancelled if the client disconnects" (six times) restates
  contextForSender's doc. "Fetch item info (label + attributes)", "Resolve
  sender information" and "Require approval before ..." restate the call
  below them.
  Fix: Keep the pinentry fact once, on UpstreamCallContext, and delete the
  restating comments. (b9-12)
- `internal/proxy/service.go:449-466` (inline generic machinery).
  searchAttributesToItemInfo sorts the keys and joins "k=v" pairs by hand.
  Its doc says it builds "an ItemInfo" while it returns a slice, and "for
  notification display" is one caller's reason, and an incomplete one, since
  the result also feeds trust-rule matching and history.
  Fix: slices.Sorted(maps.Keys(attributes)) and strings.Join. The doc states
  only the one-item result and its sorted label. (b9-13)
- `internal/proxy/collection.go:224-240`, `internal/proxy/service.go:67-76`
  (duplication). CreateItem logs the "ignored" outcome with the same
  LogMethod call in the ShouldIgnore branch and in the ErrIgnored branch.
  OpenSession writes the same error log line in both of its error branches.
  Fix: One ignored exit in CreateItem (log once, then return). OpenSession
  logs once before choosing which error to return. (b9-14)
- `internal/proxy/collection.go`, `internal/proxy/item.go`,
  `internal/proxy/service.go` (41 sites, for example collection.go:110,
  collection.go:125-127, item.go:105, item.go:120-122) (mixed altitude). The
  approval-gated handlers interleave their steps with inline LogMethod map
  literals and the result strings "ok", "denied", "error" and "ignored".
  GetSecret and Service already use typed logger methods (LogItemGetSecret,
  LogGetSecrets).
  Fix: Typed logger methods per D-Bus method (LogCollectionDelete,
  LogItemDelete, LogCreateItem, ...), and a result type in logging instead
  of the string. (b9-15)

### tests

- `internal/api/gpgsign_test.go:42`, `:76`, `:104`, `:137`, `:214`, `:267`,
  `:359`, `:405` (repeated derivation). Eight tests build the same POST to
  /api/v1/gpg-sign/request: httptest.NewRequest, the Content-Type header, a
  recorder, and a call to HandleGPGSignRequest. Two of them marshal a
  map[string]any body first (:199-211, :251-264).
  Fix: A `postGPGSign(t, h, body string) *httptest.ResponseRecorder` helper
  in gpgsign_test.go, and a body builder that marshals a GPGSignInfo instead
  of a map. (b1-14)
- `internal/approval/gpgsign_test.go:55`, `:116`, `:147`, `:201`, `:230`
  (duplication). Five tests scan mgr.List() for the request's id, although
  mgr.GetPending(id) exists. The repeated NewManager literal in the same file
  belongs to .tasks/6qbjt.
  Fix: The scans become `mgr.GetPending(id)` checks. (b4-15)
- `internal/gpgsign/payload_test.go:44-45` (restated code). The annotated-tag
  case says "this is the bug being fixed (previously the tag was parsed as a
  commit and everything blanked)", which narrates history that the test no
  longer needs.
  Fix: Delete the comment. The assertions already say that commit-only
  fields stay empty for a tag. (b6-13)
- `internal/notification/desktop_test.go:879-890` (inline generic machinery).
  contains and stringContains hand-roll a substring search, with a guard
  expression that the loop already covers. It is strings.Contains.
  Fix: Delete both helpers. The call sites use strings.Contains, or
  assert.Contains, which the file already uses. (b7-3)
- `internal/notification/desktop_test.go:478-482`,
  `internal/notification/desktop_test.go:520-523` (duplicated knowledge).
  Two test docs restate facts that have an owner. The first explains why
  every value in the body is escaped, which ADR 0007 now records, and calls
  the values "client-controlled", which now reads as
  approval.SenderInfo.Client. The second explains why quotes stay unescaped,
  which markupEscaper's doc (desktop.go:675-679) states.
  Fix: Each test doc names what it guards and points to ADR 0007 or to
  markupEscaper. "client-controlled" becomes "sender-controlled". (b7-5)
- `internal/proxy/slow_upstream_test.go:149-150` (duplicated knowledge). The
  test doc restates the rule about Always and prompted requests that
  SlowUpstream.Always and SlowUpstream.reports own (slow_upstream.go:61-69),
  and that config.go states for users.
  Fix: Delete the test doc. The table rows name the cases. (b8-9)
- `internal/proxy/slow_upstream_test.go:15-31` (oversized lock).
  mockNotifier.mu guards notifyCalls, dismissCalls and lastRequest, but
  WithSlowNotify calls NotifySlowUpstream and the dismiss function on its
  caller's goroutine, which is the test goroutine. fn's goroutine never
  touches the mock, so the lock guards nothing shared.
  Fix: Delete mu and every Lock and Unlock pair. (b8-10)
- `internal/notification/slow_upstream_test.go:86-130`,
  `internal/notification/slow_upstream_test.go:221-235` (duplication).
  GPGSignSummary, SSHSignSummary, SearchSummary and UnlockSummary are one
  test written four times, differing only in the request type and the
  expected summary. All thirteen tests repeat the mock, constructor, dismiss
  and lock scaffold.
  Fix: One table test over (type, want summary) that calls
  slowUpstreamSummary, and one table test over (request, want body) that
  calls formatSlowUpstreamBody. One notifier test keeps the
  NotifyPersistent and Close round trip. (b8-11)

### docs

- `internal/approval/gpgsign.go:182`, `internal/approval/gpgsign_test.go:41`,
  `:74`, `:88`, `:124`, `:155`, `internal/api/gpgsign_test.go:36`, `:71`,
  `:115`, `:130`, `:419`, `internal/gpgsign/run.go:77`, `:85`, `:121`, `:129`,
  `:144`, `internal/gpgsign/outcome.go:12`, `:71` (ambiguous noun). Comments
  cite requirement ids that no file in the repository defines: "ERR-03" and
  "Case 1" to "Case 10", and SIGN-02 to SIGN-09, ERR-01 and ERR-02. They come
  from a planning document that no longer exists, so a reader cannot follow
  them. Several of the run.go comments also only restate the next call
  ("Load auth token", "Block until resolution").
  Fix: The ids go everywhere at once. Where a comment holds a reason, the
  reason stays without the id, citing ADR 0008 where a decision stands behind
  the line. Comments that only restate the call go, and test docs keep only
  what the test's name does not say. (b4-13, b6-11, x-22)
- `internal/api/websocket.go:52`, `internal/approval/manager.go:135-137`,
  `internal/approval/gpgsign.go:76-77` (duplicated knowledge). That a gpg
  result is set for an approved or auto-approved gpg_sign request is stated
  on WSMessage's embedded GPGResult, on Request.GPGOutput, which owns the
  rule, and on Request.GPGResult's doc, which lists its producers instead.
  Fix: GPGOutput keeps the rule. The WSMessage field comment and
  Request.GPGResult's doc point to it, or go. (b3-7, x-7, b5-11)
- `internal/api/websocket_test.go:357-360`, `internal/gpgsign/daemon.go:181-184`
  (duplicated knowledge, long comment). The test doc is four lines, over the
  three-line limit. Its second half explains that the thin client loops past
  message types it does not recognize, so a drift between the two sides shows
  up as a read that times out. That is a fact about
  gpgsign.WaitForResolution, whose doc does not state it.
  Fix: The test doc keeps its first sentence. The timeout-on-drift fact moves
  to WaitForResolution's doc. (b3-8, x-23)
- `internal/cli/format.go:171-173` (duplicated knowledge). The comment says
  that git signs commits, annotated tags and push certificates through the
  same path. approval.GPGSignKind's doc now owns that fact (ADR 0008). The
  switch below it also compares Kind with bare strings ("tag") instead of
  the GPGSignKind constants. Left over by change c5, since cli was out of
  that run's scope.
  Fix: The comment keeps only why the approver on the trusted VT needs each
  kind labelled, or points to GPGSignKind. The switch uses the GPGSignKind
  constants if cli's GPGSignInfo copy takes that type.

Found by the 2026-11-21 unsmell run (b1-11, b1-14, b1-19, b2-6, b2-8, b2-9,
b3-5, b3-6, b3-7, b3-8, b3-11, b4-6, b4-7, b4-8, b4-11, b4-13, b4-15, b5-1,
b5-5, b5-6, b5-7, b5-8, b5-9, b5-10, b5-11, b5-13, b5-14, b5-15, b6-7, b6-8,
b6-9, b6-10, b6-11, b6-12, b6-13, b6-14, b7-3, b7-4, b7-5, b7-6, b7-7, b7-9,
b7-10, b8-8, b8-9, b8-10, b8-11, b9-3, b9-4, b9-5, b9-6, b9-7, b9-8, b9-12,
b9-13, b9-14, b9-15, x-4, x-7, x-20, x-21, x-22, x-23, x-27).
