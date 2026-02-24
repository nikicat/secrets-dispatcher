---
phase: 03-ui-and-observability
plan: 03
subsystem: ui
tags: [svelte5, typescript, css, browser-notifications, gpg-sign]

# Dependency graph
requires:
  - phase: 02-core-signing-flow
    provides: GPGSignInfo Go struct and gpg_sign request type emitted by daemon

provides:
  - GPGSignInfo TypeScript interface matching Go JSON tags
  - PendingRequest type union extended to include "gpg_sign"
  - Purple/violet gpg_sign card with commit subject, author, key ID, changed files, expandable body
  - Type badge (GPG Sign / Secret / Search) on ALL card types in RequestCard
  - Session identity line (PID + repo for gpg_sign, PID for others) on ALL cards
  - History entries showing gpg_sign type badge and commit subject
  - Browser notifications with correct titles per request type

affects:
  - 03-ui-and-observability
  - future-phases

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "Svelte 5 runes: $state, $props, $effect used throughout new UI logic"
    - "CSS class modifier pattern: card--{type} for per-type styling"
    - "Conditional template branching: {#if type === 'gpg_sign'} for type-specific content"

key-files:
  created: []
  modified:
    - web/src/lib/types.ts
    - web/src/lib/RequestCard.svelte
    - web/src/lib/notifications.ts
    - web/src/App.svelte
    - web/src/app.css

key-decisions:
  - "Notification title changed from 'Secret Access Request' to 'Secret Request' (shorter) and 'Commit Signing Request' for gpg_sign"
  - "Type badge and session identity applied to ALL card types, not just gpg_sign — consistent visual scanning"
  - "Changed files list shows first 5 inline with <details> expand for overflow — avoids overwhelming card height"

patterns-established:
  - "typeBadgeLabel/historyItemsSummary helpers: single function per type-specific display logic, avoids inline conditionals"
  - "card--{type} CSS modifier: extensible pattern for future request types without changing component structure"

requirements-completed: [DISP-02, DISP-03, DISP-05]

# Metrics
duration: 3min
completed: 2026-02-24
---

# Phase 3 Plan 03: UI gpg_sign Support Summary

**Svelte 5 web UI extended with purple-accented gpg_sign cards, type badges on all cards, and per-type browser notification titles**

## Performance

- **Duration:** 3 min
- **Started:** 2026-02-24T10:37:30Z
- **Completed:** 2026-02-24T10:40:34Z
- **Tasks:** 3
- **Files modified:** 5

## Accomplishments

- GPGSignInfo TypeScript interface and gpg_sign union member added to PendingRequest — web UI now types signing requests correctly
- RequestCard extended with gpg_sign branch: purple left border, commit subject headline, author/key metadata, expandable commit body, changed files list (first 5 + overflow expand), secondary details section
- Type badge ("GPG Sign" / "Secret" / "Search") and session identity (PID + repo) added to ALL card types for consistent visual scanning
- History entries updated: type badge handles three types with purple accent for gpg_sign, items summary shows commit subject for gpg_sign entries
- Browser notifications use "Commit Signing Request" for gpg_sign, "Secret Request" for others, with repo/commit subject in body

## Task Commits

1. **Task 1: Update TypeScript types and CSS variables** - `a795275` (feat)
2. **Task 2: Add gpg_sign card content, type badge, and session identity to RequestCard** - `5f27144` (feat)
3. **Task 3: Update App.svelte history view and browser notifications** - `88e310a` (feat)

## Files Created/Modified

- `web/src/lib/types.ts` - Added GPGSignInfo interface; extended PendingRequest.type union to include "gpg_sign"; added gpg_sign_info? optional field
- `web/src/app.css` - Added --color-gpg-sign (#8b5cf6), --color-gpg-sign-bg, --color-gpg-sign-border (#7c3aed) CSS variables
- `web/src/lib/RequestCard.svelte` - Full gpg_sign card implementation: type badge, session identity, gpg_sign content branch, purple border, all new CSS classes
- `web/src/lib/notifications.ts` - Conditional notification title; gpg_sign branch in formatBody with repo name and commit subject
- `web/src/App.svelte` - Updated formatSenderInfo with repo prefix for gpg_sign; added historyItemsSummary helper; updated both history type badge spans; added .history-type--gpg_sign CSS

## Decisions Made

- Notification title simplified from "Secret Access Request" to "Secret Request" (shorter, cleaner) per plan locked decision
- Type badge and session identity applied universally to all card types — consistent mental model for users scanning multiple request types
- Changed files list: first 5 inline, rest behind `<details>` — balances information density vs. card height for large commits

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None - `deno task check` fails due to missing tsconfig.json (pre-existing issue unrelated to this plan), but `deno task build` passes cleanly.

## Next Phase Readiness

- Web UI fully supports gpg_sign request display end-to-end
- Visual distinction (purple accent) makes signing requests immediately distinguishable from secret requests
- All type badges and session identity lines are in place for consistent UX across all three request types
- Ready for integration testing with real gpg_sign requests from the thin client

---
*Phase: 03-ui-and-observability*
*Completed: 2026-02-24*
