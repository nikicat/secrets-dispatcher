---
status: todo
tags: [refactor, api]
---
# Route the API with ServeMux patterns

## Goal
`api.newRouter` builds the routing table inline, with dispatch
closures. `/api/v1/pending/` actions are matched by `strings.HasSuffix` on the
path, and each handler parses the id again. The route paths are spelled in the
router and again in the clients: `internal/apiclient` callers in `gpgsign`
(daemon.go) and `cli` (client.go) pass paths and `apiclient.Action` values as
literals.

Done when the router uses Go 1.22 `ServeMux` patterns with `r.PathValue`, and
the server and its clients share route constants.

Found by the 2026-11-07 unsmell run.
