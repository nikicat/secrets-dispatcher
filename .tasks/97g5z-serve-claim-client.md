---
status: todo
tags: [refactor, cli]
---
# runServe writes claim-or-exit twice

## Goal
`runServe` (`main.go`) writes the `approvalMgr.ClaimClient` check with its
"claimed twice (should be caught by validation)" message and `os.Exit` twice.
Done when one helper does it, or the runner type from Tspmgz owns it.

Found by the 2026-11-19 unsmell run. (b1-7)
