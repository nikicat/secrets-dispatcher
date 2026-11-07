# ADR 0007: Approve from notifications, the CLI, or the web UI

Date: 2026-02-09 · Status: accepted

## Context

A request blocks its caller until it is decided, so the prompt has to reach the
user wherever they are and must not disappear before they act on it.

## Decision

Three surfaces over the same approval manager:

1. **Desktop notifications** (`org.freedesktop.Notifications`) with Approve and
   Deny actions, activated in-process.
2. **CLI**: `list`, `show`, `approve`, `deny`, `history`, for scripting.
3. **Web dashboard** with live updates over WebSocket, plus browser
   notifications.

Approval notifications are sent with **critical urgency, no `transient` hint and
`expire_timeout=0`**. Probing gnome-shell 46 (2026-07-18/19, confirmed against
its `messageTray.js` and `notificationDaemon.js`) showed that GNOME ignores
`expire_timeout` entirely; what keeps a banner alive there is critical urgency
without `transient`. `expire_timeout=0` is what spec-honoring daemons (dunst,
mako, KDE) need, where `-1` meant "use the server's default timeout".

A terminal UI for bulk approval exists only in the exploratory privsep path and
is not shipped.

## Consequences

- gnome-shell keeps at most three notifications per app
  (`MAX_NOTIFICATIONS_PER_SOURCE`) and destroys the oldest with
  `reason=expired`, which on the bus is indistinguishable from a timeout. A
  burst of requests can evict a pending approval (task Thaxg6).
- `NotificationClosed` and `ActionInvoked` are unicast to the sender, so tests
  observe them only through the notifier's own connection.
