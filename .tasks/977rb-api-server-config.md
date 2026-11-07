---
status: todo
tags: [refactor, api]
---
# A config struct for api.Server

## Goal
`api.NewServer` and `NewServerWithProvider` take eight or nine parameters.
Adjacent `remoteSocket, clientName string` and a bare `trimProcessChain bool`
travel together into `NewHandlers*`. Done when one `ServerConfig` struct
carries them, with the client source as the only other argument.

Found by the 2026-11-07 unsmell run.
