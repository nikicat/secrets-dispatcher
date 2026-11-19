---
status: todo
tags: [refactor, gpg]
---
# gpgsign spells its own shutdown signal set

## Goal
`internal/gpgsign/run.go` builds its own `signal.NotifyContext` signal list,
which `main.go`'s `shutdownContext` now owns for the other commands. Passing
main's context in is not mechanical: `Run` installs the handler only on the
signing path, so Ctrl-C still kills the pass-through to the real gpg. Done when
one definition of "the shutdown signals" serves both, with the pass-through
behaviour kept.

Found by the 2026-11-19 unsmell run. (x-2)
