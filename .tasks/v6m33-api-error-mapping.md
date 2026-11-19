---
status: todo
tags: [refactor, api]
---
# api maps approval errors to status codes inline, eight times

## Goal
Handlers in `internal/api/handlers.go` map `approval.ErrNotFound` to 404 (six
times) and `approval.ErrNotAutoApprovable` to 422 (twice), and other errors to
500, inline and comparing with `==` instead of `errors.Is`, and
eight body comments there read the next line back. Done when one
`writeResolveError` helper using `errors.Is` does the mapping and the restating
comments are gone.

Found by the 2026-11-19 unsmell run. (b3-11, b3-14)
