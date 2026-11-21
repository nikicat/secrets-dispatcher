---
status: todo
tags: [refactor, approval]
---
# approval.Manager internals repeat themselves

## Goal
Done when each item below is fixed as its "Fix" says.

- `internal/approval/manager.go:486-502`,
  `internal/approval/manager.go:508-530`,
  `internal/approval/manager.go:533-548`,
  `internal/approval/manager.go:552-567`,
  `internal/approval/manager.go:592-608` (repeated derivation). Approve,
  ApproveAndAutoApprove, Deny, Cancel and ApproveGPGSign each write the same
  block. It looks up the pending request, unlocks by hand on each early
  return, sets the resolution, deletes it, closes done, unlocks and notifies.
  The changeset added another hand unlock inside ApproveAndAutoApprove's copy.
  The block is on unchanged lines.
  Fix: One `(m *Manager) resolve(id string, res Resolution, admit
  func(*Request) error) (*Request, error)` with a deferred unlock that the
  five call, each adding only its own effect (event, cacheApproval, GPGResult,
  rule).
- `internal/approval/manager.go:761-802`,
  `internal/approval/manager.go:849-861` (computing loop with effects).
  checkAutoApproveRules prunes expired rules and searches for a match in one
  loop, and computes the loop-invariant callerExe inside it. The expiry rule
  is written twice with opposite comparisons (:772 Before, :856 After).
  CheckAutoApproveRules is a pass-through whose doc lists its callers. All of
  this is on unchanged lines.
  Fix: `AutoApproveRule.expired(now)` owns expiry. The match becomes one
  exported CheckAutoApproveRules that prunes with slices.DeleteFunc, then
  finds with slices.IndexFunc. Its doc keeps only that it returns a copy of
  the first matching rule, or nil.
- `internal/approval/manager.go:345-354`,
  `internal/approval/manager.go:378-387` (duplication). addHistory and the
  test-only AddHistoryEntry both prepend an entry to the history and trim it
  to historyMax, on unchanged lines.
  Fix: AddHistoryEntry and addHistory share one `pushHistory(entry)` under
  historyMu.
- `internal/approval/manager.go:789-793`,
  `internal/approval/manager.go:804-808`,
  `internal/approval/types.go:295-297` (duplicated knowledge). The rule that
  a permissive rule must cover every item of a batch is explained in
  checkAutoApproveRules' body, on autoApproveCoversAll, and on
  SecretMatcher.matches. ADR 0005 owns it, and SecretMatcher.matches already
  cites it. The manager.go lines are unchanged.
  Fix: autoApproveCoversAll's doc points to ADR 0005 instead of restating the
  rule, and the body comment goes.

Found by the 2026-11-19 unsmell run (b1-10, b1-11, b1-15, b1-16, b1-18).
