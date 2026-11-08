---
status: todo
tags: [refactor, proxy]
---
# One runner type for serve's downstreams

## Goal
The downstream runner closures in `runServe` (`main.go`) all capture
`approvalMgr`, the level, `TrimProcessChain`, the slow-upstream notifier, the
threshold and `upstreamAddr`. `proxy.Config` is written out twice with the same
fields. An empty `upstreamAddr` means "the session bus", and every runner
branches on it. `unix:path=` is prefixed by hand in two places in `main.go`,
plus `proxy/manager.go`.

Done when one runner type holds the shared settings, and an upstream address
type owns the "session bus or socket" choice and the `unix:path=` form.

## Related
Tkf361 changes the session-bus runner's retry.

Found by the 2026-11-07 unsmell run.
