---
status: todo
tags: [proxy, dbus, bug]
---
# A proxy whose bus name is still taken gives up for good

## Goal
When a remote's ssh forward drops and comes back, the remote bus can still hold `org.freedesktop.secrets` for the dead connection for a while. `Proxy` then gets reply 2 (`IN_QUEUE`) from `RequestName` (internal/proxy/proxy.go:167-169), closes itself, and `Manager` logs "failed to start proxy" (internal/proxy/manager.go:271) and never tries again. The remote has no Secret Service until someone recreates the socket, so gh there reports its token as invalid. Done when a proxy whose name is queued becomes the owner once the old owner leaves (wait for `NameAcquired`, or retry with backoff), with a test where the name is held by another connection at start.

## Notes
- Seen 2026-11-17 on a dev environment's forward: the drop came at 09:13, the failure at 09:16, and `systemctl --user restart dev-forward@<repo>` fixed it at 13:49.
- `RequestName` is called with `NameFlagReplaceExisting` but without `NameFlagDoNotQueue`, so the connection had already joined the queue when the proxy closed it.
