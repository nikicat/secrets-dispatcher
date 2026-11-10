---
status: todo
tags: [unsmell, approval]
---
# GPG sign requests carry their client outside SenderInfo

## Goal
Since SenderInfo gained Client, `Request.Client` repeats it, and the gpg_sign paths (`CreateGPGSignRequest`, `AwaitGPGSign`, `RecordAutoApprovedGPGSign`, `api/remote.go`) still take a separate client string and leave `SenderInfo.Client` empty. Nothing leaks today, since gpg_sign never reaches trust rules or the approval cache, but a later change that does would read an empty client. Found by the /unsmell run of 2026-11-10 (r1-2).

## Next
One field: the gpg paths set `SenderInfo.Client` (`ClientLocal` for the local gpg socket, `"remote:"+name` for a remote), and `Request.Client` goes, or derives from SenderInfo. The web UI, CLI and notifications read `request.client`, so keep that JSON field.
