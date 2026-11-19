---
status: todo
tags: [refactor, api]
---
# main builds the API Resolver twice

## Goal
`runServe` (`main.go`) builds `api.NewResolver(approvalMgr, slowUpstreamNotifier,
upstreamSlowThreshold)` and passes the same three values to
`NewServerWithProvider`, which builds a second Resolver inside
`NewHandlersWithProvider`. Done when main builds the Resolver once and the
constructors take it.

## Related
T977rb's config struct for `api.Server` touches the same constructors.

Found by the 2026-11-19 unsmell run. (x-4)
