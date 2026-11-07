---
status: todo
tags: [refactor, cli, service]
---
# config edit restarts the service by service's own unit name

## Goal
`runConfigEdit` (`main.go`) restarts `secrets-dispatcher.service` by literal,
a name the `service` package owns as `proxyServiceUnit`. Done when `service`
exports the unit name, or a `Restart` function, and `runConfigEdit` uses it.

Found by the 2026-11-07 unsmell run.
