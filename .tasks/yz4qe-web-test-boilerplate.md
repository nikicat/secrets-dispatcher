---
status: todo
tags: [test, web]
---
# Playwright tests repeat their login and timeouts

## Goal
Done when each item below is fixed as its "Fix" says.

- `web/tests/auto-approve.spec.ts:358`, `web/tests/auto-approve.spec.ts:85`,
  `web/tests/auto-approve.spec.ts:146` (split protocol). `timeout: 10000` on
  the first expect after page load is written 9 times in this file (the
  changeset added one at line 358) and 27 times in web/tests. It is one
  setting, the time the first snapshot may take, with no name.
  Fix: Set `expect: { timeout: 10_000 }` once in web/playwright.config.ts and
  drop the per-call timeouts that only repeat it
- `web/tests/auto-approve.spec.ts:350-351` (repeated derivation). `const
  loginURL = await backend.generateLoginURL(); await page.goto(loginURL);` is
  written 9 times in this file (the changeset added one) and about 40 times in
  web/tests, while the fixture already has backend.authenticate(page) for the
  same step.
  Fix: Each site calls backend.authenticate(page), or a fixture method that
  only opens the login URL where a test must not wait for "client connected"
- `web/tests/fixtures/test-utils.mts:56-61`,
  `web/tests/fixtures/test-utils.mts:86-91`,
  `web/tests/fixtures/test-utils.mts:214-218`,
  `web/tests/fixtures/test-utils.mts:431-432` (restated code). generateJWT's
  doc is not third person and keeps history ("a drifted copy is how the
  missing-jti breakage slipped past the suite"); buildCommitObject's doc
  restates the daemon's rebinding (WYSIWYS), which line 431 states again;
  writeTestConfig's doc names a Go mechanism (staticProvider) and its callers
  ("many tests expect"). Three of them are over the three-line limit.
  Fix: generateJWT "Returns a login JWT signed with secret, as the backend
  expects."; buildCommitObject keeps one sentence on why tests must pass a
  consistent commit_object, and line 431 points to it; writeTestConfig "Writes
  a config that isolates the backend from the user's own and returns its
  path." with the client-connected fact kept only if a caller relies on it

Found by the 2026-11-19 unsmell run (b4-4, b4-5, b4-13).
