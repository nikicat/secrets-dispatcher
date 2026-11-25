---
status: todo
tags: [unsmell]
---
# Leftovers of the 2026-11-25 11:51 unsmell run

## Goal
Done when each item below is fixed as its "Fix" says. Each sits on lines that
the run did not change, so the run planned it instead of fixing it.

### service

- `internal/service/install.go:30`, `:106`, `:167`, `:203` (doc opens with its
  name). The docs of allUnitNames, Options.Start, lookPathFunc and
  ownerWaitTimeout start with the item's own name. Fix: delete the
  allUnitNames doc. Options.Start reads "True if Install starts the units after
  enabling them." lookPathFunc reads "A test seam for exec.LookPath.", or loses
  its doc. ownerWaitTimeout reads "Bounds the ownership polls after a start or
  a restore. Tests shorten it." (b1-9)
- `internal/service/backend.go` (gopassExec, gnomeKeyringDaemon),
  `internal/service/topology.go` (takeover.units, dbus-daemon) (repeated
  derivation). A lookPathFunc call followed by `fmt.Errorf("find <name>: %w")`
  is written three times. gnomeKeyringDaemon is that helper for one name only.
  Fix: one `findProgram(name string) (string, error)` that wraps the error as
  "find <name>: %w", used by all three. gnomeKeyringDaemon goes, and
  gnomekeyring.go calls findProgram. (b1-10)
