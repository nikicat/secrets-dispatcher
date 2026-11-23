---
status: todo
tags: [unsmell, refactor]
---
# Leftovers of the 2026-11-22 20:25 unsmell run

## Goal
Done when each item below is fixed as its "Fix" says. Each sits on lines that
the run did not change, so the run planned it instead of fixing it.

### approval

- `internal/dbus/types.go:147`, `internal/dbus/types.go:157` (repeated
  derivation). ParseCollectionPath and ParseItemPath both reject an empty
  ref.Name after cutCollection, so the rule that an empty segment names no
  collection is written twice outside the function that cuts the segment.
  Fix: cutCollection returns false for an empty segment, and both callers drop
  the check. (b5-8)
- `internal/dbus/types.go:68-75`, `internal/dbus/types.go:99-132` (doc opens
  with its name). Secret's doc and the ErrLocked, ErrSessionNotFound,
  ErrObjectNotFound, ErrUnsupportedAlgorithm, ErrAccessDenied and ErrFailed
  docs open with the declared name and restate the signature ("ErrLocked
  returns an IsLocked error"); Secret's fields carry sentences in trailing
  comments. Task vycht's sweep does not list package dbus.
  Fix: Delete the restating docs (ErrInterfaceNotFound's "Returns an
  UnknownInterface error that names the interface" is the shape for one kept);
  Secret keeps its D-Bus signature fact on its own line, and the field
  comments go or move above their fields. (b5-9)
- `internal/dbus/types.go:61-62`, `internal/dbus/types.go:100`,
  `internal/dbus/types.go:105` (primitive obsession). ErrLocked,
  ErrSessionNotFound and ErrObjectNotFound take path string where
  dbus.ObjectPath is the value, and ErrUnsupportedAlgorithm takes algo string
  beside the untyped AlgorithmPlain and AlgorithmDH constants.
  Fix: The path helpers take dbus.ObjectPath; an Algorithm string type for the
  two constants and OpenSession's parameter. (b5-10)
- `internal/approval/gpgsign.go:61`, `internal/approval/gpgsign.go:99`,
  `internal/approval/gpgsign.go:103` (primitive obsession).
  CommitDetails.ChangedFiles, StagedFiles.List, ListedFiles and
  TrustedSigner.FilePrefix all hold paths relative to the repository's top
  level as plain strings, so the fields that must agree (a prefix and the
  files it filters) are typed independently.
  Fix: A repo-relative path type (such as RepoPath) for the staged files and
  the prefix, with the prefix test as its method. (b5-11)
- `internal/approval/types.go:196-199`, `internal/approval/types.go:282-285`
  (repeated derivation). TrustedSigner.matches and ProcessMatcher.matches each
  write "some process in the chain has an exe path that satisfies X" with
  slices.ContainsFunc and p.ExePath().
  Fix: One named helper over the process chain (an exe-matching predicate),
  used by both matchers. (b5-12)
- `internal/approval/manager.go:1-989` (long file). manager.go is 989 lines
  and holds several concepts. These are the request model (ItemInfo,
  RequestType, RequestInfo, Payload, Request, Pending), Resolution and
  HistoryEntry, the auto-approve rule type and its matching, the Manager's
  pending and History core, the approval cache and client claims, and trust-
  rule and trusted-signer checks. Mostly unchanged lines.
  Fix: Split by concept. request.go takes the request model, autoapprove.go
  the AutoApproveRule type and the Manager's auto-approve methods, cache.go
  approvalCacheKey, cachedApproval, CacheItemForSender and
  Claim/ReleaseClient, and manager.go keeps the Manager core and its History.
  (b6-11)
- `internal/approval/manager.go:414-419` (algebraic blindness).
  ManagerConfig.ApprovalWindow ("Zero turns this off.") and
  AutoApproveDuration ("Zero means DefaultAutoApproveDuration.") explain zero
  states in their docs. The owner's rules ban such docs. These are unchanged
  lines next to the rewritten field docs.
  Fix: ApprovalWindow becomes an optional window (for example a pointer or an
  Option type that the config parser sets only when caching is on).
  AutoApproveDuration gets its default applied where config parses it, so
  ManagerConfig always holds the real value and the zero clauses go. (b6-12)
- `internal/approval/autoapprove_test.go:37-44` (duplication). The changeset
  retyped both mustAddRule helpers (approval's and api's at
  internal/api/handlers_test.go:51) to return AutoApproveRuleID. They have the
  same body (make the rule from the request, fail on error, and add it), in
  two packages.
  Fix: One exported fixture in the non-test approval file that nhmks plans for
  the shared sender fixtures. Both packages call it. (b6-13)
- `internal/approval/autoapprove_test.go:15`,
  `internal/approval/autoapprove_test.go:139-140`,
  `internal/approval/manager.go:220-221` (duplicated knowledge). "Auto-approve
  rules match only on the invoker's exe (ADR 0005)" is stated on
  Request.AutoApprovable (manager.go:220), in testSender's doc
  (autoapprove_test.go:15) and in the CommSpoof test's doc (:140). Unchanged
  lines.
  Fix: AutoApprovable owns the fact. The test docs say only what they check
  (the CommSpoof doc keeps its scenario), and testSender's second sentence
  goes. (b6-14)
- `internal/approval/manager.go:378`, `internal/approval/manager.go:417-418`,
  `internal/config/config.go:69` (repeated derivation). The two-minute auto-
  approve default (approval.DefaultAutoApproveDuration) is applied in
  approval.NewManager and in config.ServeConfig.withDefaults, and the web
  writes it again as 120 seconds in App.svelte and websocket.ts, though the
  snapshot carries the server's value.
  Fix: one Go owner (b14-14's choice); the web takes the duration only from
  the snapshot and has no default of its own (x-12)
- `internal/approval/manager.go:220-221`,
  `internal/approval/autoapprove_test.go:15`,
  `internal/approval/autoapprove_test.go:139-140` (duplicated knowledge).
  "Auto-approve rules match only on the invoker's exe (ADR 0005)" is stated on
  Request.AutoApprovable and again in approval's and api's tests.
  Fix: the tests cite ADR 0005 by pointer to Request.AutoApprovable or drop
  the sentence, with b6-14 (x-15)
- `internal/approval/autoapprove_test.go:37-44`,
  `internal/api/handlers_test.go:51-56` (duplication). The mustAddRule helper
  is written with the same body in approval's and api's tests; the changeset
  retyped both.
  Fix: one exported fixture in approval's shared test-fixture file (task
  nhmks) (x-16)

### api

- `internal/api/websocket.go:124`, `internal/api/websocket.go:131`,
  `internal/api/websocket.go:146` (doc opens with its name). docs on lines the
  changeset did not change open with their declaration's name (WSHandler,
  wsConnection, Broadcast*, the api response types, Resolver, NewResolver,
  Deny, mockClientProvider). "// Active connections" restates conns.
  isConnClosedErr says "whether err is", a parameter name used as a noun.
  Resolver's doc names an in-repo reader (notification.Approver). OnClient*
  and Resolver.Approve are left out because h5fa0 covers them.
  Fix: delete each tautology, or keep only its (b) fact in a non-name opening;
  isConnClosedErr reads "Checks whether the given error is expected teardown
  of the connection." (b3-9)
- `internal/api/handlers_test.go:18-19`,
  `internal/api/handlers_test.go:82-84`, `internal/api/handlers_test.go:94-95`
  (compressed doc). test helper docs use parameter names as nouns ("on auth",
  "for items", "once mgr lists it as pending", "after timeout"), which the
  style rules forbid; the lines are not changed lines (startPending's body
  line 101 is)
  Fix: say "the given auth", "the given items", "once the given manager lists
  it as pending", "after the given timeout" (b4-7)
- `internal/api/handlers.go:274-276`, `internal/api/handlers.go:287-288`
  (compressed doc). respondJSON's doc says "Writes v as …" and decodePost's
  "Decodes a POSTed JSON body into body, or writes the error response and
  returns false": parameter names as nouns, and decodePost's bool return is
  unnamed and its 405 for a non-POST method is not stated; not changed lines
  Fix: respondJSON: "Writes the given value as a JSON response with the given
  status code. Errors when the value cannot be encoded, and then writes
  nothing." decodePost: "Decodes a POSTed JSON body into the given value.
  Responds with 405 for another method and 400 for a body that does not
  decode, and then returns false." (b4-8)

### cli

- `internal/cli/client.go:31-118` (split protocol). cli.ItemInfo,
  RequestRecord, PendingEntry, HistoryEntry, PendingResponse and
  HistoryResponse mirror api.ItemInfo, RequestRecord, PendingRequest,
  HistoryEntry, PendingListResponse and HistoryResponse by hand, so each JSON
  key is spelled in two Go packages; the mirror already lags (no collection,
  no auto_approvable), and ItemInfo.Path stays a string that format.go
  converts to dbus.ObjectPath.
  Fix: Options: cli decodes into the api types (cli_test already imports api)
  and adds its methods on a local wrapper; or the wire types move to a package
  that api and cli both import. Either way one spelling per key, and Path
  becomes dbus.ObjectPath. (b1-16)
- `internal/cli/client.go:46-58`, `internal/cli/format.go:153-170`,
  `internal/cli/format.go:274-284` (algebraic blindness). cli.GPGSignInfo is
  flat: TagName and Target matter only for a tag, Pushee only for a push,
  ChangedFiles, ParentHash and Committer only for a commit. It hand-mirrors
  approval's wire form (gpgSignInfoWire, internal/approval/gpgsign.go:139),
  though cli imports approval, whose GPGSignInfo is a sum over ObjectDetails
  with UnmarshalJSON. Summary and the changeset's writeSignInfo each switch on
  Kind.
  Fix: cli decodes into approval.GPGSignInfo, and the per-kind writers and
  summaries hang off its details through an exported interface method rather
  than a Kind switch (the owner forbids type switches on sealed sums); or a
  cli-local sum with the same shape. (b1-17)
- `internal/cli/format.go:18-25` (boolean blindness). Formatter.asJSON picks a
  branch at the top of each of its four methods (FormatPending, FormatEntry,
  FormatHistory, FormatDecision).
  Fix: A Formatter interface with jsonFormatter and textFormatter
  implementations; main.go picks one from the --json flag. (b1-18)
- `internal/cli/client.go:1` (ambiguous noun). The package doc "provides a
  client for the secrets-dispatcher API" is stale since the changeset moved
  the API client into apiclient; cli now holds the CLI commands' requests and
  their output.
  Fix: A package doc for what cli is now (the CLI's reads and decisions over
  the daemon API, and their output), or none. (b1-19)
- `internal/cli/format_test.go:91-95`, `internal/cli/format_test.go:135-139`,
  `internal/cli/format_test.go:151-155` (duplication). Ten tests build a
  strings.Builder, a text Formatter and call one Format method with the same
  four lines.
  Fix: helpers formatEntry(t, Entry) string and formatPending(t,
  []PendingEntry) string with require.NoError (b1-26)

### gpgsign

- `internal/gpgsign/run.go:93-99` (unparsed input). the
  SECRETS_DISPATCHER_REMOTE_SOCKET env var is read in thinClient.sign, two
  calls below Run, while SECRETS_DISPATCHER_DEBUG is read at the edge in Run
  (unchanged lines)
  Fix: Run reads both env vars into thinClient (debug, remoteSocket
  unixsock.Path), and sign branches on the field (b2-10)

### proxy

- `internal/proxy/prompter.go:128-133`, `internal/proxy/prompter.go:123-125`
  (duplication). prompterBridge.close repeats unexportCallback's body (Export
  nil, delete from callbacks) in its loop; the changeset changed close's
  signature but left the copy
  Fix: one unexportLocked(path) used by both, or close calls the unexport
  under the held lock (b10-14)
- `internal/proxy/prompter.go:142-145` (speculative generality).
  prompterCallback.logger is set in exportCallback and never read; the
  changeset rewrote the type's doc
  Fix: delete the field (b10-15)
- `internal/proxy/prompter_test.go:70-76`,
  `internal/proxy/prompter_test.go:144-150` (duplication). both bridge tests
  build a mockShellPrompter with the same three buffered channels, export it
  and claim the prompter name
  Fix: a startShellPrompter(t, frontAddr) helper returning the connection and
  the mock (b10-17)
- `internal/proxy/proxy.go:263-268` (speculative generality). Run's nil check
  on frontConn.Context() is dead: godbus's Conn.Context always returns a set
  context (conn.go:291-294)
  Fix: delete the fallback and its comment (b10-18)
- `internal/proxy/session.go:183-187` (duplication). CloseAll copies the map
  into a new one and then replaces the original; taking the old map and
  installing a fresh one is the same swap without the copy
  Fix: sessions := m.sessions; m.sessions = make(...) under the lock (b10-19)
- `internal/proxy/proxy.go:69-70`, `internal/proxy/proxy.go:75`,
  `internal/proxy/proxy.go:100-101` (garbled sentence). the NewFactory, Claim
  and Claim.Dial docs use parameter names as nouns ('serve upstream', 'with
  chains', 'Claims client', 'the bus that front opens'), which the style rules
  forbid
  Fix: say 'the given upstream', 'the given chain reader', 'the given client',
  'the bus that the given connector opens' (b10-20)
- `internal/proxy/collection.go:33`, `internal/proxy/collection.go:62`,
  `internal/proxy/collection.go:86` (restated code). Fifteen "Signature: X(…)
  -> (…)" doc lines restate the D-Bus signature that the Go signature, the
  Secret Service spec and the introspection XML already state. The changeset
  rewrote the line above each but left these.
  Fix: delete the Signature lines (b11-11)
- `internal/proxy/collection.go:34-59`, `internal/proxy/item.go:33-58`
  (duplication). CollectionHandler.Delete and ItemHandler.Delete are the same
  25 lines apart from the info builder, the interface and the log method, and
  the approval block (start caller, build RequestInfo, requireApproval, log
  denied, AccessDenied) recurs in CreateItem, GetSecret, SetSecret and
  GetSecrets.
  Fix: One approvalCaller method that runs the request and returns the
  upstream call context or the D-Bus error to reply with, built together with
  the per-call outcome type of b9-22. (b11-15)
- `internal/proxy/item.go:79-83`, `internal/proxy/service.go:86-90` (repeated
  derivation). GetSecret and GetSecrets each look the client session up and
  build dbustypes.ErrSessionNotFound(string(session)) twice, once to log and
  once to return.
  Fix: A SessionManager method that returns the local session or the NoSession
  D-Bus error; both sites log and return that one value. (b11-16)
- `internal/proxy/collection.go:121` (restated code). The comment restates the
  next line ("Cache the new item path") and names an in-repo-external caller
  as the reason ("e.g., gh verification").
  Fix: Keep only the reason without the caller: "A client that reads back the
  item it just created is not asked again." The string(item) argument is task
  6n0x9. (b11-17)
- `internal/proxy/manager.go:144-147`, `internal/proxy/manager.go:263`,
  `internal/proxy/manager.go:269` (repeated derivation). ClientInfo{Name,
  SocketPath} is built in three places (Clients, startProxy, serve), and serve
  takes socketPath beside inst because proxyInstance does not know its own
  socket. Unchanged lines.
  Fix: proxyInstance holds its ClientInfo, built once in startProxy; Clients,
  serve and notify* read inst.client, and serve drops its socketPath parameter
  (b12-5)
- `internal/proxy/manager.go:163-168` (inline generic machinery). Unsubscribe
  hand-rolls an index-and-splice loop. Unchanged lines.
  Fix: m.observers = slices.DeleteFunc(m.observers, func(o ClientObserver)
  bool { return o == obs }) or slices.Index + slices.Delete (b12-6)
- `internal/proxy/manager.go:85`, `internal/proxy/manager.go:106`,
  `internal/proxy/manager.go:111` (restated code). Body comments that say what
  the next line does ("Create the sockets directory if it doesn't exist", "Get
  PID", "Decode the unit path to get the unit name", "Start goroutine to
  process signals", "Cancel all pending contexts"). Unchanged lines.
  Fix: delete them; keep the ones with a local reason (senderinfo.go:66 logind
  remark, tracker.go:155 why signals is not closed) (b12-7)
- `internal/proxy/senderinfo.go:35-38`, `internal/proxy/senderinfo.go:75`
  (algebraic blindness). SenderInfoResolver.local's doc explains its nil state
  ("Nil when the bus is forwarded"), and Resolve branches on that nil. The
  owner's rule forbids docs that explain a nil state. Unchanged lines.
  Fix: an explicit choice of resolver shape, e.g. a senderScope interface with
  localScope{*sender.ChainReader} and forwardedScope{} implementations whose
  method fills the unit and chain (or nothing); the constructor takes the
  scope; the Forwarded test builds a forwardedScope instead of nilling a field
  (b12-11)
- `internal/proxy/senderinfo.go:109-110`, `internal/proxy/unitpath.go:11-20`,
  `internal/proxy/unitpath.go:44-70` (misplaced rule). The systemd D-Bus
  protocol (bus name, object path, GetUnitByPID, the unit-path prefix and its
  escape decoding) lives in package proxy, split between senderinfo.go and
  unitpath.go, while internal/systemd owns systemd.Unit. decodeHex and
  hexValue hand-roll what strconv.ParseUint(s, 16, 8) or encoding/hex does.
  DecodeUnitPath takes a string where its only caller holds a dbus.ObjectPath.
  Unchanged code lines.
  Fix: internal/systemd gains UnitFromObjectPath(dbus.ObjectPath) (Unit, bool)
  decoding with encoding/hex, and the systemd1 names as constants; proxy's
  realDBusClient.unitByPID calls it; unitpath.go and its hex helpers go
  (b12-13)
- `internal/proxy/subtree.go:12-14` (speculative generality).
  SubtreePropertiesHandler.sessions and .logger are never read, and localConn
  holds the backend connection (proxy.go:193 passes h.backendConn). Unchanged
  lines.
  Fix: drop sessions and logger from the struct and constructor, rename
  localConn to backendConn; moot if task xkve4 merges the type into properties
  (b12-16)
- `internal/proxy/tracker.go:76-92` (unparsed input). processSignals reads the
  NameOwnerChanged body by position (Body[0..2] with three type assertions)
  and writes the "a unique name lost its owner" rule inline (name[0] == ':'
  and old != "" and new == ""). The same positional parsing appears in
  internal/notification/desktop.go:202, :215. Unchanged lines.
  Fix: a nameOwnerChange{name, oldOwner, newOwner} parsed from the signal in
  one function (or dbus.Store into it), with a disconnected()
  (sender.UniqueName, bool) method; the ':' test becomes a sender.UniqueName
  method (b12-20)
- `internal/proxy/tracker_test.go:19-40`,
  `internal/proxy/tracker_test.go:68-92`,
  `internal/proxy/tracker_test.go:136-157` (repeated derivation). Each of the
  four tracker tests repeats the same setup (start daemon, connect tracker
  conn, newClientTracker, connect client, uniqueName) with its narrating
  comments. Unchanged lines.
  Fix: one helper returning {tracker, clientConn, clientName}, registering
  cleanups with t.Cleanup (b12-23)
- `internal/proxy/senderinfo_test.go:141-158`,
  `internal/proxy/senderinfo_test.go:160-169` (speculative generality).
  TestSenderInfoResolver_Resolve_PartialInfo builds an approval.SenderInfo
  literal and asserts its own fields back, calling nothing under test;
  TestNewSenderInfoResolver only checks non-nil. Unchanged lines.
  Fix: delete both tests (b12-25)

### notification

- `internal/notification/desktop_test.go:146` (restated code). "Zero delay for
  existing tests — notifications fire immediately." narrates history
  ("existing tests") and restates the 0 argument on the next line.
  Fix: delete the comment (or fold into 39k8m's newTestHandler =
  newTestHandlerWithDelay(0)) (b9-4)

### sshagent

- `internal/sshagent/proxy_test.go:68-71`,
  `internal/sshagent/proxy_test.go:146-149`,
  `internal/sshagent/proxy_test.go:88-98` (duplication).
  TestProxyAgent_SignApproved and TestProxyAgent_SignDenied repeat the same
  setup (keyring, key, NewManager{5s, 100}, newProxyAgent, List), the same
  signResult type, the same Sign goroutine and the same hand-rolled poll loop
  for a pending request. The changeset edited both copies the same way (the
  List call at :68-71 and :146-149, mgr.Pending at :91 and :168). Other
  packages wait with require.Eventually (internal/api/remote_test.go:70).
  Fix: A helper that builds the keyring, manager and proxy and returns the
  proxy, the manager and the listed key, and a waitPending(t, mgr) built on
  require.Eventually. Both tests call them, with testify checks. (b8-11)
- `internal/sshagent/proxy.go:73` (request-scoped value threaded).
  signWithApproval passes context.TODO() to Manager.RequireApproval, though
  the proxyAgent is built per connection in Server.handleConnection, which
  holds the connection's ctx (server.go:70, :97). A sign request that waits
  for approval is not cancelled when the daemon shuts down.
  Fix: newProxyAgent takes the connection's context, and signWithApproval
  passes it to RequireApproval (b8-12)
- `internal/sshagent/proxy.go:87-100` (narrating doc comment).
  findKeyComment's doc "Returns the comment of key, matched by its blob
  against the cached key list" uses the parameter name as a noun and states
  the mechanism. The body hashes the given key and every listed key with
  sha256 to compare them, though agent.Key carries the Blob and bytes.Equal
  compares it directly. Only the loop header was changed.
  Fix: `if bytes.Equal(k.Blob, blob)` in place of the two sha256 sums; doc
  "Returns the comment that the latest List gave the given key." (b8-13)
- `internal/sshagent/proxy.go:23-25` (algebraic blindness).
  proxyAgent.destination is a *string whose doc explains its nil state ("nil
  when it is not known"), which the style rules forbid. approval.SSHSignItem
  takes the same pointer.
  Fix: An empty string (or a named sshDestination type whose zero value means
  unknown, with SSHSignItem taking it), so the doc goes (b8-14)

### tests (mock and root)

- `internal/testutil/mockservice.go:1`, `internal/testutil/mockservice.go:13`,
  `internal/testutil/mockservice.go:27` (doc opens with its name). 25 docs in
  mockservice.go open with their declaration's name, and most are tautologies
  ("Lock locks the collection", "ItemCount returns the number of items", "Get
  implements Properties.Get"); the package doc lists what it provides; the
  Unlock doc phrases a precondition as "the caller must invoke Prompt()";
  locked has a trailing sentence comment
  Fix: delete the tautologies; keep only the (b) facts (SearchItems puts
  matches in the locked list while the collection is locked; Unlock returns a
  prompt instead of unlocking while locked, as gnome-keyring does; GetSecret
  errors IsLocked while locked) in verb-first form; field comment above the
  field (b13-6)
- `internal/testutil/mockservice.go:55`,
  `internal/testutil/mockservice.go:60`, `internal/testutil/mockservice.go:65`
  (restated code). `//` comments restate the next line ("Export Service
  interface", "Request the bus name", "Extract label from properties", "Create
  new item"); 381 and 389 sit directly above the changed lines 383 and 391
  Fix: delete them; 381 and 389 go away with b13-1's parse call (b13-7)
- `internal/testutil/mockservice.go:398-411`,
  `internal/testutil/mockservice.go:426-429`,
  `internal/testutil/mockservice.go:438-441` (inline generic machinery).
  CreateItem's replace loop spells exact map equality as matchesAttributes
  plus a length check (maps.Equal) and finds and mutates in one body, with an
  Unlock on two paths; mockCollection.Get and GetAll each collect the keys of
  service.items by hand (slices.Collect(maps.Keys))
  Fix: a MockSecretService.itemWithAttributes(attrs) lookup using maps.Equal,
  then the update under one deferred lock; one itemPaths() method using
  slices.Collect(maps.Keys) for Get and GetAll (b13-9)
- `internal/testutil/mockservice.go:147-152`,
  `internal/testutil/mockservice.go:486-491` (repeated derivation). GetSecrets
  and mockItem.GetSecret build the same dbustypes.Secret from a MockItem and a
  session
  Fix: a MockItem.secret(session) method used by both (b13-10)
- `internal/testutil/mockservice.go:199`,
  `internal/testutil/mockservice.go:202`,
  `internal/testutil/mockservice.go:209` (split protocol). the mock answers
  Properties.Get by bare names ("Label", "Locked", "Items", "Attributes",
  "Collections") while the tests read them as dbustypes.CollectionLabel,
  ItemLabel, ItemAttributes and Interface.Member(...); the error name
  "org.freedesktop.DBus.Error.UnknownProperty" is spelled three times and is
  missing from dbustypes.ErrorName
  Fix: an UnknownProperty ErrorName in dbustypes used by all three sites; the
  property switches keyed on the member names the tests use, from one per-
  object table that Get and GetAll share (fixes b13-8 too) (b13-11)
- `proxy_test.go:454-493`, `proxy_test.go:495-540`, `proxy_test.go:599-641`
  (duplication). TestProxyItemDelete-, CollectionDelete-, SetSecret- and
  CreateItemRequiresApproval each hand-write the shape that the table-driven
  TestProxyApprovalRequestCalls runs for the same six calls (goroutine call,
  waitPendingID, GetPending, type check, resolve, wait with timeout);
  approval.ManagerConfig{Timeout: 30s, HistoryMax: 100} is written nine times
  Fix: fold the approve path into TestProxyApprovalRequestCalls' table
  (approve then deny then disconnect per call) and delete the four tests; one
  newApprovalManager(t, opts) helper for the config (b13-12)
- `proxy_test.go:50`, `proxy_test.go:66-68`, `proxy_test.go:91` (garbled
  sentence). test helper docs use parameter names as nouns ("Connects p to…",
  "a proxy deciding through mgr", "through serviceObj", "in mgr's pending
  list"); waitPendingID was touched at 112 but its doc line was not
  Fix: "the given proxy", "the given manager", "the given service object"; or
  delete openPlainSession's doc, which restates its name (b13-13)
- `internal/testutil/mockservice.go:52-53`,
  `internal/testutil/mockservice.go:290-291`,
  `internal/testutil/mockservice.go:331` (temporal coupling). conn is set by
  Register, so AddItem or a locked Unlock before Register dereferences nil;
  AddItem and newPrompt drop the errors of conn.Export
  Fix: NewMockSecretService(conn) registers and returns the mock (one
  constructor), and AddItem/newPrompt report Export failures (t.Fatal through
  a testing.TB the mock holds, or a returned error) (b13-14)

### companion and daemon

- `internal/companion/check.go:12-16` (algebraic blindness).
  CheckResult.Message holds a confirmation when Pass is true and a fix hint
  otherwise, as its trailing comment explains. A result is really one of two
  cases, a pass or a failure. Name's trailing comment restates the field name.
  The changeset touched this struct (the type doc and the Pass comment).
  Fix: CheckResult{Name string, Fix string}, where an empty Fix means pass.
  Or, better, a Passed/Failed pair of variants with String methods. Delete the
  trailing comments. (b14-15)
- `internal/companion/check.go:34-121` (long function). Check is 88 lines with
  numbered section comments. The same directory-exists check (checkResult with
  "directory %q exists" and provisionHint) is written three times (lines 56,
  77, 85), and a fourth time for the unit file.
  Fix: Add a Config.checkDirExists(name string, d companionDir) CheckResult
  helper. Check becomes a list of named checks with no numbered comments.
  (b14-16)
- `internal/daemon/dispatcher.go:14`, `internal/daemon/dispatcher.go:16`
  (split protocol). BusName (an untyped string) and Interface spell the same
  literal "net.mowaka.SecretsDispatcher1". This is unchanged code.
  Fix: Interface is declared from BusName (`Interface =
  dbustypes.Interface(BusName)`), or the reverse, and the const block's
  restating comment goes. (b14-19)
- `internal/daemon/daemon.go:26-79` (mixed altitude). daemon.Run interleaves
  its steps (connect, export, request the name, notify) with detail: the
  introspect node literal and the bus choice. Its body comments restate each
  call ("// Request the well-known bus name."), and one comment names its
  caller ("SIGTERM/SIGINT handled by caller"). This is unchanged code.
  Fix: Extract an exportDispatcher(conn, d) helper that holds the
  introspection. Delete the restating comments. (b14-20)

### main and config

- `internal/config/config.go:69` (repeated derivation). The
  AutoApproveDuration default is applied twice: in ServeConfig.withDefaults
  (config.go:69) and again in approval.NewManager (cmp.Or with
  DefaultAutoApproveDuration). The ManagerConfig doc repeats "Zero means
  DefaultAutoApproveDuration". This is unchanged code beside the new
  ManagerConfig().
  Fix: Keep one owner. Either NewManager stops defaulting and its doc loses
  the zero sentence, with tests passing the duration, or config stops filling
  the default. (b14-14)
- `main.go:511-663` (mixed altitude). serveCmd.Run is 150 lines that mix named
  steps with inline detail and decisions: the notifier setup with co-dependent
  nil pointers (desktopNotifier and notifHandler), the chainEnd choice, the
  SSH-upstream check, API socket setup, a shutdown defer and the runner loop.
  The changeset extracted only ManagerConfig and setDefaultLogger.
  Fix: Extract named builders: newNotifications(settings) returning one
  optional value that holds both the notifier and its handler,
  chainReader(settings), and sshRunner(settings). Run then reads as its steps.
  (b14-21)

### web

- `web/src/lib/HistoryEntry.svelte:144-158`, `web/src/App.svelte:486`,
  `web/src/App.svelte:813-821` (duplication). The request-type label is
  written in three more places beside RequestCard.typeBadgeLabel (task rt113),
  and they disagree: App.svelte:486 labels unlock and ssh_sign as Secret,
  App.svelte:813-821 capitalizes the raw type. Unchanged lines.
  Fix: One `requestTypeLabels: Record<RequestRecord["type"], string>` in a lib
  module, used by all four sites. (b15-9)
- `web/src/lib/HistoryEntry.svelte:46-78` (duplication). formatSenderInfo is
  RequestCard's formatSenderInfo plus a repo prefix, with the same narrating
  'Fall back to' comments and the same dead `if (!info)` guard on a required
  field. Unchanged lines; rt113 lists only the RequestCard copy.
  Fix: One senderSummary(request) in sender.ts used by both; HistoryEntry adds
  its repo prefix around it. (b15-10)
- `web/src/App.svelte:698-733`, `web/src/App.svelte:746-781` (duplication).
  The Recent Activity section is written twice, once under the empty state and
  once under the pending list, and its chevron SVG a third time for Trust
  Rules (App.svelte:788-801). Unchanged lines.
  Fix: Render the history section once after the requests-or-empty-state
  branch (a Svelte snippet or a HistorySection component), with a shared
  chevron toggle. (b15-11)
- `web/src/lib/websocket.ts:13-23`, `web/src/lib/websocket.ts:153-163`
  (parameter bloat). onSnapshot takes nine positional values, and
  App.svelte:113-123 renames each one again. The auto-approve duration default
  120 is written in websocket.ts:161 and App.svelte:63. Unchanged lines.
  Fix: onSnapshot receives one Snapshot record (the parsed WSSnapshotMessage
  with its defaults applied in one place); App assigns from its fields.
  (b15-12)
- `web/src/lib/websocket.ts:222-238` (inline generic machinery).
  ApprovalWebSocket hand-rolls exponential backoff (reconnectDelay,
  maxReconnectDelay, the reset at line 74, the doubling at 235) beside its
  connection logic, with narrating comments. Task kf361 covers only the Go
  session-bus runner. Unchanged lines.
  Fix: A small Backoff class (next(), reset()) in web/src/lib, with its own
  test, used by ApprovalWebSocket. (b15-13)

### web tests

- `web/tests/gpg-sign.spec.ts:26-33`, `web/tests/gpg-sign.spec.ts:53-56`
  (duplication). gpg-sign.spec.ts spells out a full commit literal at every
  createGPGSignRequest call (17 sites, the ident "Dev <dev@example.com>
  1700000000 +0000" 22 times in web/tests). It also repeats the deny cleanup
  fetch of /api/v1/pending/${reqId}/deny 13 times. focus-request.spec.ts:50-56
  has that cleanup as denyRequest, and request-id.spec.ts:45 has one more
  copy.
  Fix: gpg-sign tests build their commits with the testCommit builder from
  b16-3, passing only the fields each test checks. A
  TestBackend.deny(requestId) method in test-utils.mts replaces the 13 inline
  fetches, focus-request's denyRequest and request-id.spec.ts:45. (b16-4)
- `web/tests/focus-request.spec.ts:297-303`, `web/tests/focus-
  request.spec.ts:311` (duplication). The expiration test installs the
  window.close spy inline and reads window.__closeCalled inline. It copies
  spyWindowClose (lines 74-81) and wasWindowCloseCalled (lines 84-86) from the
  same file.
  Fix: the test calls spyWindowClose(page) and wasWindowCloseCalled(page)
  (b16-7)

Found by the 2026-11-22 20:25 unsmell run (`.git/unsmell/20261122-202523`).

### follow-ups of the run's own fixes

- `internal/proxy/session.go` sessionHandler.Close (missing check). A client's
  Session.Close closes any client session path it names, without checking that
  the caller opened it; ForClient and ForUpstream do not check ownership
  either. Fix: SessionManager records the opening sender per client session,
  and Close, ForClient and ForUpstream refuse another sender's session with
  AccessDenied. (p-bugs review, 2026-11-22)
- `internal/proxy/collection.go:63`, `internal/proxy/service.go:44`,
  `internal/logging/audit.go:65`, `:88` (primitive obsession). The SearchItems
  handlers, Logger.LogSearchItems, LogCollectionSearchItems and the mock's
  SearchItems and AddItem still take the attribute dict as map[string]string,
  beside the run's dbustypes.Attributes.
  Fix: they take dbustypes.Attributes (convert at the godbus method's first
  line if godbus will not decode into the named type). slog then prints the
  attributes through Attributes.String, which changes the log format. (r2-4)
