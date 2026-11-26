---
status: todo
tags: [tests]
---
# A test still checks file modes by hand

## Goal
internal/companion/provision_test.go (around line 374) stats a file and
compares its permission bits by hand. testutil.AssertPerm now does that.

## Next
Use testutil.AssertPerm there. internal/unixsock/unixsock_test.go has the same
check but cannot use it, because testutil imports unixsock.

Found by the 2026-11-26 unsmell run.
