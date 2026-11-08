---
status: todo
tags: [refactor, service, bug]
---
# service install runs the steps that Plan returns

## Goal
`service.Plan` repeats Install's sequence by hand: mask, demote, stop, write or
remove units, then daemon-reload, enable and start. Its doc says "keep it in
sync with Install". `stopDBusActivatedService` repeats DetectProvider's
owner-PID query, and it signals the owner even when that's the dispatcher,
which Plan doesn't list, so `--dry-run` under-reports a reinstall.

Done when Install executes the steps Plan returns, and
`stopDBusActivatedService(provider)` signals only under Plan's condition.

Found by the 2026-11-07 unsmell run.
