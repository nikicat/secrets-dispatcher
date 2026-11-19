---
status: todo
tags: [refactor, api]
---
# api maps ErrNotFound to 404 six times

## Goal
Six handlers in `internal/api/handlers.go` map `approval.ErrNotFound` to 404
and other errors to 500 inline, comparing with `==` instead of `errors.Is`, and
eight body comments there read the next line back. Done when one
`writeResolveError` helper using `errors.Is` does the mapping and the restating
comments are gone.

Found by the 2026-11-19 unsmell run. (b3-11, b3-14)
