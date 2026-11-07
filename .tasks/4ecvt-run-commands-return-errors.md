---
status: todo
tags: [refactor, cli]
---
# Subcommand runners return errors instead of exiting

## Goal
`main.go` has about 35 `fmt.Fprintf(os.Stderr, …)` + `os.Exit(1)` pairs inside
the `run*` functions. `os.Exit` skips deferred cleanup, so sockets stay on disk
until the next start removes them. Done when each `run*` returns an error and
`main` prints it and exits in one place.

Found by the 2026-11-05 unsmell run.
