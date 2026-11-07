---
status: todo
tags: [refactor, proxy]
---
# A retry helper for the session-bus runner

## Goal
The `session_bus` runner in `runServe` (`main.go`) hand-rolls exponential
backoff inside topology setup. Done when the backoff is a small retry helper, or
a session type that owns reconnecting, and the runner only says what to retry.

## Related
Tspmgz restructures the same runners.

Found by the 2026-11-05 unsmell run.
