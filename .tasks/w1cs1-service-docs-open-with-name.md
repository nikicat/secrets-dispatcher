---
status: todo
tags: [service, docs]
---
# Four service docs open with their own name

## Goal
These docs open with the name of what they document. lookPathFunc's doc also restates its type:
- `allUnitNames` (internal/service/install.go:31)
- `lookPathFunc` (internal/service/install.go:180)
- `ownerWaitTimeout` (internal/service/install.go:216)
- `captureStdout` (internal/service/install_test.go:551)

## Next
Keep only what the signature can't say:
- `allUnitNames`: "Every unit file the package may install."
- `lookPathFunc`: "Replaced in tests."
- `ownerWaitTimeout`: "Bounds the polls that confirm ownership after a start or a restore. Shortened in tests."
- `captureStdout`: "Returns what fn printed to stdout."

Found by the 2026-11-25 unsmell run.
