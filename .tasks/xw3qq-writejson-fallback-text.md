---
status: todo
tags: [api, bug]
---
# JSON encode failures answer with text/plain

## Goal
`writeJSON` (`internal/api/handlers.go`) falls back to `http.Error` with a
hand-written JSON body when encoding fails, so that response goes out as
`text/plain`, unlike every other API error since Middleware moved to
`writeError`. Done when the fallback sends `application/json`.

Found by the 2026-11-08 unsmell run.
