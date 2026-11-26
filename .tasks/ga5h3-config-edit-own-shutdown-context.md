---
status: todo
tags: [cli]
---
# config edit makes its own shutdown context

## Goal
configEditCmd.Run (main.go, around line 361) creates its own
shutdownContext(), though run() already passes every Run a ctx.

## Next
Take the ctx that run() provides. That sets the Ctrl-C handler up before the
editor starts, so check that an editor in the foreground still gets Ctrl-C.

Found by the 2026-11-26 unsmell run.
