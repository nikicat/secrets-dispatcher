---
status: todo
tags: [unsmell, proxy]
---
# Stopping a forwarded proxy waits on upstream calls with no deadline

## Goal
`proxy.Manager.stopProxy` and `stopAllProxies` wait until a proxy has closed, so its client name is free before another bus can take it. `Proxy.Close` makes D-Bus calls to the upstream with no timeout: `SessionManager.CloseAll` closes each session (session.go:186-189) and the prompter releases its bus name (prompter.go:192). If the upstream stalls, the manager's event loop stops handling socket events and shutdown hangs at `wg.Wait` in main.go. Found by the /unsmell run of 2026-11-10 (r3-2).

## Next
Options: (a) free the name, drop the entry and close `done` before `Close`, which lengthens the in-flight caching window of `v3k8d`; (b) a timeout on the waits; (c) a deadline on `Close`'s upstream calls, which keeps the name held until the bus has closed and also bounds the session_bus and socket: runners. (c) is the recommended one.
