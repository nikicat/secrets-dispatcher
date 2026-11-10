---
status: todo
tags: [bug, approval]
---
# An approval still pending when its bus stops can be cached for the bus's successor

## Goal
`approval.Manager.ReleaseClient` drops a client's cached approvals when its bus stops, so a new bus that takes the same client name starts with none. A request still pending at that moment can be approved afterwards, and `cacheApproval` then writes an entry under the old bus's name, which the new bus's same-numbered sender (`:1.5`) could match. Found by the /unsmell run of 2026-11-10 (after r2-1).

## Next
Either cancel a client's pending requests in `ReleaseClient`, or tag each claim with a generation that cache entries carry, so an entry from an earlier claim never matches.
