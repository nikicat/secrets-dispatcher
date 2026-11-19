---
status: todo
tags: [refactor, service]
---
# The unit-file layout has no type of its own

## Goal
The mask, drop-in, autostart-shadow and unit paths, and the checks for
whether a file is ours, are derived again in `plan.go`, `status.go`,
`install.go`, `try.go` and `gnomekeyring.go`, each from `unitDir()` and the
XDG directories. Done when one `layout` type owns those paths and checks and
the five files use it.

Found by the 2026-11-19 unsmell run. (b2-7)
