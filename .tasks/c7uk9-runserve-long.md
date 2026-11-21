---
status: todo
tags: [unsmell]
---
# serveCmd.Run is long

## Goal
serveCmd.Run (`main.go`) is about 160 lines mixing logger setup, the ServeSettings to approval.ManagerConfig mapping, notifier wiring and API server startup. The downstream runners moved to `downstreams` (`downstreams.go`).

Found by the /unsmell run of 2026-11-10 (b3-10).

## Next
- Move the ServeSettings to approval.ManagerConfig mapping (main.go:540-548) into `func (s ServeSettings) ManagerConfig() approval.ManagerConfig` in `internal/config`, so serveCmd.Run calls `approval.NewManager(settings.Serve.ManagerConfig())`. Three values have a different name on each side: Rules/TrustRules, HistoryLimit/HistoryMax and IgnoreChromeDummySecret/IgnoreChromeDummy; the method is the one place where the pairs meet.
- Extract the logger setup (shared with daemonCmd.Run). The session-bus reconnect loop is Tkf361.
- The runner fan-out (wg.Add / go func / defer wg.Done, with the idx and runFn loop-variable copies) can become `wg.Go` (sync.WaitGroup.Go).
