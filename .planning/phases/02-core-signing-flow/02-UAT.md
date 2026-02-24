---
status: complete
phase: 02-core-signing-flow
source: 02-01-SUMMARY.md, 02-02-SUMMARY.md, 02-03-SUMMARY.md, 02-04-SUMMARY.md
started: 2026-02-24T14:00:00Z
updated: 2026-02-24T17:50:00Z
---

## Current Test

[testing complete]

## Tests

### 1. Build and test suite
expected: `go build ./...` succeeds with no errors. `go test ./...` — all packages pass (no failures).
result: pass

### 2. Setup command creates wrapper and configures git
expected: Running `secrets-dispatcher gpg-sign setup` creates `~/.local/bin/secrets-dispatcher-gpg` shell wrapper and sets `git config --global gpg.program` to point at it. Running `secrets-dispatcher gpg-sign setup --local` sets the config at repo level instead. The wrapper script contains `exec <path-to-binary> gpg-sign "$@"`. Setup does NOT enable `commit.gpgsign=true`.
result: pass

### 3. End-to-end signing: approve
expected: With the daemon running and `gpg.program` configured, `git commit -S` shows a signing request in the daemon's web UI. Approving the request causes the commit to complete with a valid GPG signature. `git verify-commit HEAD` succeeds.
result: pass

### 4. Denial flow
expected: With the daemon running, `git commit -S` shows a signing request. Denying the request causes `git commit` to exit non-zero with stderr message `secrets-dispatcher: signing request denied by user`. No commit is created.
result: pass

### 5. Daemon unreachable
expected: With the daemon NOT running, `git commit -S` (with `gpg.program` configured) exits immediately with exit code 2 and a clear stderr message like `secrets-dispatcher: daemon unreachable at /run/user/.../api.sock. Is secrets-dispatcher running?`. It does NOT fall back to calling real gpg.
result: pass

### 6. GPG error propagation
expected: If the GPG key referenced by `-u <keyID>` doesn't exist in the user's keyring, the daemon calls real gpg which fails, and the error propagates back — `git commit` exits non-zero. The exit code is the gpg process's exit code (not 1 or 2).
result: pass

## Summary

total: 6
passed: 6
issues: 0
pending: 0
skipped: 0

## Gaps

[none]
