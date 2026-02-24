---
phase: 03-ui-and-observability
plan: 02
subsystem: ui
tags: [cli, formatter, gpg_sign, git-log]

# Dependency graph
requires:
  - phase: 02-core-signing-flow
    provides: "GPGSignInfo struct with JSON field names; gpg_sign request type in API"
provides:
  - "cli.GPGSignInfo struct (intentional duplication, no cross-package import)"
  - "GPGSignInfo field on cli.PendingRequest for JSON deserialization"
  - "gpg_sign display in list (file count), show (git-log style), and history (file count)"
  - "SUMMARY column header replacing SECRET in list and history tables"
affects: [03-ui-and-observability]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Intentional struct duplication: cli package owns its own GPGSignInfo to avoid cross-package imports"
    - "Type-dispatch via nil pointer check: if req.GPGSignInfo != nil routes to gpg_sign branch"
    - "git-log style output: indented commit subject/body with 4-space prefix"

key-files:
  created:
    - "internal/cli/format_test.go"
  modified:
    - "internal/cli/client.go"
    - "internal/cli/format.go"

key-decisions:
  - "Intentional struct duplication for GPGSignInfo in cli package — no import of internal/approval or internal/api"
  - "requestSummary (renamed from secretSummary) dispatches on GPGSignInfo != nil first, falls through to existing get_secret/search logic"
  - "Committer line in show output is suppressed when Committer == Author (common case)"
  - "commitBody strips trailing newlines — git appends trailing newline to commit objects"

patterns-established:
  - "requestSummary pattern: check GPGSignInfo first, then Items, then SearchAttributes, then fallback"
  - "formatRequest gpg_sign block: repo/author/key, then indented commit message, then changed files, then optional secondary fields"

requirements-completed: [DISP-04, DISP-06]

# Metrics
duration: 2min
completed: 2026-02-24
---

# Phase 3 Plan 2: CLI gpg_sign Display Summary

**gpg_sign display in CLI list/show/history: file count summary in SUMMARY column, git-log style show output (repo, author, key, indented commit message, changed files list)**

## Performance

- **Duration:** 2 min
- **Started:** 2026-02-24T19:17:34Z
- **Completed:** 2026-02-24T19:20:00Z
- **Tasks:** 2
- **Files modified:** 3 (client.go, format.go, format_test.go created)

## Accomplishments
- Added `GPGSignInfo` struct to `cli` package with correct JSON tags matching `approval.GPGSignInfo` (intentional duplication — no cross-package import)
- Added `GPGSignInfo *GPGSignInfo` pointer field to `PendingRequest` enabling deserialization of gpg_sign API responses
- Renamed `secretSummary` to `requestSummary` with gpg_sign branch: "1 file" / "N files" based on `ChangedFiles` length
- Renamed column header from "SECRET" to "SUMMARY" in both `list` and `history` table output
- Added git-log style `formatRequest` branch for gpg_sign: Repo, Author, Key, indented commit subject+body, Changed files list, optional Committer (when differs) and Parent hash
- Added `commitSubject` and `commitBody` helpers for commit message parsing
- Added 13 new tests in `format_test.go` covering all new behaviors; all 23 tests (10 existing + 13 new) pass

## Task Commits

Each task was committed atomically:

1. **Task 1: Add GPGSignInfo struct and field to cli.PendingRequest** - `c294341` (feat)
2. **Task 2: Add gpg_sign branches to CLI formatters** - `ab8b840` (feat)

## Files Created/Modified
- `internal/cli/client.go` - Added `GPGSignInfo` struct and pointer field on `PendingRequest`
- `internal/cli/format.go` - Renamed column headers, renamed function, added gpg_sign display branches and helper functions
- `internal/cli/format_test.go` - New file: 13 tests for requestSummary, formatRequest gpg_sign, column headers, commitSubject/commitBody

## Decisions Made
- Intentional struct duplication: `cli.GPGSignInfo` mirrors `approval.GPGSignInfo` field-for-field but lives in the cli package. The cli package deliberately avoids importing internal packages.
- Committer line in `show` output is suppressed when `Committer == Author` (the common case where the committer is the same person).
- `commitBody` strips trailing newlines to match how git appends a trailing newline to commit objects (consistent with Phase 02-02 decision).

## Deviations from Plan

### Auto-added Tests

**[Rule 2 - Missing Critical] Added format_test.go with formatter unit tests**
- **Found during:** Task 2 (format.go changes)
- **Issue:** The plan mentioned "existing tests still pass" but the existing test file only had client HTTP tests — no formatter tests existed despite new formatting logic being added
- **Fix:** Created `format_test.go` with 13 tests covering all new gpg_sign display branches, helper functions, column headers, and regression tests for get_secret behavior
- **Files modified:** `internal/cli/format_test.go` (created)
- **Verification:** All 23 tests pass with -race flag
- **Committed in:** ab8b840 (Task 2 commit)

---

**Total deviations:** 1 auto-added (missing tests for new feature code)
**Impact on plan:** Tests are essential for correctness verification of the new display branches. No scope creep.

## Issues Encountered
None — plan executed cleanly. Both tasks built and passed tests on first attempt.

## Next Phase Readiness
- CLI display for gpg_sign is complete across all three commands (list, show, history)
- format.go is ready for any additional request types that may follow the same dispatch pattern
- No blockers for subsequent 03-xx plans

---
*Phase: 03-ui-and-observability*
*Completed: 2026-02-24*
