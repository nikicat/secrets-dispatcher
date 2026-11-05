---
status: todo
tags: [api, logging]
---
# Stop logging an error on every clean shutdown

## Goal
Every daemon stop logs `ERR Unix socket server error error="accept unix
.../api.sock: use of closed network connection"`. A clean stop should log no
error, so a real one stands out in the journal.

## State
`Server.Shutdown` (internal/api/server.go) closes `s.unixListener` itself before
calling `s.httpServer.Shutdown`. The unix socket's `Serve` loop then fails with
`net.ErrClosed` instead of returning `http.ErrServerClosed`, and
`serveInBackground` logs it. v0.4.0 already did this, so it predates
remote signing.

## Next
Drop the explicit `unixListener.Close()`: `http.Server.Shutdown` closes every
listener it serves, and closing a `UnixListener` it created removes the socket
file. Add a test that a shutdown logs nothing at error level.
