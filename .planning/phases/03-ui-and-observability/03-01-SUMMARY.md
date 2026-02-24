---
phase: 03-ui-and-observability
plan: 01
subsystem: ui
tags: [dbus, desktop-notification, gpg-sign, approval]

# Dependency graph
requires:
  - phase: 02-core-signing-flow
    provides: approval.RequestTypeGPGSign, GPGSignInfo struct with RepoName/CommitMsg fields
provides:
  - Per-type desktop notification dispatch (gpg_sign vs get_secret/search)
  - Commit Signing Request notification with repo name and commit subject
  - emblem-important icon for gpg_sign, dialog-password for secrets
  - Renamed get_secret title from Secret Access Request to Secret Request
affects:
  - Any future UI phases that extend notification content

# Tech tracking
tech-stack:
  added: []
  patterns:
    - notificationMeta() method pattern for per-request-type title/icon dispatch
    - commitSubject() helper to extract first line of multi-line commit messages

key-files:
  created: []
  modified:
    - internal/notification/desktop.go
    - internal/notification/desktop_test.go

key-decisions:
  - "Notifier interface adds icon as third positional param — avoids options struct overhead for a simple three-field call"
  - "notificationMeta() centralizes title+icon dispatch — single switch rather than duplicating per-type logic in handleCreated and formatBody"
  - "commitSubject() extracts first line via strings.IndexByte — avoids strings.Split allocation for common case"
  - "emblem-important chosen for gpg_sign icon — signals action required, visually distinct from dialog-password"

patterns-established:
  - "Per-type notification meta: notificationMeta() returns (summary, icon) tuple from req.Type switch"
  - "commitSubject(): first-line extraction with IndexByte, no allocation on single-line messages"

requirements-completed:
  - DISP-01

# Metrics
duration: 3min
completed: 2026-02-24
---

# Phase 3 Plan 01: Desktop Notification gpg_sign Support Summary

**Per-type desktop notification dispatch for gpg_sign requests: Commit Signing Request title, emblem-important icon, and repo+commit-subject body; get_secret title renamed from Secret Access Request to Secret Request**

## Performance

- **Duration:** 3 min
- **Started:** 2026-02-24T10:57:25Z
- **Completed:** 2026-02-24T11:00:42Z
- **Tasks:** 1
- **Files modified:** 2

## Accomplishments
- Extended `Notifier` interface with `icon` parameter; `DBusNotifier` now dispatches per-type icon via D-Bus
- Added `notificationMeta()` method dispatching title and icon by request type (gpg_sign vs. default)
- Added `gpg_sign` case to `formatBody` showing repo name and commit subject (first line only)
- Renamed get_secret notification title from "Secret Access Request" to "Secret Request"
- Added `TestHandler_OnEvent_GPGSignRequest` and `TestHandler_OnEvent_GetSecretIcon` covering new behavior

## Task Commits

Each task was committed atomically:

1. **Task 1: Add per-type icon to Notifier interface and gpg_sign notification branch** - `80b9c15` (feat)

**Plan metadata:** (docs commit follows)

## Files Created/Modified
- `internal/notification/desktop.go` - Updated Notifier interface, DBusNotifier, notificationMeta(), formatBody gpg_sign case, commitSubject helper
- `internal/notification/desktop_test.go` - Updated mockNotifier with icon field, updated existing assertion, added 2 new tests

## Decisions Made
- `Notifier` interface adds `icon` as third positional param rather than an options struct — keeps the call site simple for a stable three-field signature
- `notificationMeta()` centralizes title+icon dispatch in one method so `handleCreated` and `formatBody` stay independent of per-type logic
- `commitSubject()` uses `strings.IndexByte` for zero-allocation extraction on single-line messages
- `emblem-important` chosen as the gpg_sign icon — standard freedesktop icon that signals "action required", visually distinct from `dialog-password`

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None - no external service configuration required.

## Next Phase Readiness
- Desktop notification layer now covers all three request types (get_secret, search, gpg_sign)
- Ready for Phase 3 Plan 02 (web UI / observability work)
- No blockers

---
*Phase: 03-ui-and-observability*
*Completed: 2026-02-24*
