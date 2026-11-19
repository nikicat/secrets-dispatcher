---
status: todo
tags: [test]
---
# Move the remaining Go tests to testify

## Goal
29 of the 55 Go test files use testify (`assert`/`require`); the rest
hand-write `if got != want { t.Errorf(...) }` checks, and some files mix both.
Done when every Go test file checks with testify: `require` where a failure
makes the rest of the test meaningless, `assert` otherwise.

## Files left (2026-11-19)
Per package, test files with no testify import:

- `internal/config` (5), `internal/proxy` (5)
- `internal/approval`, `internal/cli`, `internal/companion`, `internal/gpgsign` (2 each)
- `internal/api`, `internal/daemon`, `internal/dhcrypto`, `internal/notification`,
  `internal/procutil`, `internal/randhex`, `internal/sshagent` (1 each)
- `proxy_test.go` at the root

List them again with:

```sh
comm -23 <(git ls-files '*_test.go' | sort) \
         <(git grep -l stretchr/testify -- '*_test.go' | sort)
```

## Next
One package per change, so each diff stays reviewable; a mechanical rewrite
keeps each test's assertions and failure meaning the same.
