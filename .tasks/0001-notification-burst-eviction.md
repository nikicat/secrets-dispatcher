---
status: todo
tags: [notifications, gnome]
---
# Keep a burst of approval notifications from evicting pending ones

## Goal
gnome-shell keeps at most three notifications per app
(`MAX_NOTIFICATIONS_PER_SOURCE = 3`) and destroys the oldest with
`reason=expired` when a fourth arrives. A burst of requests can therefore evict
a pending approval's notification while the request still waits. Done when a
burst never leaves a pending request without a visible notification on GNOME.

## Decisions
- Approval notifications already use critical urgency, no `transient` hint and
  `expire_timeout=0` (ADR 0007); this task is only about the per-source cap.

## Next
Pick between coalescing pending approvals into one summary notification and
capping concurrent approval notifications at three (queueing the rest). Test it
in the Tier-2 VM with more than three concurrent requests, keeping at most
three open per source while measuring (more measures eviction, not expiry).
