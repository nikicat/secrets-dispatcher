---
status: todo
tags: [refactor, service]
---
# A typed install mode

## Goal
`service.Install` and `Plan` branch on the strings `"remote"`, `"local"` and
`"full"` (`internal/service/plan.go`, `install.go`). Done when a `Mode` type
with constants is parsed where the flag is read, and the service code switches
on the constants.

## Related
Tsx7vj is the same fix for `--backend`.

Found by the 2026-11-05 unsmell run.
