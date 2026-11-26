---
status: todo
tags: [config]
---
# Topology checks live in Settings.validate

## Goal
The checks on the serve upstream and downstream (internal/config/config.go,
Settings.validate, around lines 139-154) are about config.TopologyConfig, which
is now a type of its own. They belong on it.

## Next
`TopologyConfig.validate()` holds them, and Settings.validate calls it.

Found by the 2026-11-26 unsmell run.
