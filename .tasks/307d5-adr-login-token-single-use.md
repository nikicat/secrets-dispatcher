---
status: todo
tags: [docs, api]
---
# No ADR records why a login token is single-use

## Goal
`HandleAuth` (`internal/api/handlers.go`) explains in a body comment why a
login token can be redeemed only once (argv capture and replay). ADR 0006 does
not record it. Done when ADR 0006 states it and the comment cites the ADR.

Found by the 2026-11-19 unsmell run. (b3-18)
