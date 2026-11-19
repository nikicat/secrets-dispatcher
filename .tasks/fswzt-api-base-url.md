---
status: todo
tags: [refactor, api]
---
# The API base URL is built in four places

## Goal
`"http://" + addr` builds the API base URL in `main.go` (twice),
`internal/service/try.go` and `internal/cli/client.go`. Done when one function
in `api` returns it and every site calls that.

Found by the 2026-11-19 unsmell run. (b1-8)
