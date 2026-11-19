---
status: todo
tags: [refactor, web]
---
# RequestCard and the web types carry untyped and duplicated shapes

## Goal
Done when each item below is fixed as its "Fix" says.

- `web/src/lib/RequestCard.svelte:110-159` (duplication). handleApprove,
  handleApproveAndAutoApprove and handleDeny are one body three times (set
  loading, clear error, call, onAction, classify ApiError against a fallback
  message, reset loading); only the action, the api call and the fallback text
  differ.
  Fix: One `run(action, call, fallback)` helper in the component; each
  button's onclick calls it with its api function
- `web/src/lib/RequestCard.svelte:66-79` (primitive obsession). typeBadgeLabel
  takes `type: string` instead of PendingRequest["type"], so its switch is not
  exhaustive: "unlock" and "ssh_sign" fall to the default and are labelled
  "Secret".
  Fix: Take PendingRequest["type"], label every member, and let an exhaustive
  check (a Record<PendingRequest["type"], string> or a never default) catch a
  new kind
- `web/src/lib/types.ts:107`, `web/src/lib/types.ts:139` (primitive
  obsession). AutoApproveRule.request_type is a string and
  TrustRule.request_types a string[], while the same values are
  PendingRequest["type"]; nothing links them, so HistoryEntry.svelte:150
  compares a typed kind with a bare string.
  Fix: type both as PendingRequest["type"] (a named RequestType alias used by
  all three)
- `web/src/lib/types.ts:25-42` (algebraic blindness). GPGSignInfo carries kind
  beside tag_name/target ("tag only") and pushee ("push only"); the 4-line
  comment on kind explains which fields matter per kind and how git routes
  objects, instead of the type saying it.
  Fix: A discriminated union on kind (commit, tag with tag_name and target,
  push with pushee, unknown) over the shared fields; the comment shrinks to
  what the type cannot say
- `web/src/lib/types.ts:20-21`, `web/src/lib/types.ts:145` (compressed doc).
  SenderInfo's trailing comments ("invoker process comm (display); spoofable",
  "real systemd unit (authoritative)") are sentences in trailing comments
  joined by a semicolon; "WebSocket message types" restates the union's name.
  Fix: Move the two facts to plain sentences above their fields (the invoker
  name is for display and can be spoofed; the unit is the trusted one) and
  delete the "WebSocket message types" line
- `web/src/lib/RequestCard.svelte:219-276`,
  `web/src/lib/RequestCard.svelte:362-418`,
  `web/src/lib/RequestCard.svelte:199-209` (duplication). The item card
  (label, copy button with two inline SVG icons, attributes table) is written
  twice, for search results and for other requests, and the attributes table a
  third time for the search criteria.
  Fix: An ItemCard component (or a Svelte snippet) and an AttributesTable
  snippet used by all three sites
- `web/src/lib/RequestCard.svelte:27-53` (narrating doc comment).
  formatSenderInfo's comments narrate its branches ("Fall back to …" three
  times, "Format sender info for display" restates the name, "If we have a
  unit name" calls invoker_name a unit), and its `if (!info)` guards a field
  that the type says is always present.
  Fix: delete the comments and the dead guard; the branches read on their own
- `internal/notification/desktop.go:496-509`,
  `web/src/lib/RequestCard.svelte:16-22` (repeated derivation).
  formatDurationShort is written twice, in Go for the notification's "Approve
  2m" button (desktop.go:496, called from :526, changed) and in TS for the
  web's "Approve 2m" button (RequestCard.svelte:16, called from the
  re-indented :443-450). The two agree today; the definitions are unchanged.
  Fix: The snapshot carries the label next to auto_approve_duration_seconds,
  built by the Go function, and RequestCard drops its copy; or keep both,
  since each surface formats for itself.

Found by the 2026-11-19 unsmell run (b4-7, b4-9, b4-10, b4-11, b4-12, b4-16, b4-17, x-8).
