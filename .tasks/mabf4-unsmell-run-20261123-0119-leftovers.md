---
status: todo
tags: [unsmell, test]
---
# Leftovers of the 2026-11-23 01:19 unsmell run

## Goal
Done when each item below is fixed as its "Fix" says. Each sits on lines that
the run did not change, or needs a decision, so the run planned it instead of
fixing it.

### approval

- `internal/approval/manager_test.go:548`,
  `internal/approval/manager_test.go:1740` (duplicated knowledge). "The expiry
  records the entry after it wakes RequireApproval." is written twice. It is a
  contract fact of Await/RequireApproval (manager.go resolve closes done before
  publishEnd records History), yet neither doc says that History may lack the
  entry when the call returns; at HEAD
  TestManager_RequireApproval_ExpiryEndsRequestOnce read History without
  waiting. Fix: Await's doc (manager.go) states once that History and observers
  get the end after the call returns; the test comments go (one of the two
  tests goes with b1-13). manager.go is outside this batch. (b1-12)
- `internal/approval/manager_test.go:1021`,
  `internal/approval/manager_test.go:1069-1098`,
  `internal/approval/manager_test.go:1324-1325` (stringly-typed control flow).
  Trust rules in the tests spell Action as "approve"/"ignore"/"deny" and
  RequestTypes as "search"/"write"/"get_secret" where RuleApprove, RuleDeny and
  the RequestType constants exist (and later tests in the same file use them).
  Mostly unchanged lines. Fix: use the typed constants (RuleApprove,
  RuleIgnore, RuleDeny, RequestTypeSearch, ...) (b1-20)
- `internal/approval/manager_test.go:879-883`,
  `internal/approval/manager_test.go:911-915`,
  `internal/approval/manager_test.go:1362-1365` (duplication). The Chrome dummy
  item (default collection path, chromeDummySchema attribute) is written three
  times. Fix: one chromeDummyItems() helper used by the three tests (b1-21)
- `internal/approval/gpgsign_test.go:141-157` (oversized lock). mu guards only
  the ids slice, appended to from n goroutines; writing ids[i] by index needs
  no lock, and wg.Go replaces Add/Done. The uniqueness loop with a seen map
  (162-166, changed by the changeset) is a hand-rolled dedup. Fix: ids :=
  make([]RequestID, n); for i := range n { wg.Go(func() { ids[i] =
  mgr.Create(...) }) }; then assert.Len(t,
  slices.Compact(slices.Sorted(slices.Values(ids))), n). (b2-13)
- `internal/approval/gpgsign_test.go:184`,
  `internal/approval/gpgsign_test.go:187`,
  `internal/approval/gpgsign_test.go:161` (restated code). "Must NOT appear in
  pending.", "History should record auto_approved resolution." and "All IDs
  must be unique." read the next assertion back; the test doc already states
  them. Fix: delete the three comments. (b2-14)

### api

- `internal/api/websocket_test.go:307`, `internal/api/websocket_test.go:322`
  (primitive obsession). AutoApproveRule.InvokerName is a plain string
  (manager.go:319) while SenderInfo.Client is approval.ClientName; the
  changeset's asserts compare it to the bare "gh". Fix: a named InvokerName
  type in approval, used by SenderInfo.InvokerName and
  AutoApproveRule.InvokerName (b3-14)
- `internal/api/handlers_test.go:79-90`, `internal/api/remote_test.go:64-69`,
  `internal/api/remote_test.go:121` (duplication). "Poll Manager.Pending until
  it lists n requests" is written in startPending, remoteHarness.waitPending
  and approval's new waitPending (setup_test.go:31). The api copies are
  unchanged lines. Fix: One exported fixture in internal/approval/fixtures.go
  (or a Manager wait method for tests) that both packages call. (b3-15)
- `internal/api/jwt_test.go:254`, `internal/api/jwt_test.go:258`,
  `internal/api/jwt_test.go:261` (restated code). Step comments ("// Generate a
  JWT", "// Validate the token", "// Test with valid auth", "// Multiple calls
  should return same value", "// Approve the request") restate the line below
  or the t.Run name. Unchanged lines. Fix: delete them (b3-16)
- `internal/api/auth_test.go:26-38`, `internal/api/jwt_test.go:360-379`
  (duplication). TestNewAuth and TestLoadAuth both check that a second
  construction on one state dir reuses the stored token; JWT tests are split
  across auth_test.go and jwt_test.go. Unchanged lines. Fix: merge into one
  persistence test in jwt_test.go (or auth_test.go) (b3-17)
- `internal/api/handlers_test.go:95-98`,
  `internal/api/handlers_test.go:115-118`,
  `internal/api/handlers_test.go:126-129` (duplication). Fifteen tests build
  httptest.NewRequest, httptest.NewRecorder and call one handler method in the
  same three lines; postGPGSign is the one named copy. These lines are
  unchanged. Fix: A serve(t, handler http.HandlerFunc, method, path string)
  *httptest.ResponseRecorder helper beside b3-1's decodeResponse; postGPGSign
  is built on it. (b3-3)

### notification and gpgsign

- `internal/notification/desktop_test.go:27-28`,
  `internal/notification/desktop_test.go:41-43`,
  `internal/notification/desktop_test.go:56-58` (speculative generality).
  mockNotifier.notifyErr and closeErr are never set by any test, at HEAD
  either, so their checks in Notify and Close are dead. These lines are
  unchanged. Fix: Delete both fields and their checks (b4-14)
- `internal/notification/desktop_bus_test.go:13-14`,
  `internal/notification/desktop_bus_test.go:26-27` (doc opens with its name).
  The notifyArgs and stubNotificationServer docs open with their own names. The
  stub's doc adds "the in-package version of the fast-tier stub", which names
  another layer's fixture. These lines are unchanged. Fix: notifyArgs: "One
  org.freedesktop.Notifications.Notify call as the server saw it on the wire."
  stubNotificationServer: "Owns org.freedesktop.Notifications on a private bus
  and records Notify calls." (b4-16)
- `internal/gpgsign/daemon_test.go:24-31`,
  `internal/gpgsign/daemon_test.go:41-47`,
  `internal/gpgsign/daemon_test.go:66-78` (duplication). Each DaemonClient test
  builds an httptest server around its handler, defers Close and wraps it with
  newTestClient(srv). The same three steps appear five times. These lines are
  unchanged by the changeset. Fix: newTestClient(t, handler http.HandlerFunc)
  *DaemonClient starts the server, registers its Close with t.Cleanup and
  returns the client. (b4-4)

### proxy, procutil, dbus and daemon

- `proxy_test.go:154`, `proxy_test.go:166`, `proxy_test.go:178` (restated
  code). Comments such as "// Test: ReadAlias" above t.Run("ReadAlias"), "//
  Approve the request", "// Delete should succeed" and "// Create the tracker"
  restate the line below them. These lines are unchanged. Those in
  tracker_test.go go with b5-7. Fix: Delete them. TestProxyAliasPathOperations
  is left out, since task w2xk8 plans work there. (b5-16)
- `proxy_test.go:46`, `proxy_test.go:62-64`, `proxy_test.go:83` (compressed
  doc). The docs of connectProxy ("Connects p to ..."), startProxy ("a proxy
  deciding through mgr"), openPlainSession ("through serviceObj") and
  openDHSession use parameter names as nouns. openDHSession also returns an
  unnamed (*dhcrypto.Session, dbus.ObjectPath) pair. These lines are unchanged.
  Fix: Use "the given proxy", "the given manager" and "the given service
  object" in the docs. Optionally give openDHSession a named return struct.
  (b5-17)
- `internal/proxy/senderinfo_test.go:168-175`,
  `internal/procutil/procutil_test.go:114-124` (duplication). Both tests check
  that the first /proc chain entry has an Exe, Args and a CWD, with differently
  worded messages ("an Exe" against "expected an Exe in first ProcessChain
  entry"); the changeset rewrote both. The rule belongs to procutil.Chain; the
  proxy test re-checks procutil. Fix: The proxy test asserts that
  info.ProcessChain equals what the resolver's ChainReader returns for the PID
  (or only that it is non-empty), and the field checks stay in procutil_test.
  Alternative is an exported checker, which needs a non-test file. (b6-10)
- `internal/procutil/procutil_test.go:18-19`,
  `internal/proxy/senderinfo_test.go:17-20` (duplicated knowledge). Two
  packages define the same absent PID (1 << 30) with the same PID_MAX_LIMIT
  fact in their comments; unchanged lines. Fix: One exported
  procutil.AbsentPIDForTest in a non-test file, as approval/fixtures.go does
  for senders, used by both. (b6-11)
- `internal/dbus/types_test.go:13-16`, `internal/dbus/types_test.go:35-38`
  (duplication). type parsed struct{ref CollectionRef; ok bool} is declared in
  both TestCollectionOf and TestParseCollectionAndItemPath; unchanged lines.
  Fix: One file-level parsed type (or a parsedOf(ref, ok) helper) shared by
  both tests. (b6-12)
- `internal/proxy/prompter_test.go:68-75`,
  `internal/proxy/prompter_test.go:142-149` (duplication). Both bridge tests
  connect to the front bus, build the same three-channel mockShellPrompter,
  Export it and claim systemPrompterName; and promptCall.sender is a string
  carrying a bus unique name although sender.UniqueName exists (senderOf
  returns one). Unchanged lines. Fix: A startMockShellPrompter(t, frontAddr)
  *mockShellPrompter helper, and promptCall.sender typed sender.UniqueName.
  (b6-13)
- `internal/daemon/daemon_test.go:22-29`,
  `internal/daemon/daemon_test.go:55-57`,
  `internal/daemon/daemon_test.go:106-107` (doc opens with its name). Every doc
  in daemon_test.go but waitForName's opens with its own name;
  policyConfigTemplate's is 8 lines and cites "Pitfall 1", which nothing in the
  repo defines; the body comments "Verify Ping.", "Verify GetVersion." restate
  the code. Unchanged lines. Fix: Docs rewritten by "A doc carries the design"
  (policy template keeps only why the default block is complete, with its
  record), restating comments deleted. (b6-14)
- `proxy_test.go:644`, `proxy_test.go:688`, `proxy_test.go:988` (split
  protocol). proxy_test.go (b5) spells approval's unexported chromeDummySchema
  "_chrome_dummy_schema_for_unlocking" twice, and the text of
  approval.ErrDeniedByRule plus the rule name ("access denied by trust rule:
  deny-search") three times, which manager_test.go (b1, :1628) also spells.
  Both must agree with approval's constants and its wrapping format. Fix:
  Export the schema (approval.ChromeDummySchema) for the proxy test, and have
  the denied-by-rule checks build the expected text from ErrDeniedByRule (or
  check errors.Is plus the rule name where the error crosses no bus). (x-14)

### service, config and dhcrypto

- `internal/service/install_test.go:41`,
  `internal/service/install_test.go:111`,
  `internal/service/install_test.go:114` (restated code). Section comments
  restate the next line ("Install in local mode.", "Re-read config.", "Switch
  to remote.", "Proxy unit should exist." above a read), "Pre-create all 4 unit
  files" repeats len(allUnitNames), and captureStdout's doc opens with its own
  name. All on unchanged lines. Fix: Delete the restating comments; drop the
  count; captureStdout doc becomes "Returns what fn prints to os.Stdout."
  (b7-10)
- `internal/service/units_test.go:18` (algebraic blindness). unitCall's doc
  explains an empty value ("unit is empty for opReload"), the user's rule
  against docs about empty values; unchanged line. Fix: Let the reload call
  carry no unit by type, or drop the clause; e.g. record reload as unitCall{op:
  opReload} and delete the doc clause. (b7-11)
- `internal/service/install_test.go:116`,
  `internal/service/install_test.go:137`,
  `internal/service/install_test.go:142` (duplication). Unchanged lines left by
  the testify migration ignore errors: UnitFile.Read results are taken with
  `content, _ :=` although readFile(t, f.File) exists (managedfile_test.go:59),
  and UnitFile.Write errors are dropped. In TestUninstall a failed Write would
  make assert.False(unit.Exists()) pass vacuously. Fix: readFile(t,
  dir.UnitFile(u).File) for reads, require.NoError around each Write. (b7-9)
- `internal/config/config_test.go:18-20`,
  `internal/config/config_test.go:54-56`,
  `internal/config/config_test.go:76-78` (repeated derivation). Fourteen tests
  repeat the same setup. They write YAML to filepath.Join(t.TempDir(),
  "config.yaml") and then call File{path}.Load(). Nine sites drop
  os.WriteFile's error (config_test.go:20,56,78,87,154,298,307,332,344 and
  statedir_test.go:84). The setup already has a name, but only as the local
  closure `load` in TestLoadAutoApproveDurationSign. These setup lines are not
  changed lines. The changeset only rewrote the assertions after each Load.
  Fix: Add a package test helper loadYAML(t, text string) (*Config, error) that
  writes the file with require.NoError and loads it. Every site and the `load`
  closure use it. The `file` variable in TestLoadRejectsInvalidGlob, which
  avoids shadowing the path package, goes away with it. (b8-6)
- `internal/dhcrypto/dhcrypto_test.go:13`,
  `internal/dhcrypto/dhcrypto_test.go:21-22`,
  `internal/dhcrypto/dhcrypto_test.go:29-32` (doc opens with its name). Six
  docs open with their own declaration's name (mustHex, keyPairFromHex,
  TestGoldenVector, TestKeyAgreement, TestEncryptDecryptRoundTrip and
  TestEncryptFreshIV). TestGoldenVector's doc runs to 4 lines.
  TestKeyAgreement's doc restates its body. None of these lines are changed.
  Fix: Drop the mustHex and TestKeyAgreement docs. Rewrite the others
  verb-first, and cut TestGoldenVector's doc to its reason: interop with
  libsecret and secretstorage through an independent reference vector. (b8-7)

### cli and companion

- `internal/companion/provision_test.go:104-111`,
  `internal/companion/provision_test.go:327-334` (duplication).
  TestProvision_CreatesUser and TestProvision_CompanionNameOverride each
  hand-write a userLookupFunc fake with a callCount that returns unknown on the
  first call and a user afterwards. Unchanged lines. Fix: A helper
  lookupCreatedOnSecondCall(uid, gid) beside mockLookupAlwaysFound. (b9-14)
- `internal/companion/provision_test.go:119-121` (long comment). A 3-line
  comment in TestProvision_CreatesUser says it verifies the --system flag is
  absent, then admits the test cannot and that it is 'architecturally
  guaranteed'. It asserts nothing and restates production code. Unchanged
  lines. Fix: Delete it. (b9-15)
- `internal/companion/check_test.go:147`,
  `internal/companion/check_test.go:155`,
  `internal/companion/check_test.go:163` (doc opens with its name). assertPass,
  assertFail, resultNames and mockLookupAlwaysFound docs open with their own
  name and restate the signature. Unchanged doc lines; check_test ones fall
  inside b9-7/b9-8's reach. Fix: Delete the docs (b9-7 and b9-8 remove the
  check_test ones); mockLookupAlwaysFound's doc goes too. (b9-16)
- `internal/companion/provision_test.go:22-24`,
  `internal/companion/provision_test.go:83-84` (restated code). fakeSystem's
  3-line doc lists every seam it swaps (restated code); fakeSessionStack's doc
  'Makes a stack file holding content the only session stack' uses the
  parameter content as a noun and reads garbled. Unchanged lines. Fix:
  fakeSystem: 'Fakes a root host with an existing companion user secrets-nb
  (1001:1001) and an empty directory tree, and returns the tree.'
  fakeSessionStack: 'Writes the given content to a stack file, makes it the
  only session stack for the test, and returns its path.' (b9-17)
- `internal/cli/format_test.go:31`, `internal/cli/format_test.go:77`,
  `internal/cli/format_test.go:120` (primitive obsession). Test fixtures spell
  approval.RequestType and approval.Resolution values as raw strings
  ("gpg_sign", "get_secret", "approved", "auto_approved", "denied") where named
  constants exist (approval.RequestTypeGPGSign, RequestTypeGetSecret,
  ResolutionApproved, ...). Unchanged lines. Fix: Use the approval constants in
  every fixture; keep raw strings only in tests that pin the wire format
  (TestPendingEntryDecodesInvokerName style). (b9-4)

### web tests

- `web/tests/auth.spec.ts:111-138` (duplication). The no-jti test hand-rolls
  the header, claims and HMAC that generateJWT builds; the changeset deleted
  the generateJWT doc sentence that said specs must not hand-roll one, and the
  jti single-use fact is stated both at test-utils.mts:70-71 and
  auth.spec.ts:114-116. Fix: generateJWT(secret, claims?) lets a caller
  override or omit claims; the test calls generateJWT(token, { jti: undefined
  }) and keeps only its own fact (why a missing jti is rejected). (b10-10)
- `web/tests/focus-request.spec.ts:32`, `web/tests/focus-request.spec.ts:39`,
  `web/tests/focus-request.spec.ts:48` (restated code). The helper docs restate
  their names in imperative form ("Deny a request via API.", "Check whether
  window.close() was called."). Fix: delete them; keep only spyWindowClose's
  (b) fact that the window stays open (b10-11)
- `web/tests/auto-approve.spec.ts:365-368`,
  `web/tests/auto-approve.spec.ts:411-414` (repeated derivation). The history
  entry for a request id is located by the same filter twice in one file (and
  request-id.spec.ts uses the same selector). Fix: a historyEntry(page, id)
  helper in test-utils.mts, used by both tests (b10-12)
- `web/tests/focus-request.spec.ts:272-290` (duplication). The expiration test
  inlines openFocused (273-275), spyWindowClose (279-284) and
  wasWindowCloseCalled (290) because those helpers close over the module
  `backend` and this test uses shortBackend. Fix: openFocused takes the backend
  (or becomes openFocused(page, id, on = backend)), and the test calls
  openFocused, spyWindowClose and wasWindowCloseCalled. (b10-6)
- `web/tests/focus-request.spec.ts:39-55`, `web/tests/approval.spec.ts:79-124`,
  `web/tests/auto-approve.spec.ts:28-31` (missing receiver). Every
  authenticated API call builds `Authorization: Bearer ${await
  backend.getAuthToken()}` by hand (about 30 sites across the specs), and
  denyRequest/cancelRequest never check res.ok, so a failed deny passes
  silently, while createGPGSignRequest on TestBackend does check it. Fix:
  TestBackend gets an authorized fetch (or Playwright request context with
  extraHTTPHeaders) plus resolve(id, "deny" | "cancel" | "approve") that throws
  on a non-ok status; specs use them. (b10-7)
- `web/tests/auto-approve.spec.ts:64-79`,
  `web/tests/auto-approve.spec.ts:161-198`,
  `web/tests/auto-approve.spec.ts:210-241` (duplication). The auto-approve rule
  literal (id, invoker_name, invoker_exe, request_type, collection, expires_at)
  is spelled ten times, each with `new Date(Date.now() + N).toISOString()`,
  while pending requests already have the pendingRequest fixture in
  test-utils.mts. Fix: test-utils.mts gets autoApproveRule(fields) with
  defaults beside pendingRequest, and an expiresIn(ms) helper shared with
  pendingRequest's expires_at. (b10-8)
- `web/tests/auto-approve.spec.ts:247-254`,
  `web/tests/auto-approve.spec.ts:281-290` (duplication). Two tests check "the
  timer ticks" the same way, a real 3s waitForTimeout and a textContent
  compare; the changeset moved Go tests off real-clock sleeps but these stay.
  Fix: Drive time with page.clock (install, then runFor) or assert with
  expect(timerEl).not.toHaveText(textBefore); share one expectTicks(timerEl)
  helper. (b10-9)
- `web/tests/notifications.spec.ts:31-36` (long comment). The
  installNotificationSpy doc runs four text lines, over the 3-line limit, and
  describes the spy's mechanism (records in window.__notificationCalls, reports
  permission). Fix: Keep the contract in up to two sentences, such as "Makes
  every document the page loads see a Notification spy with the given
  permission and visibility. notificationCalls reads what it recorded."
  (b11-12)
- `web/tests/history.spec.ts:216-234`, `web/tests/history.spec.ts:123-142`,
  `web/tests/history.spec.ts:168-190` (parameter bloat). makeHistoryEntry takes
  five positional parameters, three of them adjacent strings (id, invokerName,
  itemPath). itemPath is the same ".../collection/default/x" literal at all
  nine call sites, and type is never passed. The other history tests and
  request-id.spec.ts build the same { request: pendingRequest(...), resolution,
  resolved_at: now } literal inline. Fix: A historyEntry(resolution, fields:
  RequestFields) builder in test-utils.mts beside pendingRequest, stamping
  resolved_at; history and request-id specs use it, and makeHistoryEntry goes.
  (b11-5)
- `web/tests/history.spec.ts:51-89` (duplication). "history entry has correct
  structure in API response" runs against a backend whose log is always empty
  (the file's UI tests inject history through routeSnapshot), so the loop body
  never runs and the test passes vacuously. Its resolution list at 80-87 is a
  hand copy of Resolution in src/lib/types.ts:102-103, and the body is read
  untyped by key. Fix: Create a real request and resolve it first
  (createGPGSignRequest then deny), and type the body as { entries:
  HistoryEntry[] }; drop the resolution literal list. (b11-7)
- `web/tests/gpg-sign.spec.ts:576-598` (misleading name). The test title says
  "approve removes request from UI and adds to history" but the body checks
  only that the card disappears; the comment at 592-593 says gpg is expected to
  fail, so nothing about history is asserted. Fix: assert the history entry
  (approved or the gpg failure resolution) or retitle the test to what it
  checks (b11-8)
- `web/tests/gpg-sign.spec.ts:40-46` (primitive obsession). The GET
  /api/v1/pending body is cast to an inline shape with type: string, although
  PendingListResponse (src/lib/types.ts:88) names it. Fix: cast to
  PendingListResponse (b11-9)
- `web/tests/timestamp.spec.ts:49`, `web/tests/timestamp.spec.ts:56`,
  `web/tests/timestamp.spec.ts:62` (split protocol). The relative-time pattern
  /just now|minute|hour|day/ appears three times and the absolute-time pattern
  /\d{1,2}:\d{2}:\d{2}/ four times; each must agree with the app's formatter.
  Unchanged lines. Fix: two named constants (RELATIVE_TIME, ABSOLUTE_TIME) at
  the top of the file (b12-10)
- `web/tests/timestamp.spec.ts:8-10`, `web/tests/timestamp.spec.ts:42`,
  `web/tests/screenshots.spec.ts:268` (restated code). Comments that read the
  next line back ("Login and wait for history to appear", "Wait for both
  pending cards to appear", "Toggle to absolute", "Reload page", "Check
  localStorage", "Get status via REST API", "share one isolated backend
  instance"), and version-reload.spec.ts:38 restating authenticate's mechanism
  ("new JWT, same master token"). Some sit beside changed lines, all are
  unchanged. Fix: delete them (b12-11)
- `web/tests/screenshots.spec.ts:275-282` (concept mixing). The screenshot test
  clicks the commit-body toggle only if it is visible, though the gpg fixture
  always has a body, so a missing toggle passes silently; and it sleeps 500 ms
  for animations. Unchanged lines. Fix: Click the toggle unconditionally and
  pass animations "disabled" to page.screenshot instead of waitForTimeout.
  (b12-12)
- `web/tests/websocket.spec.ts:35-43`, `web/tests/websocket.spec.ts:95-103`,
  `web/tests/websocket.spec.ts:105-114` (misleading name). "reconnects after
  server restart" restarts nothing (it sleeps 500 ms and re-checks); "retry
  button appears on connection error" asserts the button is absent; "pending
  requests from REST API matches WebSocket snapshot" never looks at the
  WebSocket and mentions an "API-only mode"; "shows unauthenticated state
  without valid session" duplicates auth.spec.ts:29-33. Unchanged lines. Fix:
  Delete the restart and retry tests (version-reload.spec.ts covers a real
  restart), rename the pending test to what it checks, and delete the duplicate
  of auth.spec.ts. (b12-5)
- `web/tests/timestamp.spec.ts:29-35`, `web/tests/websocket.spec.ts:123-128`,
  `web/tests/websocket.spec.ts:150-155` (repeated derivation). Every
  authenticated REST call fetches getAuthToken() and spells `Authorization:
  Bearer ${token}` by hand, about 30 times across web/tests (three here, one in
  test-utils.mts:464). Unchanged lines in this batch. Fix: TestBackend.api, a
  Playwright APIRequestContext built with request.newContext({ baseURL: url,
  extraHTTPHeaders: { Authorization } }), disposed in cleanup; specs call
  backend.api.get("/api/v1/status"). (b12-6)
- `web/tests/version-reload.spec.ts:58-82`,
  `web/tests/version-reload.spec.ts:117-154` (duplication). The version test
  hand-rolls snapshot interception (routeWebSocket, JSON.parse, type ===
  "snapshot") that routeSnapshot/parseSnapshot in test-utils.mts own, and the
  snapshot-version test captures frames with addInitScript plus exposeFunction
  and casts to a hand-written { type; version? } where routeSnapshot's rewrite
  can capture the WSSnapshotMessage (as trust-rules.spec.ts:23-30 does). The
  close of the latest route ({ code 1006, "test-induced drop" }) is spelled
  twice. Unchanged lines. Fix: routeSnapshot also lets a test drop the latest
  connection (e.g. returns { send, drop }); the version test counts rewrites to
  pick the version and calls drop(); the snapshot-version test captures via
  rewrite. (b12-7)
- `web/tests/version-reload.spec.ts:4-11` (long comment). An eight-line file
  comment that narrates history ("Since sessions became server-side and
  in-memory (single-use login JWT fix)") and repeats the in-memory-session fact
  stated again at 28-30. Unchanged lines. Fix: Keep one sentence on why version
  reload is tested through WebSocket interception rather than a restart; the
  in-memory fact stays only at 28-30. (b12-8)
- `web/tests/timestamp.spec.ts:24-26` (speculative generality). beforeEach
  loads the app only to clear localStorage, but Playwright gives each test a
  fresh browser context, so storage is already empty. A dead page load per
  test. Unchanged lines. Fix: delete the goto and the localStorage.clear
  (b12-9)
- `web/tests/fixtures/test-utils.mts:464`,
  `web/tests/focus-request.spec.ts:39-55`, `web/tests/approval.spec.ts:79-124`
  (missing receiver). About 30 authenticated REST calls across all three web
  batches fetch backend.getAuthToken() and build `Authorization: Bearer
  ${token}` by hand (b10: approval, focus-request, auto-approve; b11: gpg-sign,
  history, request-id; b12: timestamp, websocket), and TestBackend's own
  createGPGSignRequest does it a fourth way. The token is TestBackend's, and
  every caller passes it back to fetch. Fix: TestBackend.api(path, init?) that
  adds the header and the base URL; the specs call it, and the few tests of the
  API's auth itself (400, 405, missing token) keep a raw fetch. (x-12)

### fixture values

- opaque "nb" fixture values on unchanged lines at internal/approval/autoapprove_test.go:256, internal/config/runtime_test.go:18, internal/sender/sender_test.go:16,31; use a neutral self-explaining value such as alice (u-1; the user rejected `nb` as a fixture name)
### bus waits

- `internal/daemon/daemon_test.go:138`, `internal/proxy/busname_test.go:36`
  (duplication). Two waits on a real dbus-daemon still spell `5*time.Second`
  beside `testutil.BusWait`: TestDaemon_NameAlreadyTaken's Run deadline and
  the name-owner Eventually in TestProxyTakesBusNameWhenHolderLeaves.
  Fix: both use testutil.BusWait. (r2-2)
