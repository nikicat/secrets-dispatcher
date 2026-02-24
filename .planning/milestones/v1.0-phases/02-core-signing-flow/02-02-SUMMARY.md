---
phase: 02-core-signing-flow
plan: "02"
subsystem: gpgsign
tags: [go, gpg, tdd, bufio, commit-parsing, path-scanning]

# Dependency graph
requires:
  - phase: 02-core-signing-flow
    provides: "Research on git commit object format, gpg invocation args, PATH scanning pattern"
provides:
  - "ParseCommitObject: parses raw git commit objects into author/committer/message/parentHash"
  - "FindRealGPG: scans PATH for real gpg binary, skips self via inode comparison"
  - "extractKeyID: parses key ID from git's combined/separate -u flag invocation"
affects:
  - 02-core-signing-flow/02-04 (Run() calls all three functions)

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "bufio.Scanner for header/body git object parsing"
    - "os.SameFile for inode-level self-detection in PATH scan"
    - "Combined short-flag suffix detection for gpg arg parsing (ends with 'u')"

key-files:
  created:
    - internal/gpgsign/commit.go
    - internal/gpgsign/commit_test.go
    - internal/gpgsign/gpg.go
    - internal/gpgsign/gpg_test.go
  modified: []

key-decisions:
  - "First parent hash wins for merge commits (multiple parent lines): display context only, not security-critical"
  - "Trailing newline stripped from commit message body (git appends trailing newline to commit objects)"
  - "Combined short-flag detection: arg starts with '-', not '--', and ends with 'u' means next arg is key ID"

patterns-established:
  - "ParseCommitObject: header/body split on blank line; first-wins for repeated headers"
  - "FindRealGPG: os.Executable + os.Stat + os.SameFile self-detection before PATH scan"

requirements-completed: [SIGN-02]

# Metrics
duration: 5min
completed: 2026-02-24
---

# Phase 02 Plan 02: Thin Client Pure Functions Summary

**`internal/gpgsign` package with TDD-tested ParseCommitObject, FindRealGPG, and extractKeyID — zero new dependencies, 15 passing tests**

## Performance

- **Duration:** 5 min
- **Started:** 2026-02-24T12:58:16Z
- **Completed:** 2026-02-24T13:03:00Z
- **Tasks:** 2 (RED + GREEN)
- **Files modified:** 4 created

## Accomplishments

- `ParseCommitObject` correctly extracts author, committer, message, and first parent hash from any git commit object format (root, single-parent, merge)
- `FindRealGPG` scans PATH directories and skips self via `os.SameFile` inode comparison — the critical mechanism that prevents the thin client from recursively calling itself
- `extractKeyID` handles both `-bsau <keyID>` (combined short flags) and `-bsa -u <keyID>` (separate flag) patterns as observed in git's live GPG invocations

## Task Commits

Each task was committed atomically:

1. **Task 1: RED — failing tests** - `f968841` (test)
2. **Task 2: GREEN — implementation** - `7faf23e` (feat)

**Plan metadata:** *(docs commit follows)*

## Files Created/Modified

- `internal/gpgsign/commit.go` — `ParseCommitObject([]byte) (author, committer, message, parentHash string)` using `bufio.Scanner`; first parent wins; trailing newline stripped
- `internal/gpgsign/commit_test.go` — 6 test cases covering standard, parent, merge, empty message, multi-line, trailing-newline-stripping
- `internal/gpgsign/gpg.go` — `FindRealGPG() (string, error)` with inode-level self-skip; `extractKeyID([]string) string` with combined-flag detection
- `internal/gpgsign/gpg_test.go` — 4 cases for FindRealGPG (happy, not-found, only-self, self+real) and 5 cases for extractKeyID

## Decisions Made

- **First parent for merge commits:** Multiple `parent` lines in a merge commit — only the first is stored in `parentHash`. This is display-only context; collecting all parents would complicate `GPGSignInfo` without adding value.
- **Trailing newline stripping:** Git appends a trailing newline to the commit message body when writing to gpg's stdin. `strings.TrimRight(..., "\n")` normalizes this so `CommitMsg` doesn't carry a spurious newline in the approval UI.
- **Combined short-flag detection:** A short flag (starts with `-`, not `--`) that ends with `u` means the next argument is the key ID. This covers `-bsau`, `-bsu`, and `-u` without special-casing each variant.

## Deviations from Plan

None — plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None — no external service configuration required.

## Next Phase Readiness

- `internal/gpgsign` package is ready for Plan 04 (`Run()` implementation) which calls all three functions
- No unresolved issues; all 15 tests pass, `go vet` and `go build ./...` clean

---
*Phase: 02-core-signing-flow*
*Completed: 2026-02-24*
