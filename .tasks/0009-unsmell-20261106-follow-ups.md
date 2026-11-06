---
status: todo
tags: [refactor, unsmell, web]
---
# Follow-ups planned by the 2026-11-06 unsmell run

## Goal
Smells the unsmell run of `build/pnpm-prettier-eslint` found but did not fix.
They sit on lines that branch did not change, or the fix is larger than that
branch. Each entry is independent. Split one out into its own task when you
start it.

## Entries
- **Notification tests check a copy of the app.** Three tests in
  `web/tests/notifications.spec.ts` rebuild `formatBody` and
  `showRequestNotification` inside `page.evaluate` and assert on that copy, so
  `web/src/lib/notifications.ts` never runs. The copy has drifted: it titles a
  notification "Secret Access Request", while the app uses "Secret Request" or
  "Commit Signing Request". Two options:
  - drive `request_created` through `routeWebSocket`, as
    `screenshots.spec.ts` does, so the app builds the notification;
  - unit-test `formatBody` directly and drop the copies.
- **Stale `TestBackend.process`.** In `web/tests/fixtures/test-utils.mts`,
  `TestBackend.process` keeps the first backend child. After `restart()` it
  points at a dead process, and no spec reads it. Fix: delete the field.
- **Unused `waitFor`.** `test-utils.mts` exports `waitFor`, which has no caller.
  It is a poll loop that Playwright's `expect.poll` already provides. Fix:
  delete it.
- **The test proxy has no receiver.** `createTestProxy` passes `backendPort` to
  both `proxyRequest` and `proxyWebSocket`. `proxyWebSocket` writes the raw
  upgrade headers into a response in two places, under a 6-line comment that
  corrects itself. Fix: a `TestProxy` that holds the port and has the request,
  upgrade and static handlers as methods, plus one
  `writeRawResponse(socket, statusLine, rawHeaders)` helper. Cut the comment to
  the one fact that Node hands over the socket after the handshake.
- **`startTestBackend` is 170 lines.** It mixes named steps with inline detail
  (the config YAML, argv). Its `waitForServer` and `stopProcess` closures
  capture nothing from the call. The `{ version?: string }` options shape is
  spelled three times. Fix: move the two closures to module level, name one
  `BackendOptions` type, and give the YAML block a `writeTestConfig(stateDir)`.
  Then `startTestBackend` reads as its steps.
