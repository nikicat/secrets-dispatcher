---
status: todo
tags: [refactor, unsmell, web]
---
# Follow-ups planned by the 2026-11-07 unsmell run

## Goal
Smells the unsmell run of `refactor/web-test-follow-ups` found but did not fix.
They sit on lines that branch did not change. Each entry is independent. Split
one out into its own task when you start it.

## Entries
- **False file comment.** `web/tests/history.spec.ts:15`,
  `web/tests/approval.spec.ts:9` and `web/tests/auth.spec.ts:11` say "Each test
  starts an isolated backend instance." Each file actually starts one backend
  in `beforeAll` and shares it across its tests. Fix: "The tests in this file
  share one isolated backend instance."
- **Notification tests that make their own context.** Three tests in
  `web/tests/notifications.spec.ts` still call
  `browser.newContext({ permissions: ["notifications"] })` and close the
  context at the end, so a failing `expect` leaks it. Fix: wrap them in a
  `describe` with `test.use({ permissions: ["notifications"] })`, and have them
  take `{ page }`.
- **Spec helpers written twice.** `createGPGSignRequest` and `authenticate`
  are written out the same way in `web/tests/gpg-sign.spec.ts` and
  `web/tests/request-id.spec.ts`. Fix: make them `TestBackend` methods in
  `web/tests/fixtures/test-utils.mts`.
