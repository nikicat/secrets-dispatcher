---
status: todo
tags: [unsmell]
---
# Service, CollectionHandler and ItemHandler repeat the trust-rule check

## Goal
The trust-rule deny-or-passthrough block (CheckTrustRules, deny check, RecordDenied, ErrAccessDenied, RecordPassthrough) is written three times, and the changeset had to add `clientName` to each copy identically. Its root is that Service, CollectionHandler and ItemHandler share nine fields and four identical helper methods (upstream, senderResolver, upstreamWithContext, upstreamGetProperty) with no common receiver.

Where: internal/proxy/service.go:95-101, internal/proxy/service.go:201-207, internal/proxy/collection.go:189-195.

Found by the /unsmell run of 2026-11-10 (b2-8).

## Next
- An embedded `handler` struct holding the nine shared fields and the four helpers, built once in ConnectWith; it gets `passthrough(sender senderInfo, infos, reqType, attrs) *dbus.Error` that runs the rule check and records the outcome, called from the three sites.
