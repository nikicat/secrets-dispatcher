---
status: todo
tags: [refactor, companion]
---
# The companion's D-Bus policy path is spelled twice

## Goal
`/usr/share/dbus-1/system.d/net.mowaka.SecretsDispatcher1.conf` is written by
`internal/companion/provision.go` and checked by `check.go`, each spelling the
path. Done when one `dbusPolicyPath` constant (built from `daemon.BusName` if
that fits) serves both, as `pamConfigPath` already does.

Found by the 2026-11-20 unsmell run (r1c-12).
