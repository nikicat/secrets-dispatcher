---
status: todo
tags: [unsmell, refactor, approval]
---
# Item object paths are strings

## Goal
`ItemInfo.Path` holds an item's D-Bus object path as a `string`, and so do the
approval cache key, `CacheItemForSender` and `dbus.ExtractCollection`'s input
(r1-2 of unsmell run 20261121-103738, `internal/approval/manager.go:66-70`).
Done when D-Bus items carry a `dbus.ObjectPath`, as `RequestInfo.Session`
already does. The JSON key `path` stays a string.

## Ruled out
- Retyping `ItemInfo.Path` alone: `sshagent/proxy.go` puts a key fingerprint
  in `Path` for ssh_sign requests, and the approval cache keys on it
  (`SenderInfo.cacheKey(item.Path)`), so ssh approvals are cached per key.
  Converting a fingerprint to `dbus.ObjectPath` would be a lie.

## Next
Decide where an ssh key's identity lives: either `ItemInfo` gets a cache
identity separate from `Path` (the fingerprint for ssh, the object path for
D-Bus items) and ssh items leave `Path` empty, or ssh_sign items get their own
type. Either way the web UI's copy-path button and `App.svelte`'s dedupe key
(`items.map(i => i.path)`) must keep working for ssh_sign requests.
