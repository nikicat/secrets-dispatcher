---
status: todo
tags: [config, service]
---
# The remote serve defaults are built in two places

## Goal
`config.withDefaults` (internal/config/config.go, around line 79) fills an
unset serve upstream and downstream with the same pair that the remote
install topology's serve() builds (internal/service/topology.go). The two
must agree but are written separately. Since install now reads the mode back
from the config (installedMode), a hand-written config that leaves the
upstream unset runs as remote but matches no install mode, and
`service install` without --mode errors.

## Next
One owner for the remote pair: withDefaults and the remote topology build it
from the same function, and installedMode compares the config after defaults
are applied.

Found by the 2026-11-25 unsmell run.
