---
status: todo
tags: [refactor, approval]
---
# approval.Manager internals repeat themselves, and notify's doc is false

## Goal
Done when each item below is fixed as its "Fix" says.

- `internal/approval/manager.go:482-498`,
  `internal/approval/manager.go:503-524`,
  `internal/approval/manager.go:527-542`,
  `internal/approval/manager.go:546-561`,
  `internal/approval/manager.go:586-602` (repeated derivation). Approve,
  ApproveAndAutoApprove, Deny, Cancel and ApproveGPGSign each write the same
  block. It looks up the pending request, unlocks by hand on each early
  return, sets the resolution, deletes it, closes done, unlocks and notifies.
  The changeset added another hand unlock inside ApproveAndAutoApprove's copy.
  The block is on unchanged lines.
  Fix: One `(m *Manager) resolve(id string, res Resolution, admit
  func(*Request) error) (*Request, error)` with a deferred unlock that the
  five call, each adding only its own effect (event, cacheApproval, GPGResult,
  rule).
- `internal/approval/manager.go:757-811`,
  `internal/approval/manager.go:860-873` (computing loop with effects).
  checkAutoApproveRules prunes expired rules and searches for a match in one
  loop, and computes the loop-invariant callerExe inside it. The expiry rule
  is written twice with opposite comparisons (:778 Before, :868 After).
  CheckAutoApproveRules is a pass-through whose doc lists its callers. All of
  this is on unchanged lines.
  Fix: `AutoApproveRule.expired(now)` owns expiry. The match becomes one
  exported CheckAutoApproveRules that prunes with slices.DeleteFunc, then
  finds with slices.IndexFunc. Its doc keeps only that it returns a copy of
  the first matching rule, or nil.
- `internal/approval/manager.go:957-969` (misplaced rule). invokerExePath is a
  free function that only reads a SenderInfo. The changeset added a third
  caller (AutoApprovable at :117). Its sibling hasLocalProcess already lives
  on SenderInfo (types.go:37). The function is unchanged.
  Fix: `func (s SenderInfo) invokerExe() string` beside hasLocalProcess in
  types.go; its doc keeps the fail-closed fact and cites ADR 0005.
- `internal/approval/manager.go:826-829`,
  `internal/approval/manager.go:1161-1164` (duplication). Both
  autoApproveCoversAll and matchSecretItem replace a nil Attributes map with
  an empty one before calling attributesMatch. That code is dead, because
  indexing a nil map in Go yields "". The lines are unchanged.
  Fix: Delete both conversions and pass it.Attributes directly.
- `internal/approval/manager.go:307-313`,
  `internal/approval/manager.go:340-346` (duplication). addHistory and the
  test-only AddHistoryEntry both prepend an entry to the history and trim it
  to historyMax, on unchanged lines.
  Fix: AddHistoryEntry and addHistory share one `pushHistory(entry)` under
  historyMu.
- `internal/approval/manager.go:798-802`,
  `internal/approval/manager.go:813-817`,
  `internal/approval/manager.go:1117-1126` (duplicated knowledge). The rule
  that a permissive rule must cover every item of a batch is explained in
  checkAutoApproveRules' body, on autoApproveCoversAll, and on matchSecret.
  The lines are unchanged.
  Fix: matchSecret's doc owns it; autoApproveCoversAll points to it ("Covers a
  batch as an approve rule's matchSecret does"), and the body comment goes.
- `internal/approval/manager.go:255` (restated code). notify's doc says it
  sends to observers "asynchronously". It calls each OnEvent synchronously
  while holding observersMu, and it also records history, which the doc leaves
  out. The line is unchanged.
  Fix: "Delivers event to every observer, in the caller's goroutine, and
  records a request's end in History."

Found by the 2026-11-19 unsmell run (b1-10, b1-11, b1-12, b1-13, b1-15, b1-16, b1-18).
