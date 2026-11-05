---
status: todo
tags: [refactor, unsmell]
---
# Follow-ups planned by the 2026-11-05 unsmell run

## Goal
Smells the unsmell run of `feat/remote-gpg-sign` found but did not fix: they
sit on older lines, or need a decision or work larger than that branch. Each
entry is independent; split one out into its own task when you start it.

## Entries
- **One owner of the runtime directory.** `$XDG_RUNTIME_DIR/secrets-dispatcher`
  is derived in about seven places with three fallbacks (none, error,
  `/run/user/<uid>`); `api.sock` is written by `main.go` and read by
  `gpgsign.unixSocketPath` with different fallbacks. Fix: one function in config
  returning the directory, with named paths for `api.sock`, `ssh-agent.sock`,
  `sockets/`, `remote/<name>.sock`, `backend-bus.sock`; pick one fallback rule
  (a behaviour change for the thin client).
- **No auto-approve offer on remote requests.** The web UI
  (`RequestCard.svelte`) and the desktop notification (`notification/desktop.go`)
  offer "approve and auto-approve" on `remote:<name>` requests; the rule it
  creates has an empty `InvokerExe` and can never match. Fix: hide the action for
  requests without a local exe, and have `AddAutoApproveRule` refuse them.
  Needs the embedded web build refreshed.
- **One outcome type for both thin-client paths.** `WaitForResolution` (local)
  still returns a loose tuple while `SignRemote` returns `RemoteSignResponse`;
  the "denied by user" / "timed out" messages and exit codes are spelled in both
  `runLocal` and `runRemote`. Fix: `WaitForResolution` returns the same type, and
  one function maps it to message and exit code.
- **Local path cancels with `signal.NotifyContext`.** `runLocal` hand-rolls a
  goroutine racing a signal channel against its context, where `runRemote` uses
  `signal.NotifyContext`. Fix: derive the context from `NotifyContext` and send
  the best-effort cancel when it ends.
- **`run*` functions return errors.** About 35 `Fprintf(os.Stderr)` +
  `os.Exit(1)` pairs in `main.go`; `os.Exit` skips registered defers (sockets
  stay on disk until the next start removes them). Fix: each `run*` returns an
  error and `main` prints and exits once.
- **Settings resolved once at the edge.** Flag-over-config precedence and the
  state directory are merged field by field in `runLogin`, `runCLI` and
  `runServe` (nine settings for serve). Fix: one settings type per subcommand,
  flags over config over defaults, possibly via a library such as ff or koanf.
- **Retry with backoff as a helper.** The session_bus runner in `main.go`
  hand-rolls exponential backoff inside topology setup. Fix: a small retry
  helper, or a session type that owns reconnecting.
- **One slog setup.** Serve and daemon build their slog handlers with different
  systemd rules, and the daemon's comment "Mirror the slog setup from runServe"
  is false. Fix: one `newLogHandler(level, format)`.
- **Misplaced doc line.** "runService handles the service subcommand group"
  sits above `runTry`, so `runService` has no doc. Fix: move it.
- **`Config.Validate` by type.** It inlines bus, rule-action, request-type and
  glob checks; `strFromProcessMatcher`/`strFromSecretMatcher` pick fields by
  string key. Fix: `BusConfig.validate`, `TrustRule.validate`, and name/value
  pairs built from the structs.
- **Typed enums in config.** `BusConfig.Type` and `TrustRule.Action` are strings
  branched on by literal. Fix: string types whose `UnmarshalYAML` rejects
  unknown values (as `RemoteName` does), with typed constants at every branch.
- **Long trailing field comments in config.** They encode defaults and what an
  empty or zero value means ("0 disables; default 1.5s"). Fix: comments above
  the fields as sentences; typed defaults where zero means "use the default".

- **`SetupGitConfig` doc on relative sockets.** It now resolves a relative
  `remoteSocket` against the current directory at setup time, and its doc does
  not say so. Fix: one sentence in the doc.

## Next
Pick the runtime-directory owner first: the thin client, the daemon and the
remote sockets all depend on it.
