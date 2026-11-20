---
status: todo
tags: [refactor, api, web]
---
# ClientInfo.SocketPath holds a bus keyword

## Goal
For the session-bus downstream, `serveCmd.Run` (`main.go`) puts
`unixsock.Path(config.BusSessionBus)` ("session_bus") into
`proxy.ClientInfo.SocketPath`, a path field. It is serialized as `socket_path`, and the web UI shows it and
tells clients apart by it (`App.svelte`). Done when `ClientInfo` carries the
downstream as a typed value (a socket path, or the session bus) and the JSON
renders it, e.g. as a transport plus an optional path.

Found by the 2026-11-19 unsmell run. (b1-5)
