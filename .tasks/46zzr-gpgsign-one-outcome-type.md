---
status: todo
tags: [refactor, gpgsign]
---
# One outcome type for both thin-client signing paths

## Goal
`DaemonClient.WaitForResolution` (local path) returns a loose tuple
`(signature, gpgStatus, exitCode, denied, err)`, while `SignRemote` returns
`RemoteSignResponse`. The "denied by user" and "timed out" messages and their
exit codes are spelled in both `runLocal` and `runRemote`
(`internal/gpgsign/run.go`). `Run`, `runLocal`, `runRemote` and
`GPGOutput.Emit` return a bare `int` that means an exit status.

`WaitForResolution` also never uses its `DaemonClient` receiver (tests call it
on a zero `&gpgsign.DaemonClient{}`); make it a free function or a method of a
wrapped connection while changing its return. Its doc also leaves out the
cancelled outcome.

Done when `WaitForResolution` returns the same outcome type as `SignRemote`, one
function maps an outcome to its message and exit status, and the exit status is
a named type.

## Related
T2nykk also rewrites `runLocal`; doing this one first leaves it smaller.

Found by the 2026-11-05, 2026-11-07 and 2026-11-08 unsmell runs.
