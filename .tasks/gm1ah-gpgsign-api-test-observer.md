---
status: todo
tags: [tests, api, gpgsign]
---
# Test gpg-sign signatures over the real WebSocket connection

## Goal
`TestHandleGPGSignRequest_WSSignatureOnApproval` (`internal/api/gpgsign_test.go`)
asserts on messages that its own `wsEventObserver` collects. The observer no
longer copies the connection's message building or polls: it appends
`endedMessages(entry)` from `RequestEnded`. But the test still reads the
messages from that function, not from a real `wsConnection`, so it does not
see what a WebSocket peer receives. Done when the test reads the signature
over a real connection, as `TestWSHandler_GPGSignClientReadsResolution` does,
and `wsEventObserver` is gone.

Found by the 2026-11-08 unsmell run, and updated by the 2026-11-21 23:24 run
(b1-11).
