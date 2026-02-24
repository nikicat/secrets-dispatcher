---
phase: 02-core-signing-flow
plan: "04"
subsystem: gpgsign
tags: [go, gpg, unix-socket, websocket, bearer-auth, thin-client, setup]

# Dependency graph
requires:
  - phase: 02-core-signing-flow/02-01
    provides: "Bearer WebSocket auth, Unix socket listener, WSMessage.GPGStatus/ExitCode"
  - phase: 02-core-signing-flow/02-02
    provides: "ParseCommitObject, FindRealGPG, extractKeyID"
provides:
  - "DaemonClient: HTTP+WS over Unix socket transport for thin client"
  - "Run(): gpg-sign entry point that intercepts git's gpg call and blocks until resolved"
  - "SetupGitConfig: writes wrapper script and configures git gpg.program"
  - "main.go gpg-sign subcommand routing"
  - "Unix socket path passed from main.go to API server on startup"
affects:
  - 02-core-signing-flow/02-05 (end-to-end flow complete; integration tests can cover full pipeline)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "HTTP-over-Unix-socket via http.Transport.DialContext overriding network to 'unix'"
    - "WebSocket-before-POST ordering: ensures no resolution event is missed between subscribe and send"
    - "Shell wrapper for git gpg.program: execvp does not shell-split, wrapper calls 'exec binary gpg-sign \"$@\"'"
    - "XDG_RUNTIME_DIR for Unix socket path, XDG_STATE_HOME for cookie path — both sides use same convention"

key-files:
  created:
    - internal/gpgsign/daemon.go
    - internal/gpgsign/run.go
    - internal/gpgsign/setup.go
  modified:
    - main.go
    - internal/api/server.go
    - internal/api/server_test.go

key-decisions:
  - "NewServer/NewServerWithProvider accept unixSocketPath as last parameter — explicit rather than implicit; tests pass '' to skip socket"
  - "timeout (request_expired) exits 1 not 2 — timeout is a user-visible outcome (like denial), not a system error"
  - "runServe skips Unix socket when --api-only — test/dev mode does not need the signing pipeline"
  - "XDG_RUNTIME_DIR unset warning in runServe — graceful degradation with clear log message rather than hard failure"

patterns-established:
  - "DaemonClient pattern: any future thin client tool can use the same HTTP-over-Unix-socket transport + Bearer auth"

requirements-completed:
  - SIGN-01
  - SIGN-03
  - SIGN-04
  - SIGN-05
  - SIGN-08
  - ERR-01

# Metrics
duration: 4min
completed: 2026-02-24
---

# Phase 02 Plan 04: Thin Client Implementation Summary

**DaemonClient (HTTP+WS over Unix socket), Run() entry point for git gpg.program interception, SetupGitConfig wrapper installer, and main.go subcommand wiring completing the thin client side of the signing pipeline**

## Performance

- **Duration:** 4 min
- **Started:** 2026-02-24T13:06:29Z
- **Completed:** 2026-02-24T13:11:01Z
- **Tasks:** 2
- **Files modified:** 3 created, 3 modified

## Accomplishments

- `DaemonClient` communicates with the daemon over a Unix socket using an HTTP transport that overrides `DialContext` to dial `"unix"` — the same `http.Client` handles both regular HTTP (POST) and WebSocket upgrade
- `Run()` implements the complete thin client flow: reads commit object from stdin, parses context, opens WebSocket first (guarantees no missed events), POSTs signing request, blocks on WebSocket until `request_resolved` or `request_expired`
- `SetupGitConfig` writes a shell wrapper to `~/.local/bin/secrets-dispatcher-gpg` and configures `git gpg.program` — the wrapper is required because git uses `execvp` (not shell expansion) so a binary with spaces in the path would fail
- `main.go` routes `gpg-sign` subcommand to `gpgsign.Run()` and `gpg-sign setup` to `gpgsign.SetupGitConfig()`; `runServe` computes the Unix socket path from `XDG_RUNTIME_DIR` and passes it to `NewServer`/`NewServerWithProvider` (was `""` in all prior plans)
- Updated `NewServer` and `NewServerWithProvider` signatures to accept `unixSocketPath` — makes the Unix socket explicit at call site rather than requiring a post-construction setter

## Task Commits

Each task was committed atomically:

1. **Task 1: Implement DaemonClient and Run() entry point** - `dd5c573` (feat)
2. **Task 2: Implement SetupGitConfig and wire subcommands in main.go** - `fbf4d2e` (feat)

## Files Created/Modified

- `internal/gpgsign/daemon.go` — `DaemonClient` struct with `NewDaemonClient`, `DialWebSocket`, `PostSigningRequest`, `WaitForResolution`; local `wsMsg` type avoids circular import with `api` package
- `internal/gpgsign/run.go` — `Run(args []string, stdin io.Reader) int` with helpers `resolveRepoName`, `collectChangedFiles`, `loadAuthToken`, `unixSocketPath`, `runGitCommand`; exit codes 0/1/2/N per spec
- `internal/gpgsign/setup.go` — `SetupGitConfig(scope string) error`; writes shell wrapper, runs `git config gpg.program`, prints setup confirmation
- `main.go` — added `gpg-sign` case + `runGPGSign`/`runGPGSignSetup` functions; updated `printUsage`; compute and pass `apiUnixSocket` in `runServe`; log Unix socket path on startup
- `internal/api/server.go` — `NewServer` and `NewServerWithProvider` now accept `unixSocketPath string` as last parameter
- `internal/api/server_test.go` — updated two `NewServer` calls to pass `""` for socket path

## Decisions Made

- **Timeout exits 1, not 2:** `request_expired` is a user-visible outcome (the user did not respond in time) — exit 1 is the "not approved" code; exit 2 is reserved for system/infrastructure failures
- **`NewServer`/`NewServerWithProvider` signature change:** Passing the unix socket path explicitly at the call site (in `main.go`) is cleaner than a post-construction method or a separate field mutation — it follows the same pattern as all other server configuration parameters
- **Skip Unix socket in `--api-only` mode:** API-only mode is used for testing the HTTP API without the full D-Bus proxy setup; the signing pipeline is not needed there
- **Graceful `XDG_RUNTIME_DIR` handling:** Log a warning instead of failing hard if `XDG_RUNTIME_DIR` is unset — the daemon still starts and serves the web UI; only the thin client connection path is unavailable

## Deviations from Plan

None — plan executed exactly as written.

## Issues Encountered

None.

## Next Phase Readiness

- Thin client flow is complete: `git commit -S` → git calls `secrets-dispatcher gpg-sign` → DaemonClient connects via Unix socket → waits for approval → writes signature to stdout
- Plan 03 (real GPG invocation on daemon side) is the remaining piece to complete the full signing pipeline
- All 11 tests pass; `go build ./...` and `go vet ./...` clean

---
*Phase: 02-core-signing-flow*
*Completed: 2026-02-24*

## Self-Check: PASSED

- internal/gpgsign/daemon.go: FOUND
- internal/gpgsign/run.go: FOUND
- internal/gpgsign/setup.go: FOUND
- main.go: FOUND
- internal/api/server.go: FOUND
- internal/api/server_test.go: FOUND
- .planning/phases/02-core-signing-flow/02-04-SUMMARY.md: FOUND
- Commit dd5c573: FOUND
- Commit fbf4d2e: FOUND
