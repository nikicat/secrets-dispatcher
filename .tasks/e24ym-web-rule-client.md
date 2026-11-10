---
status: todo
tags: [unsmell]
---
# The web UI does not show a rule's client

## Goal
TrustRule now serializes `client` over the WebSocket snapshot (internal/api/websocket.go:323), but the web UI's TrustRule type (web/src/lib/types.ts:133) has no client and App.svelte renders a rule's scope from request_types, process and secret only, so a rule scoped to one tunnel is shown as if it applied to every caller.

Where: internal/approval/types.go:22.

Found by the /unsmell run of 2026-11-10 (b1-10).

## Next
- Add `client?: string` to the TS TrustRule and show it in the rules list beside the process.
