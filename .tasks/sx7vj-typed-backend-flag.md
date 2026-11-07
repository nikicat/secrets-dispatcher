---
status: todo
tags: [refactor, service]
---
# A typed --backend value

## Goal
The `service install --backend` value (empty, a path, or a keyword) travels raw
until `resolveBackendExec` (`internal/service/backend.go`) parses it. Its
keywords `gopass` and `gnome-keyring` are matched there and listed again in two
help texts. Done when a `service.Backend` type is parsed where the flag is read,
and the help texts list its keywords from one place.

## Related
Tt3kqa is the same fix for the install mode.

Found by the 2026-11-07 unsmell run.
