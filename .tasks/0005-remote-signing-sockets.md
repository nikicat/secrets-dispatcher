---
status: doing
tags: [gpg, remote]
---
# Remote git signing through per-remote sockets

## Goal
Implement ADR 0011: `serve.remotes` listeners that accept only gpg-sign
requests, identified by the socket they arrive on, and a thin-client mode
selected by `SECRETS_DISPATCHER_REMOTE_SOCKET`. Done when a commit in a
container on another host, signed through a forwarded remote socket, shows up
for approval as `remote:<name>` and gets a valid signature, with no gpg-agent
forwarded there.

## Decisions
- No token on remote sockets: the socket is the identity. Forwarding the API
  socket with the master token was ruled out because it lets the remote approve
  its own requests (ADR 0006).
- The remote endpoint is synchronous (submit and wait for the result), so the
  remote never needs the WebSocket, which broadcasts every request.

## Next
Branch off `docs/adrs-and-tasks`; add the remote listener in `internal/api`,
the config key, and the client mode in `internal/gpgsign`.
