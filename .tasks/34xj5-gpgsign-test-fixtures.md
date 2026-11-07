---
status: todo
tags: [refactor, gpgsign, tests]
---
# Shared fixtures in the gpgsign tests

## Goal
Two tests build the redirecting `DaemonClient` inline, which `newTestClient`
already does. Two `TestLoadAuthToken` subtests build the same cookie fixture.
Done when both use shared helpers.

## Related
T2djvz moves `loadAuthToken`; do this after it, or with it.

Found by the 2026-11-07 unsmell run.
