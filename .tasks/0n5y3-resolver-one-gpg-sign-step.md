---
status: todo
tags: [refactor, gpgsign]
---
# One gpg-sign step in the resolver

## Goal
`internal/api/resolver.go` runs gpg for an approved gpg_sign request in two
places, `ApproveAndAutoApprove` and `approveGPGSign`. Both look up gpg,
approve with `approval.GPGNotFound` when it is missing, run it through
`runGPGWithNotify`, log a failure and call `Manager.ApproveGPGSign`. The only
difference is that `ApproveAndAutoApprove` also adds the auto-approve rule
when signing succeeds. Its early return on failure, followed by a second
`ApproveGPGSign` call, is left over from the time when approving a failure and
approving a signature were two separate methods.

Done when one helper, such as `func (r *Resolver) signGPG(req *approval.Request) approval.GPGResult`,
finds and runs gpg and turns a failure into the result's `ExitCode`
(`GPGNotFound`, or `GPGNotStarted` when `RunGPG` errors). `approveGPGSign`
then becomes one `ApproveGPGSign` call. `ApproveAndAutoApprove` approves once,
then adds the rule only when the exit code is zero.

## Related
A `GPGResult.Succeeded()` method could replace the `ExitCode == 0` checks at
the same time (re-check finding r3-2, scored under the bar).

Found by the 2026-11-08 unsmell run (finding r3-1).
