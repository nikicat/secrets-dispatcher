---
status: todo
tags: [test, approval]
---
# approval tests repeat their setup

## Goal
Done when each item below is fixed as its "Fix" says.

- `internal/approval/manager_test.go:24-37` (repeated derivation).
  internal/approval/manager_test.go writes the wait-for-pending poll loop 23
  times (outside this batch's files, unchanged lines).
  Fix: Each copy calls b1-7's waitPendingID (or a count-based sibling for the
  PendingCount loops).
- `internal/approval/autoapprove_test.go:96`,
  `internal/approval/autoapprove_test.go:131`,
  `internal/approval/autoapprove_test.go:163`,
  `internal/approval/autoapprove_test.go:212`,
  `internal/approval/autoapprove_test.go:238`,
  `internal/approval/autoapprove_test.go:263`,
  `internal/approval/autoapprove_test.go:305`,
  `internal/approval/autoapprove_test.go:340` (duplication). The other eight
  tests in the file write the same NewManager literal on unchanged lines.
  Fix: Each uses b1-8's newRuleManager.
- `internal/approval/manager_test.go:1985`, `internal/approval/batch_authz_test.go:22`:
  two more copies of the NewManager config literal.
  Fix: use `newRuleManager()` (autoapprove_test.go) or one shared helper.

Found by the 2026-11-19 unsmell run (b1-19, b1-20, r1-5).
