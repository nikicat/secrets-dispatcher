---
status: todo
tags: [test]
---
# Tests wait on the wall clock instead of synctest

## Goal
71 `time.Sleep` calls in Go tests wait for goroutines or timers on the real
clock: poll loops, "give the goroutine 50 ms" pauses, and expiry waits. They
make the suite slow and can flake under load. Go 1.27's `testing/synctest`
runs such a test in a bubble with a fake clock, where `synctest.Wait` waits
until every goroutine in the bubble is blocked. Done when no Go test sleeps on
the real clock to wait for work in the same process.

## Files (2026-11-19)
- `internal/approval/manager_test.go` (31), `internal/notification/desktop_test.go` (11), `proxy_test.go` (10)
- `internal/daemon/daemon_test.go` (4), `internal/proxy/slow_upstream_test.go` (3), `internal/approval/autoapprove_test.go` (3)
- `internal/sshagent/proxy_test.go`, `internal/proxy/tracker_test.go`, `internal/approval/gpgsign_test.go` (2 each)
- `internal/notification/desktop_bus_test.go`, `internal/api/handlers_test.go`, `internal/api/gpgsign_test.go` (1 each)

List them again with `git grep -c time.Sleep -- '*_test.go'`.

## Next
One package per change. A test that talks to a real D-Bus daemon, a socket or
a subprocess cannot run in a bubble (its I/O is outside it); keep those on the
real clock and say why in the test. The poll loops planned in T6qbjt
(`waitPendingID`) and T39k8m become `synctest.Wait` calls here.
