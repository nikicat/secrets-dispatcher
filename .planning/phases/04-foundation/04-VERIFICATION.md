---
phase: 04-foundation
verified: 2026-02-25T21:28:49Z
status: passed
score: 12/12 must-haves verified
re_verification: false
---

# Phase 4: Foundation Verification Report

**Phase Goal:** Companion user exists, system D-Bus policy is verified, and the provisioning tool creates the full deployment skeleton — all before any companion-side daemon code is written
**Verified:** 2026-02-25T21:28:49Z
**Status:** PASSED
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | Running `secrets-dispatcher provision --user nb` creates companion user secrets-nb with 0700 home at /var/lib/secret-companion/nb | VERIFIED | `provision.go`: `ensureUser()` calls `userAddFunc` with `homeDir=/var/lib/secret-companion/{user}`, `ensureHomeDir()` calls `chmodFunc(homeDir, 0700)`. TestProvision_CreatesUser passes. |
| 2 | Running provision a second time succeeds without error (idempotent) | VERIFIED | `ensureUser()` calls `userLookupFunc` first and skips `userAddFunc` if found. `MkdirAll` is inherently idempotent. File writes overwrite. `loginctl enable-linger` is idempotent. TestProvision_Idempotent passes. |
| 3 | Running `provision --check --user nb` prints pass/fail checklist with fix hints | VERIFIED | `check.go`: `Check()` returns 10 `CheckResult` with `Pass bool` and `Message` fix hint on failure. `runProvision()` in main.go prints `[PASS]`/`[FAIL]` per result. |
| 4 | Provisioning creates D-Bus policy file, systemd user unit, PAM hook config, and gopass/GPG directory skeleton | VERIFIED | `provision.go`: `writeDBusPolicy()`, `writeSystemdUnit()`, `writePAMConfig()`, `ensureDirectorySkeleton()` cover all four. Tests WritesDBusPolicy, WritesSystemdUnit, WritesPAMConfig, CreatesDirectories all pass. |
| 5 | Running provision without root exits with clear error message | VERIFIED | `provision.go` line 64: `if geteuidFunc() != 0 { return fmt.Errorf("provision requires root; run: sudo secrets-dispatcher provision") }`. TestProvision_RequiresRoot passes. |
| 6 | Unit tests for provisioning and check run without root, real users, or real systemctl | VERIFIED | All 18 companion tests use injectable function vars (userAddFunc, loginctlFunc, geteuidFunc, etc.). `go test ./internal/companion/...` passes (1.014s). |
| 7 | Daemon registers net.mowaka.SecretsDispatcher1 on D-Bus and responds to Ping/GetVersion stubs | VERIFIED | `dispatcher.go` exports `Ping()` returning "pong" and `GetVersion()` returning version string. `daemon.go` calls `conn.RequestName(BusName, ...)`. TestDaemon_RegistersAndServesStubs verifies end-to-end. |
| 8 | A client can call Ping() via D-Bus and receive 'pong' back | VERIFIED | TestDaemon_RegistersAndServesStubs: client connects to private bus, calls `Interface+".Ping"`, verifies return value is "pong". |
| 9 | The daemon sends READY=1 via sd-notify after successful bus name acquisition | VERIFIED | `daemon.go` line 72: `SdNotify("READY=1")` called immediately after `RequestName` succeeds. TestSdNotify_WithSocket verifies the socket receives "READY=1". |
| 10 | The daemon logs to stderr with structured slog | VERIFIED | `daemon.go`: `slog.Info("daemon ready", ...)` and `slog.Info("daemon shutting down")`. `runDaemon()` in main.go sets `slog.NewJSONHandler(os.Stderr, ...)` when `INVOCATION_ID` is set (systemd), tint handler otherwise. |
| 11 | The daemon subcommand does NOT start any HTTP/WebSocket listeners | VERIFIED | `daemon.go` contains no `http.Listen`, `ListenAndServe`, `websocket`, or `net.Listen` calls. Grep confirms zero matches. |
| 12 | Integration tests with private dbus-daemon verify name registration and stub method calls without root | VERIFIED | `daemon_test.go`: `startDBusDaemonWithPolicy()` launches private `dbus-daemon` with per-UID policy. 3 integration tests + 2 unit tests for SdNotify. `go test ./internal/daemon/...` passes (1.537s). |

**Score:** 12/12 truths verified

---

### Required Artifacts

#### Plan 01 Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/companion/provision.go` | Provision() orchestrator and Config struct | VERIFIED | 263 lines. Exports `Provision(cfg Config) error` and `Config` struct. All 8 idempotent steps implemented. |
| `internal/companion/check.go` | Check() validator with pass/fail per component | VERIFIED | 203 lines. Exports `Check(cfg Config) []CheckResult` (10 checks) and `CheckResult` struct. |
| `internal/companion/sysfuncs.go` | Injectable system call function variables | VERIFIED | 59 lines. Exports `userAddFunc`, `loginctlFunc`, `userLookupFunc`, `mkdirAllFunc`, `chownFunc`, `chmodFunc`, `writeFileFunc`, `geteuidFunc`. All default to real impls. |
| `internal/companion/templates.go` | Embedded D-Bus policy, systemd unit, PAM config templates | VERIFIED | 55 lines. String constants `dbusPolicyTemplate`, `systemdUnitTemplate`, `pamConfigTemplate`. Uses `text/template` in provision.go. Note: uses string constants (not `go:embed`) — equivalent behavior. |
| `internal/companion/provision_test.go` | Unit tests for Provision with mocked system calls | VERIFIED | 470 lines (min_lines: 80). 13 tests, all PASS. |
| `internal/companion/check_test.go` | Unit tests for Check with mocked lookups | VERIFIED | 210 lines (min_lines: 40). 5 tests, all PASS. |
| `main.go` | provision subcommand routing | VERIFIED | `case "provision":` at line 62. `runProvision()` with flag parsing and exit codes. Both `companion` and `daemon` packages imported. |

#### Plan 02 Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/daemon/daemon.go` | Daemon run loop with D-Bus registration, signal handling, context cancellation | VERIFIED | 79 lines. Exports `Run(ctx, Config) error` and `Config` struct. |
| `internal/daemon/dispatcher.go` | D-Bus interface stub with Ping and GetVersion methods | VERIFIED | 34 lines. Exports `Dispatcher`, `BusName`, `ObjectPath`, `Interface`. Ping() returns "pong", GetVersion() returns version. |
| `internal/daemon/notify.go` | sd-notify writing READY=1 to NOTIFY_SOCKET | VERIFIED | 24 lines. Exports `SdNotify(state string)`. Silent no-op when NOTIFY_SOCKET unset, fire-and-forget on dial failure. |
| `internal/daemon/daemon_test.go` | Integration tests with private dbus-daemon | VERIFIED | 276 lines (min_lines: 60). 5 tests, all PASS. |
| `main.go` | daemon subcommand routing | VERIFIED | `case "daemon":` at line 64. `runDaemon()` with slog setup and signal context. |

---

### Key Link Verification

#### Plan 01 Key Links

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `main.go` | `internal/companion/provision.go` | switch case calling companion.Provision() and companion.Check() | WIRED | `companion.Provision(cfg)` at main.go:635, `companion.Check(cfg)` at main.go:619. Package imported at main.go:20. |
| `internal/companion/provision.go` | `internal/companion/sysfuncs.go` | calling injectable function vars | WIRED | `userAddFunc`, `loginctlFunc`, `mkdirAllFunc`, `chownFunc`, `chmodFunc`, `writeFileFunc` all called in provision.go lines 131-238. |
| `internal/companion/provision_test.go` | `internal/companion/sysfuncs.go` | swapping function vars with test fakes | WIRED | `userAddFunc =` at provision_test.go lines 25, 60, 73, 90. saveOrigFuncs() restores all 8 vars. |

#### Plan 02 Key Links

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `main.go` | `internal/daemon/daemon.go` | switch case calling daemon.Run() | WIRED | `daemon.Run(ctx, cfg)` at main.go:673. Package imported at main.go:22. |
| `internal/daemon/daemon.go` | `internal/daemon/dispatcher.go` | conn.Export(dispatcher, ObjectPath, Interface) | WIRED | `conn.Export(dispatcher, ObjectPath, Interface)` at daemon.go:42. |
| `internal/daemon/daemon.go` | `internal/daemon/notify.go` | SdNotify("READY=1") after bus name acquired | WIRED | `SdNotify("READY=1")` at daemon.go:72, immediately after `RequestName` success check at daemon.go:65-67. |
| `internal/daemon/daemon_test.go` | `internal/daemon/daemon.go` | starts daemon against private dbus-daemon, calls stub methods | WIRED | `startDBusDaemonWithPolicy()` at daemon_test.go:51-90. `Run(ctx, Config{BusAddress: addr})` called in TestDaemon_RegistersAndServesStubs, NameAlreadyTaken, Introspectable. |

---

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|------------|-------------|--------|----------|
| COMP-01 | 04-01 | Companion user exists as real user with separate UID and 0700 home | SATISFIED | `ensureUser()` calls useradd without `--system`, `ensureHomeDir()` enforces 0700. TestProvision_CreatesUser verifies. |
| COMP-02 | 04-01 | gopass store and GPG keyring reside under companion home, inaccessible to desktop user | SATISFIED | `ensureDirectorySkeleton()` creates `~/.config/gopass/` and `~/.gnupg/` under companion home with 0700 + chown. Phase 4 scope = dirs only. |
| COMP-05 | 04-01 | systemd linger enabled for companion user | SATISFIED | `loginctlFunc("enable-linger", companionUser)` at provision.go:131. TestProvision_EnablesLinger verifies. |
| DBUS-01 | 04-02 | Daemon registers on system bus, accepting requests from desktop user UID only | SATISFIED | `daemon.go` calls `ConnectSystemBus()` in production. Policy template in `templates.go` restricts `send_destination` to companion and desktop users only. TestDaemon_RegistersAndServesStubs verifies registration. |
| DBUS-02 | 04-01 | D-Bus policy file gates access: companion user owns name, desktop user can call methods | SATISFIED | `dbusPolicyTemplate` in templates.go: companion user gets `allow own` + `allow send_destination`; desktop user gets `allow send_destination` only. TestProvision_WritesDBusPolicy verifies content. |
| PROV-01 | 04-01 | Provisioning tool creates companion user, home dir, D-Bus policy, systemd units | SATISFIED | `Provision()` orchestrates all steps: ensureUser, ensureHomeDir, writeDBusPolicy, writeSystemdUnit. |
| PROV-02 | 04-01 | Provisioning tool installs PAM hook | SATISFIED | `writePAMConfig()` writes `/etc/pam.d/secrets-dispatcher` with `pam_exec.so --no-block`. TestProvision_WritesPAMConfig verifies. |
| PROV-03 | 04-01 | Provisioning tool configures gopass and GPG under companion user | SATISFIED (Phase 4 scope) | `ensureDirectorySkeleton()` creates `~/.config/gopass/` and `~/.gnupg/`. Full gopass init deferred to Phase 5 per CONTEXT.md. |
| PROV-04 | 04-01 | `sd-provision --check` validates full deployment with pass/fail per component | SATISFIED | `Check()` returns 10 CheckResults. `runProvision(--check)` in main.go prints `[PASS]`/`[FAIL]` with fix hints. |
| PROV-05 | 04-01 | Provisioning tool is idempotent (safe to re-run) | SATISFIED | User creation checks existence first. MkdirAll is idempotent. File writes overwrite. loginctl enable-linger is idempotent. TestProvision_Idempotent verifies two runs succeed. |
| INFRA-01 | 04-02 | All daemon logging goes to systemd-journald (structured slog) | SATISFIED | `daemon.go` uses `slog.Info()`. `runDaemon()` in main.go configures `slog.NewJSONHandler(os.Stderr)` when `INVOCATION_ID` is set (systemd environment). |
| INFRA-02 | 04-02 | HTTP/REST API, WebSocket, and Web UI are opt-in; daemon does not start them | SATISFIED | `daemon.go` contains zero HTTP/WebSocket/net.Listen calls. Grep confirms no matches. Daemon only connects to D-Bus and blocks on context. |
| TEST-04 | 04-01, 04-02 | CI-compatible: unit + integration run in CI without root | SATISFIED | All 18 companion tests use mocked syscalls (no root needed). All 5 daemon tests use private dbus-daemon (no root needed). `go test -race ./...` passes in 2.560s. |

**All 13 requirement IDs from PLAN frontmatter: SATISFIED**

**Orphaned requirements check:** REQUIREMENTS.md traceability table maps COMP-01, COMP-02, COMP-05, DBUS-01, DBUS-02, PROV-01–05, TEST-04, INFRA-01, INFRA-02 to Phase 4. All are claimed by plan frontmatter and verified above. No orphaned requirements.

---

### Anti-Patterns Found

None. Full scan of all 7 phase-created files found:
- Zero TODO/FIXME/XXX/HACK/PLACEHOLDER comments
- Zero empty return stubs (no `return null`, `return {}`, `return []`)
- Zero console.log-only implementations
- No HTTP/WebSocket stubs

One design note (not a problem): `templates.go` uses string constants rather than `go:embed`. The PLAN specified `go:embed` but string constants are equally valid for in-process template strings — there are no separate template files to embed. This is an improvement in simplicity, not a deficiency.

---

### Human Verification Required

None — all phase goals are verifiable programmatically.

The following are deferred to later phases by design (not phase 4 gaps):
- Actual companion user creation on a real system (requires Phase 8 VM E2E, TEST-03)
- PAM hook firing on real login (requires Phase 6 integration work)
- D-Bus policy enforcement across real UIDs (requires Phase 8 VM E2E)

---

### Test Results Summary

```
ok  github.com/nikicat/secrets-dispatcher/internal/companion  1.014s
ok  github.com/nikicat/secrets-dispatcher/internal/daemon     1.537s
ok  github.com/nikicat/secrets-dispatcher (full suite)        2.560s — no regressions
```

**Companion tests:** 18 tests, 0 failures
- 13 provision tests: CreatesUser, SkipsExistingUser, CreatesDirectories, WritesDBusPolicy, WritesSystemdUnit, WritesPAMConfig, EnablesLinger, RequiresRoot, DetectsSUDO_USER, FailsWithoutUser, Idempotent, UserAddPropagatesError, CompanionNameOverride
- 5 check tests: AllPass, MissingUser, MissingFiles, LingerMissing, ReturnsExpectedCheckCount

**Daemon tests:** 5 tests, 0 failures
- 3 integration tests against private dbus-daemon: RegistersAndServesStubs, NameAlreadyTaken, Introspectable
- 2 unit tests for SdNotify: NoSocket, WithSocket

---

## Gaps Summary

None. Phase 4 goal is fully achieved.

All must-haves from both plan frontmatter definitions are verified. The phase delivers exactly what was promised: provisioning infrastructure and daemon skeleton with proven D-Bus wire protocol — ready for Phase 5 to add real business logic without revisiting plumbing.

---

_Verified: 2026-02-25T21:28:49Z_
_Verifier: Claude (gsd-verifier)_
