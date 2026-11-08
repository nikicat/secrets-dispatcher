---
status: todo
tags: [refactor, cli]
---
# One table of CLI commands

## Goal
`main` and `runCLI` (`main.go`) both switch on the five CLI command names
(list, show, approve, deny, history). Done when the names are dispatched from
one place.

Found by the 2026-11-07 unsmell run.
