---
status: todo
tags: [cli, tests]
---
# The CLI tests build their parser twice

## Goal
settings_test.go parse and parseError (around lines 29-47) both repeat the
newParser and Parse setup.

## Next
One helper returns the parse result and its error; parse and parseError call it.

Found by the 2026-11-26 unsmell run.
