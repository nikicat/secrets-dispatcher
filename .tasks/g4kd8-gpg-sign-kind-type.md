---
status: todo
tags: [refactor, gpgsign, approval]
---
# The gpg-sign kind travels as a bare string

## Goal
`internal/gpgsign/run.go` `collectSignInfo` copies the typed `payload.Kind`
into `approval.GPGSignInfo.Kind` as `string(payload.Kind)`, and the
commit/tag/push kind then travels as a string with per-kind fields beside it.
Done when one kind type in `approval` (gpgsign imports approval, not the
reverse) is shared by `GPGSignInfo.Kind` and `ParseSignedPayload`.

## Related
rt113 covers the web types only.

Found by the 2026-11-20 unsmell run (r1c-15).
