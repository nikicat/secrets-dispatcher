---
status: todo
tags: [proxy, docs]
---
# proxy.go comments restate the code

## Goal
In internal/proxy/proxy.go:
- the field `connection.frontConn` (:29) has a trailing sentence fragment, "clients connect here (session bus or remote socket)";
- in `Proxy.Run`, "Fallback if context is not available" (:269) and "Front connection closed (e.g., SSH tunnel disconnected)" (:278) say what the next line already says.

## Next
Put the field note on its own line above `frontConn` as a full sentence, or delete it. Delete both comments in Run.

Found by the 2026-11-25 unsmell run.
