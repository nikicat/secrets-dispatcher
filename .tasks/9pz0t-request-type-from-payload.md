---
status: todo
tags: [refactor, approval]
---
# A request's Type and Payload can disagree

## Goal
`approval.RequestInfo` and `Request` carry both `Type` and a sealed `Payload`
(`SearchPayload`, `*GPGSignInfo`). A mismatched pair can still be written; the
`GPGSign()` and `SearchAttributes()` accessors then read it as "no payload".
Done when the type is derived from the payload (each payload kind reports its
`requestType()`, and the kinds without data get an empty payload), so a
mismatch cannot be built.

The same pairing reaches the wire records of a request: api.RequestRecord
(`internal/api/types.go`, with its payload() method, whose doc says "or nil
for a type that carries none"), cli.RequestRecord (`internal/cli/client.go`)
and the TS RequestRecord (`web/src/lib/types.ts`) each carry `type` beside
`search_attributes` (search only) and `gpg_sign_info` (gpg_sign only). Once
Type comes from the payload, api.RequestRecord holds the approval.Payload and
writes search_attributes or gpg_sign_info from its kind in MarshalJSON, as
WSMessage does for its payload, and payload() goes. cli and web follow, by
hand or through tkncw's generated types. (2026-11-21 23:24 unsmell run, s1-6,
s1-7)

## State
Left open by the nk04r refactor because it changes every `Type:` literal:
about 7 in internal/proxy, 1 in internal/sshagent and about 140 in tests.
