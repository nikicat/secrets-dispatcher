---
phase: 02-core-signing-flow
verified: 2026-02-24T14:00:00Z
status: passed
score: 14/14 must-haves verified
re_verification: false
---

# Phase 2: Core Signing Flow — Verification Report

**Phase Goal:** Running `git commit -S` with `gpg.program` set to the `gpg-sign` subcommand blocks until the user approves in the daemon, then commits with a valid GPG signature — or exits non-zero if denied or daemon is unreachable
**Verified:** 2026-02-24
**Status:** PASSED
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | `gpg-sign` subcommand intercepts git's gpg call and blocks until resolved | VERIFIED | `Run()` in `internal/gpgsign/run.go`: opens WS, POSTs request, calls `WaitForResolution` which loops on `conn.Read` |
| 2 | Thin client connects to daemon via Unix socket | VERIFIED | `DaemonClient` in `daemon.go`: `http.Transport.DialContext` overrides to `"unix"` dialect; `unixSocketPath()` returns XDG_RUNTIME_DIR path |
| 3 | WebSocket established BEFORE POST to avoid missed events | VERIFIED | `run.go` L68: `DialWebSocket` called at step 8; `PostSigningRequest` at step 9 — explicit ordering with comment |
| 4 | Denied exits 1, daemon unreachable exits 2, gpg failures propagate exit code | VERIFIED | `run.go`: denied → return 1 (L104); WS error → return 2 (L99 timeout) or 2 (L70 unreachable); exitCode != 0 → return exitCode (L112) |
| 5 | `gpg-sign setup` writes shell wrapper and configures `git gpg.program` | VERIFIED | `SetupGitConfig` in `setup.go`: writes `#!/bin/sh\nexec %s gpg-sign "$@"\n` to `~/.local/bin/secrets-dispatcher-gpg`, runs `git config gpg.program` |
| 6 | Signature written to stdout, GPG status written to stderr on success | VERIFIED | `run.go` L116-119: `os.Stdout.Write(signature)` + `os.Stderr.Write(gpgStatus)` |
| 7 | Daemon calls real gpg after approval and captures signature | VERIFIED | `HandleApprove` in `handlers.go`: calls `h.gpgRunner.FindGPG()` + `h.gpgRunner.RunGPG()` for `gpg_sign` type requests |
| 8 | GPG stdout (signature) and stderr (status) captured in separate buffers | VERIFIED | `defaultGPGRunner.RunGPG` in `gpgsign.go`: `var sigBuf, statusBuf bytes.Buffer`; `cmd.Stdout = &sigBuf`; `cmd.Stderr = &statusBuf` |
| 9 | ParseCommitObject extracts author, committer, message, parentHash | VERIFIED | `commit.go` + 6 passing test cases covering standard, parent, merge, empty, multi-line, trailing-newline |
| 10 | FindRealGPG locates real gpg, skips self via inode comparison | VERIFIED | `gpg.go`: uses `os.SameFile(selfInfo, info)` to skip; 4 test cases passing including hard-link self-detection |
| 11 | Thin client sends commit data + context to daemon as JSON | VERIFIED | `PostSigningRequest` in `daemon.go` marshals `GPGSignInfo` with all fields including `CommitObject`, POSTs to `/api/v1/gpg-sign/request` with Bearer token |
| 12 | HandleWS accepts Bearer tokens (thin client auth) | VERIFIED | `websocket.go` L99: `h.auth.ValidateRequest(r)` — combines `ValidateSession` (cookie) with Bearer header check |
| 13 | Daemon listens on Unix socket alongside TCP | VERIFIED | `server.go`: `net.Listen("unix", unixSocketPath)` when path non-empty; second goroutine in `Start()` calls `s.httpServer.Serve(s.unixListener)` |
| 14 | GPG exit codes from failures propagate to thin client via WSMessage | VERIFIED | `ApproveGPGFailed` stores `GPGExitCode`; `OnEvent` in `websocket.go` L164: `msg.ExitCode = event.Request.GPGExitCode`; `WaitForResolution` in `daemon.go` L133-136: returns `msg.ExitCode` |

**Score:** 14/14 truths verified

---

### Required Artifacts

#### Plan 02-01 Artifacts

| Artifact | Provides | Status | Evidence |
|----------|----------|--------|----------|
| `internal/approval/gpgsign.go` | `CommitObject` field on `GPGSignInfo` | VERIFIED | L26: `CommitObject string \`json:"commit_object,omitempty"\`` |
| `internal/approval/manager.go` | `GetPending`, `ApproveWithSignature`, `ApproveGPGFailed`; exported `Signature`, `GPGStatus`, `GPGExitCode` | VERIFIED | L351-398: all three methods present; L82-88: exported fields with `json:"-"` |
| `internal/api/auth.go` | `ValidateRequest` combining cookie + Bearer | VERIFIED | L152-161: checks `ValidateSession` first, then `Bearer` header via `ConstantTimeCompare` |
| `internal/api/websocket.go` | `GPGStatus`/`ExitCode` on `WSMessage`; `HandleWS` uses `ValidateRequest`; real `Request.Signature` in `OnEvent` | VERIFIED | L50-53: fields present; L99: `ValidateRequest`; L161-167: reads `event.Request.Signature` |
| `internal/api/server.go` | Unix socket listener alongside TCP | VERIFIED | L22-23: `unixListener`/`UnixSocketPath` fields; L112: `net.Listen("unix", ...)`; L135-140: second goroutine |

#### Plan 02-02 Artifacts

| Artifact | Provides | Status | Evidence |
|----------|----------|--------|----------|
| `internal/gpgsign/commit.go` | `ParseCommitObject` function | VERIFIED | L24: exported func signature; substantive 53-line implementation |
| `internal/gpgsign/gpg.go` | `FindRealGPG` and `extractKeyID` | VERIFIED | L17, L51: both functions present and implemented |
| `internal/gpgsign/commit_test.go` | Tests for `ParseCommitObject` | VERIFIED | `TestParseCommitObject` with 6 subtests |
| `internal/gpgsign/gpg_test.go` | Tests for `FindRealGPG` and `extractKeyID` | VERIFIED | `TestFindRealGPG` (4 cases) + `TestExtractKeyID` (5 cases) |

#### Plan 02-03 Artifacts

| Artifact | Provides | Status | Evidence |
|----------|----------|--------|----------|
| `internal/api/gpgsign.go` | `GPGRunner` interface and `defaultGPGRunner` | VERIFIED | L16-19: interface; L22-50: `defaultGPGRunner` with `FindGPG`/`RunGPG` |
| `internal/api/handlers.go` | `HandleApprove` calls `GetPending` then `ApproveWithSignature` | VERIFIED | L141-178: full gpg_sign branch with all three paths (success, gpg fail, gpg not found) |

#### Plan 02-04 Artifacts

| Artifact | Provides | Status | Evidence |
|----------|----------|--------|----------|
| `internal/gpgsign/run.go` | `Run()` entry point | VERIFIED | L26: exported `Run(args []string, stdin io.Reader) int`; 204-line substantive implementation |
| `internal/gpgsign/daemon.go` | `DaemonClient` with HTTP+WS over Unix socket | VERIFIED | L17-153: `DaemonClient`, `NewDaemonClient`, `DialWebSocket`, `PostSigningRequest`, `WaitForResolution` |
| `internal/gpgsign/setup.go` | `SetupGitConfig` | VERIFIED | L20-65: full implementation with wrapper script writing and `git config` |
| `main.go` | `gpg-sign` subcommand routing | VERIFIED | L55-56: case `"gpg-sign"` → `runGPGSign`; L488-493: dispatches to `gpgsign.Run` |

---

### Key Link Verification

| From | To | Via | Status | Evidence |
|------|----|-----|--------|----------|
| `websocket.go` | `auth.go` | `HandleWS` calls `ValidateRequest` | WIRED | `websocket.go` L99: `if !h.auth.ValidateRequest(r)` |
| `server.go` | `net.Listen unix` | second Unix socket listener | WIRED | `server.go` L112: `net.Listen("unix", unixSocketPath)` |
| `handlers.go` | `manager.go` | `GetPending` then `ApproveWithSignature` | WIRED | `handlers.go` L141: `h.manager.GetPending(id)`; L174: `h.manager.ApproveWithSignature(id, sig, status)` |
| `gpgsign.go` (api) | `os/exec` gpg subprocess | `exec.Command` in `RunGPG` | WIRED | `gpgsign.go` L34: `exec.Command(gpgPath, "--status-fd=2", "-bsau", keyID)` |
| `run.go` | `daemon.go` | `Run` calls `DaemonClient` methods | WIRED | `run.go` L63: `NewDaemonClient`; L68: `DialWebSocket`; L76: `PostSigningRequest`; L96: `WaitForResolution` |
| `daemon.go` | `server.go` | HTTP-over-Unix-socket transport | WIRED | `daemon.go` L26-28: `DialContext` dials `"unix"` + `socketPath` |
| `main.go` | `run.go` | dispatches `gpg-sign` to `gpgsign.Run` | WIRED | `main.go` L493: `os.Exit(gpgsign.Run(args, os.Stdin))` |
| `run.go` | `commit.go` | `Run` calls `ParseCommitObject` | WIRED | `run.go` L43: `author, committer, message, parentHash := ParseCommitObject(commitBytes)` |

---

### Requirements Coverage

| Requirement | Phase 2 Plans | Description | Status | Evidence |
|-------------|---------------|-------------|--------|----------|
| SIGN-01 | 02-04 | `gpg-sign` subcommand intercepts git's `gpg.program` call and blocks | SATISFIED | `Run()` in `run.go` reads stdin, dials WS, POSTs, waits; `main.go` routes `gpg-sign` |
| SIGN-02 | 02-02, 02-04 | Thin client parses raw commit object from stdin | SATISFIED | `ParseCommitObject` in `commit.go`; called in `run.go` L43 |
| SIGN-03 | 02-04 | Thin client resolves repository name via `git rev-parse --show-toplevel` | SATISFIED | `resolveRepoName` in `run.go` L124-134: runs git command, returns `filepath.Base` |
| SIGN-04 | 02-04 | Thin client collects changed files via `git diff --cached --name-only` | SATISFIED | `collectChangedFiles` in `run.go` L136-153 |
| SIGN-05 | 02-01, 02-04 | Thin client sends commit data + context to daemon via API | SATISFIED | `PostSigningRequest` sends full `GPGSignInfo` including `CommitObject`; `CommitObject` field added in 02-01 |
| SIGN-07 | 02-01, 02-03 | Daemon calls real `gpg` after approval, captures signature and status | SATISFIED | `defaultGPGRunner.RunGPG` uses `exec.Command`; `HandleApprove` calls it for `gpg_sign` requests |
| SIGN-08 | 02-01, 02-04 | Signature and gpg status returned to thin client; client writes to stdout/stderr | SATISFIED | `WSMessage.Signature` + `WSMessage.GPGStatus`; `WaitForResolution` decodes them; `run.go` L116-119 writes both |
| ERR-01 | 02-04 | Thin client exits non-zero with clear stderr message when daemon unreachable | SATISFIED | `run.go` L69-72: WS fail → stderr message + return 2; L51-54: auth token missing → stderr + return 2 |
| ERR-02 | 02-03, 02-04 | Exit code from real gpg failures propagated through daemon to thin client | SATISFIED | `ApproveGPGFailed` stores `GPGExitCode`; `WSMessage.ExitCode` carries it; `run.go` L107-113 returns it |

**Orphaned requirements check:** SIGN-06 (Phase 1) and SIGN-09 (Phase 1) are not in scope for Phase 2 plans. All Phase 2 requirement IDs from the task brief (SIGN-01, -02, -03, -04, -05, -07, -08, ERR-01, ERR-02) are accounted for.

---

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `internal/api/gpgsign_test.go` | 164 | `PLACEHOLDER_SIGNATURE` | Info | Test-only stub in `wsEventObserver.OnEvent` — production `OnEvent` in `websocket.go` L162 uses real `event.Request.Signature`. No production impact. |

No blocker or warning anti-patterns found in production code.

---

### Build and Test Verification

```
go build ./...    — clean (zero output)
go vet ./...      — clean (zero output)
go test ./...     — all packages pass
  ok  github.com/nikicat/secrets-dispatcher
  ok  github.com/nikicat/secrets-dispatcher/internal/api
  ok  github.com/nikicat/secrets-dispatcher/internal/approval
  ok  github.com/nikicat/secrets-dispatcher/internal/cli
  ok  github.com/nikicat/secrets-dispatcher/internal/config
  ok  github.com/nikicat/secrets-dispatcher/internal/gpgsign
  ok  github.com/nikicat/secrets-dispatcher/internal/notification
  ok  github.com/nikicat/secrets-dispatcher/internal/proxy
```

---

### Human Verification Required

#### 1. End-to-End Signing Flow

**Test:** With the daemon running (`secrets-dispatcher serve`), configure `gpg-sign setup`, then run `git commit -S` in a repo. Approve the request in the web UI.
**Expected:** Commit completes with a valid GPG signature. `git log --show-signature` shows "Good signature".
**Why human:** Requires running daemon, real gpg-agent, and a git repo. Cannot mock the full GPG keyring + `git commit` integration chain programmatically.

#### 2. Denial Flow

**Test:** Run `git commit -S` and deny the request in the web UI.
**Expected:** `git commit` fails with a non-zero exit code. Git prints an error that signing failed. No commit is created.
**Why human:** Requires interactive web UI interaction. Exit code propagation through git's gpg invocation mechanism needs human observation.

#### 3. Daemon Unreachable

**Test:** Run `git commit -S` without the daemon running.
**Expected:** Immediate failure with a clear error message on stderr mentioning that secrets-dispatcher is not running. Exit code 2.
**Why human:** Easy to test manually but confirms the exact UX message and exit code visible to the user.

---

### Gaps Summary

No gaps. All 14 observable truths are verified, all 15 artifacts are substantive and wired, all 8 key links are confirmed, and all 9 Phase 2 requirements are satisfied with direct code evidence.

The only finding is a `PLACEHOLDER_SIGNATURE` constant inside a test helper — it is not reachable from production code and does not affect the signing pipeline.

---

_Verified: 2026-02-24_
_Verifier: Claude (gsd-verifier)_
