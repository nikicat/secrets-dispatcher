---
status: todo
tags: [service, gpgsign]
---
# The dispatcher's own resolved path is found twice

## Goal
`os.Executable` followed by `filepath.EvalSymlinks`, with the same two error
wraps, is written in internal/service/plan.go (resolveInstallInputs) and in
internal/gpgsign/setup.go. Both want the dispatcher binary's real path for a
unit's ExecStart.

## Next
One function returns that path, and both sites call it. Pick the package that
owns "the dispatcher's own binary"; it may need a small new package that both
can import.

Found by the 2026-11-25 unsmell run.
