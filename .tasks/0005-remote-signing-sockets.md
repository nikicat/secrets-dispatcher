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

## State
Branch `feat/remote-gpg-sign`: `serve.remotes` listeners owned by `api.Server`
(`internal/api/remote.go`), `approval.Manager.AwaitGPGSign`, the client mode
(`internal/gpgsign/remote.go`), `gpg-sign setup --remote-socket`, and docs
(ADR 0011, REMOTE-PROXY.md). Unit tests drive the real client against the real
server over a socket for every resolution, shutdown under a pending request,
and no other endpoint reachable. Not yet run against a live daemon.

## Next
Live check: run the branch's daemon with a remote configured, forward its socket
into the symbiosis dev container on nb-home (zxc-fleet `clients/symbiosis-dev`),
and sign a commit there; then delete this task.
