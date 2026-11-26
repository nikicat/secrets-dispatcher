---
status: todo
tags: [service]
---
# A trial looks up the runtime directory twice

## Goal
`Try` (internal/service/try.go) looks up the runtime directory to place the
trial config, and `resolveInstallInputs` (internal/service/plan.go) looks it
up again. `trial.config` also holds the same file as the install's
`in.draft.File()`.

## Next
`resolveInstallInputs` takes the runtime directory from its caller, so each
install path looks it up once, and the trial keeps one name for its config
file.

Found by the 2026-11-25 unsmell run.
