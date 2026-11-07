---
status: todo
tags: [refactor, config]
---
# One owner of the state directory

## Goal
`$XDG_STATE_HOME/secrets-dispatcher` (falling back to `~/.local/state`) is
derived in `main.getStateDir` and again in `gpgsign.loadAuthToken`, which also
spells `.cookie`, the name that `api` writes as `cookieFileName`. The message
"not running (no cookie file found)" is written in both `runLogin` and
`runCLI`. The app name `secrets-dispatcher` is spelled in `config.DefaultPath`,
`config/runtime.go` and both of those.

Done when `config.StateDir` with `LookupStateDir()` and a `Cookie()` method
owns the paths, the twin of `config.RuntimeDir`, with one app-name constant
shared by `DefaultPath`, `RuntimeDir` and `StateDir`, and one cookie loader.

## Next
This finishes the shape that `RuntimeDir` started on branch
`refactor/unsmell-follow-ups`, so it's the natural next change there.

Found by the 2026-11-07 unsmell run.
