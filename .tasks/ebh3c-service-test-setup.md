---
status: todo
tags: [test, service]
---
# Service tests repeat their setup

## Goal
In `internal/service`'s tests the XDG `t.Setenv` triple appears in about 28
tests and the private-bus unit list three times. Done when test helpers own each.

Found by the 2026-11-19 unsmell run. (b2-11)
