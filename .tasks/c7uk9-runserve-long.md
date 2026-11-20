---
status: todo
tags: [unsmell]
---
# serveCmd.Run is long

## Goal
serveCmd.Run (`main.go`) is 200 lines mixing logger setup, rule conversion, notifier wiring and API server startup. The downstream runners moved to `downstreams` (`downstreams.go`).

Found by the /unsmell run of 2026-11-10 (b3-10).

## Next
- Extract the rule conversion (b3-6) and the logger setup (shared with daemonCmd.Run). The session-bus reconnect loop is Tkf361.
