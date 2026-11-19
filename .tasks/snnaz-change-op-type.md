---
status: todo
tags: [refactor, service]
---
# Change.Op is a bare string

## Goal
`service.Change.Op` (`internal/service/plan.go`) is a string matched as
"write", "run" and the like by `Plan`, its printer and its tests. Done when it
is a typed constant set.

Found by the 2026-11-19 unsmell run. (b2-13)
