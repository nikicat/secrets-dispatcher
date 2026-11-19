---
status: todo
tags: [refactor, notification]
---
# DBusNotifier repeats its reconnect-and-retry and spells D-Bus names twice

## Goal
Done when each item below is fixed as its "Fix" says.

- `internal/notification/desktop.go:221-233`,
  `internal/notification/desktop.go:264-276`,
  `internal/notification/desktop.go:280-292` (repeated derivation). Notify,
  NotifyPersistent and Close each call, check errors.Is(err, dbus.ErrClosed),
  reconnect, wrap a reconnect failure and retry once: one rule written three
  times.
  Fix: `func (n *DBusNotifier) retryOnClosed(op func() error) error` holding
  the lock, the reconnect and the single retry; the three methods call it.
- `internal/notification/desktop.go:235`,
  `internal/notification/desktop.go:225`,
  `internal/notification/desktop.go:264-276` (speculative generality).
  doNotifyFull takes urgency and expireTimeout, but every call passes (2, 0),
  so the ADR 0007 rule is spelled four times with a "critical, never expire"
  comment. With that, NotifyPersistent is Notify with nil actions.
  Fix: doNotifyFull drops the two parameters and owns the constants (named,
  citing ADR 0007); NotifyPersistent goes, and slow_upstream's notifier
  interface uses Notify(…, nil).
- `internal/notification/desktop.go:53` (primitive obsession). Closed.Reason
  is a raw uint32 whose meaning (1 expired, 2 dismissed, 3 closed by call, 4
  undefined) lives only in a trailing comment; the input stays unparsed past
  processSignals.
  Fix: a `closeReason` type with named constants and a String method
- `internal/notification/desktop.go:86-87`,
  `internal/notification/desktop.go:114-117` (narrating doc comment).
  connect's doc says it subscribes to ActionInvoked signals, but it also
  subscribes to NotificationClosed; both connect and reconnect phrase the lock
  precondition as "Must be called with n.mu held"; reconnect's doc narrates
  the goroutine restart. Both open with their own names.
  Fix: connect: "Opens a private session-bus connection that delivers
  ActionInvoked and NotificationClosed signals. The caller holds n.mu."
  reconnect: "Replaces the connection with a new one. The caller holds n.mu."
- `internal/notification/desktop.go:136-142`,
  `internal/notification/desktop.go:196-199` (duplicated knowledge). "Only the
  e2e notifprobe drains ClosedEvents and a full buffer drops events" is stated
  in ClosedEvents' doc, in processSignals' body and in desktop_bus_test.go;
  ClosedEvents' doc is eight lines and names its caller.
  Fix: processSignals' comment owns the drop rule; ClosedEvents' doc keeps one
  (b) fact ("Events are dropped while the buffer is full.") and the others
  point to it.
- `internal/notification/desktop.go:211-220` (long comment). Notify's doc is
  nine lines that restate ADR 0007's decision (critical urgency, no transient
  hint, expire 0, the GNOME 46 probe) instead of linking it.
  Fix: "Sends a notification that stays until it is closed (ADR 0007).
  Reconnects and retries once when the connection has dropped."
- `internal/notification/desktop_test.go:974-995` (duplication).
  newTestDBusNotifier rebuilds DBusNotifier by hand, copying connect's
  ActionInvoked subscription and leaving out the NotificationClosed one and
  the closed channel.
  Fix: useTestSessionBus then NewDBusNotifier()
- `internal/notification/desktop.go:96`,
  `internal/notification/desktop.go:103`,
  `internal/notification/desktop.go:177`,
  `internal/notification/desktop.go:190` (split protocol). The signal member
  names "ActionInvoked" and "NotificationClosed" are spelled where connect
  subscribes and again where processSignals matches sig.Name (and in the test
  helper).
  Fix: constants beside notifyInterface, used by both
- `internal/notification/desktop.go:29`,
  `internal/notification/desktop.go:221`,
  `internal/notification/desktop.go:235`,
  `internal/notification/desktop.go:264` (data clump). (summary, body, icon)
  travel together through Notifier.Notify, DBusNotifier.Notify, doNotifyFull,
  NotifyPersistent and the mock.
  Fix: One message type {summary, body, icon, buttons}, the same type b2-2
  would give the buttons.

Found by the 2026-11-19 unsmell run (b2-9, b2-10, b2-11, b2-12, b2-13, b2-14, b2-24, b2-26, b2-27).
