---
status: todo
tags: [tests, api, gpgsign]
---
# Test gpg-sign signatures over the real WebSocket connection

## Goal
`TestHandleGPGSignRequest_WSSignatureOnApproval` (`internal/api/gpgsign_test.go`)
asserts on messages that its own `wsEventObserver` builds, a copy of
`wsConnection.OnEvent` with a placeholder signature, so it only tests its own
constants. `wsEventObserver.WaitForMessages` is also a hand-rolled poll loop.
Done when the test reads the signature from a real `wsConnection`, as
`TestWSHandler_GPGSignClientReadsResolution` does, and the observer is gone.

Found by the 2026-11-08 unsmell run.
