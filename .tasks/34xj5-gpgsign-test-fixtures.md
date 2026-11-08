---
status: todo
tags: [refactor, gpgsign, tests]
---
# Shared test client in the gpgsign tests

## Goal
Two tests build the redirecting `DaemonClient` inline, which `newTestClient`
already does. Done when they use it.

Found by the 2026-11-07 unsmell run.
