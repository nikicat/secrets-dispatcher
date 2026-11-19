---
status: todo
tags: [test, notification]
---
# notification tests repeat their scaffolding

## Goal
Done when each item below is fixed as its "Fix" says.

- `internal/notification/desktop_test.go:637-650`,
  `internal/notification/desktop_test.go:676-688`,
  `internal/notification/desktop_test.go:716-728`,
  `internal/notification/desktop_test.go:744-756`,
  `internal/notification/desktop_test.go:781-793` (repeated derivation). Five
  ListenActions tests repeat the same scaffold (buffered channel, goroutine,
  50 ms sleep, cancel, wait) and four look the notification id up under h.mu.
  Fix: A helper `runAction(t, h, requestID, key)` that sends one action
  through ListenActions on a closed channel (so it returns without a sleep).
- `internal/notification/desktop_test.go:139-145`,
  `internal/notification/desktop_test.go:848-853` (duplication).
  newTestHandler and newTestHandlerWithDelay build the handler the same way
  Fix: newTestHandler returns newTestHandlerWithDelay(0)
- `internal/notification/desktop_test.go:629-630`,
  `internal/notification/desktop_test.go:670` (speculative generality).
  `notifID := mock.lastNotify(); _ = notifID` and `_ = mock` are dead locals
  kept alive by blank assignments.
  Fix: delete them (and take `_` for mock)

Found by the 2026-11-19 unsmell run (b2-22, b2-23, b2-25).
