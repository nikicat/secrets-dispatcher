---
status: todo
tags: [test]
---
# Test senders with and without an exe are written in three packages

## Goal
Done when each item below is fixed as its "Fix" says.

- `internal/api/websocket_test.go:209-213`,
  `internal/api/resolver_test.go:14-17` (duplication). The changeset writes "a
  local sender whose invoker has an exe" four times: SenderInfo{PID 4242,
  ProcessChain [{name, 4242, /usr/bin/name}]}. The four places are
  ghSecretRequest (websocket_test.go:209-213), `local` in
  resolver_test.go:14-17, testSender in approval/autoapprove_test.go, and
  localSender in notification/desktop_test.go. Each copy encodes the
  AutoApprovable rule, so a copy goes wrong when that rule changes.
  Fix: One exported fixture for every package, for example
  approval.LocalSenderForTest(comm, exe string) SenderInfo in a non-test file
  of approval. testutil cannot hold it, because approval's in-package tests
  would then import a package that imports approval. The four sites call it.
- `internal/approval/autoapprove_test.go:15-23`,
  `internal/approval/autoapprove_test.go:40-44`,
  `internal/api/resolver_test.go:14-17`, `internal/api/resolver_test.go:48`,
  `internal/api/websocket_test.go:209-213`,
  `internal/api/gpgsign_test.go:383-387`,
  `internal/notification/desktop_test.go:182-186`,
  `internal/notification/desktop_test.go:199`,
  `internal/notification/desktop_test.go:1035-1038` (duplication). The fixture
  pair that AutoApprovable splits on is written in three packages. "A local
  sender with an exe" (PID 4242, ProcessChain [{name, 4242, /usr/bin/name}])
  is testSender in approval, `local` in resolver_test.go and ghSecretRequest's
  sender in websocket_test.go (both added by the changeset), localSender in
  desktop_test.go (added), localSender in api/handlers_test.go:62, and
  exeSender in gpgsign_test.go:383-387
  (unchanged). "A remote request" is Client "remote:laptop" plus a sender
  without a chain, in autoapprove_test.go:40-44, resolver_test.go:48,
  desktop_test.go:199 and :1035-1038; the client value is inert, since
  AutoApprovable reads only the sender (b2-6).
  Fix: As b3-7: one exported test fixture in approval (a non-test file, since
  testutil would import approval back), giving a local sender with an exe and
  a sender without one, used by the changed sites; gpgsign_test.go's exeSender
  joins it if the edit is cheap, else it is planned.

- `internal/gpgsign/daemon_test.go` testSignInfo,
  `internal/notification/desktop_test.go` commitSignInfo,
  `internal/approval/manager_test.go` signRequestOf,
  `internal/api/remote_test.go` and `internal/api/handlers_test.go` (the
  approval.NewGPGSignInfo calls) (repeated derivation). Each writes a commit
  object, calls approval.NewGPGSignInfo with a key and ListedFiles, and
  requires no error.
  Fix: One exported commit sign-info fixture in the same non-test file of
  approval as the sender fixture above, used by all five. (2026-11-21 23:24
  unsmell run, rc3-c-13)

Found by the 2026-11-19 unsmell run (b3-7, x-5).
