---
status: todo
tags: [refactor, unsmell]
---
# Follow-ups planned by the 2026-11-07 runtime-dir unsmell run

## Goal
Smells the unsmell run of the runtime-directory refactor found but did not fix.
They sit on lines that change did not touch. Each entry is independent. Split
one out into its own task when you start it. The run's findings are in
`.git/unsmell/20261107-202321/findings/` while that directory lasts.

## Entries
- **A state-directory owner.** `$XDG_STATE_HOME/secrets-dispatcher` (falling
  back to `~/.local/state`) is derived in `main.getStateDir` and again in
  `gpgsign.loadAuthToken`, which also spells `.cookie`, the name that `api` writes
  as `cookieFileName`. The app name `secrets-dispatcher` is spelled in
  `config.DefaultPath`, `runtime.go` and both of those. Fix: `config.StateDir`
  with `LookupStateDir()` and a `Cookie()` method, the twin of `RuntimeDir`, with
  one app-name constant shared by all three.
- **Plan rewrites Install by hand.** `service.Plan` repeats Install's sequence:
  mask, demote, stop, write or remove units, then daemon-reload, enable and
  start. Its doc says "keep it in sync with Install". `stopDBusActivatedService`
  repeats DetectProvider's owner-PID query, and it signals the owner even when
  that's the dispatcher, which Plan doesn't list. So `--dry-run` under-reports a
  reinstall. Fix: Install runs the steps that Plan returns, and
  `stopDBusActivatedService(provider)` signals under Plan's condition.
- **Typed log level and format.** `serve.log_level` and `log_format` stay raw
  strings. `parseLogLevel` turns an unknown level into info without a word, and
  an unknown format means text. Fix: `config.LogLevel` and `LogFormat` with
  `UnmarshalText` that rejects unknown values, also used by the flags and by the
  planned `newLogHandler` (Te1zf9).
- **TrustedSigner copied field by field.** `main.go` copies `config.TrustedSigner`
  into `approval.TrustedSigner`, the same shape as the `TrustRule` copy in
  Te1zf9. Fix it in the same change: one type in `approval` with `yaml` and
  `json` tags. `TrustedSigner.RepoPath` holds a repository basename, not a path.
  Renaming it changes the config file, so it needs a decision.
- **API router.** `api.newServerWithHandlers` builds the routing table inline,
  with dispatch closures. `/api/v1/pending/` actions are matched by
  `strings.HasSuffix` on the path, and each handler parses the id again. The
  route paths are spelled in the router and again in `gpgsign/daemon.go` and
  `cli`. Fix: Go 1.22 `ServeMux` patterns with `r.PathValue`, and route
  constants shared by the server and the clients.
- **api.Server constructors.** `NewServer` and `NewServerWithProvider` take
  eight or nine parameters. Adjacent `remoteSocket, clientName string` and a
  bare `trimProcessChain bool` travel together into `NewHandlers*`. Fix: one
  `ServerConfig` struct, with the client source as the only other argument.
- **runServe's downstream runners.** The closures all capture `approvalMgr`,
  the level, `TrimProcessChain`, the slow-upstream notifier, the threshold and
  `upstreamAddr`. `proxy.Config` is written out twice with the same fields. An
  empty `upstreamAddr` means "the session bus", and every runner branches on it.
  `unix:path=` is prefixed by hand in two places, plus `proxy/manager.go`. Fix:
  one runner type that holds the shared settings, and an upstream address type.
- **runServe's signal handling.** It hand-rolls a signal channel and a
  goroutine, where `runTry` and `runDaemon` use `signal.NotifyContext`.
- **CLI duplication in main.go:**
  - The "not running (no cookie file found)" message is written in `runLogin`
    and `runCLI`.
  - The `--config`, `--state-dir` and `--verbose` help texts are spelled many
    times.
  - The flag defaults `127.0.0.1:8484` and `info` duplicate
    `config.DefaultListenAddr` and `DefaultLogLevel`.
  - `runConfigEdit` restarts `secrets-dispatcher.service` by literal, which
    `service` owns as `proxyServiceUnit`.
  - `main` and `runCLI` both switch on the five CLI command names.
  - `--start` and `--no-start` are two bools for one setting.
- **Typed `--backend`.** The value (empty, a path, or a keyword) travels raw
  until `resolveBackendExec` parses it. Its keywords are matched there and
  listed again in two help texts. Fix: a `service.Backend` type, parsed where
  the flag is read.
- **One topology builder.** `service.writeTopology`'s remote case and
  `config.WithDefaults`' default downstream build the same topology. Fix: one
  builder in `config`.
- **gpgsign tests and types.** Two tests build the redirecting `DaemonClient`
  inline, which `newTestClient` already does. Two `TestLoadAuthToken` subtests
  build the same cookie fixture. `Run`, `runLocal`, `runRemote` and
  `GPGOutput.Emit` return a bare `int` that means an exit status.
- **SSH proxy with no upstream agent.** With an `ssh:` section, an empty
  `ssh.upstream` and no `$SSH_AUTH_SOCK`, serve logs an error and runs without
  the SSH proxy, while `config validate` passes. That is common under systemd.
  The user chose to keep serve running in that case. Fix: `config validate`
  warns about it, and `service install` writes `ssh.upstream` from the
  installing shell's `SSH_AUTH_SOCK`.
- **Doc openings.** About 70 docs on unchanged lines open with their own
  declaration's name, in `config`, `api`, `gpgsign`, `service` and `main.go`. Fix
  them in one sweep.

## Next
The state-directory owner first, because it finishes the shape that
`RuntimeDir` started.
