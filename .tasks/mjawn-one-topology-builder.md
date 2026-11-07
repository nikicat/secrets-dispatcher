---
status: todo
tags: [refactor, config, service]
---
# One builder for the default topology

## Goal
`service.installInputs.writeTopology`'s remote case and
`config.WithDefaults`' default downstream build the same topology. Done when one
builder in `config` makes it, and both call it.

Found by the 2026-11-07 unsmell run.
