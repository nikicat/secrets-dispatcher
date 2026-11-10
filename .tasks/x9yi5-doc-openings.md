---
status: todo
tags: [unsmell]
---
# Doc comments open with their declaration's name

## Goal
Docs opening with their declaration's name (validate, namedGlob, globs, staticProvider, multiProvider, runConfig, runGPGSignSetup, setFlags); main.go:835 `runService handles…` is orphaned above runTry.

Where: internal/config/config.go:70, internal/config/config.go:226, internal/config/config.go:252, internal/config/config.go:256, main.go:645, main.go:654, main.go:667, main.go:835, main.go:961, main.go:1102.

The docs of the two functions whose signatures the changeset changed open with their own names ("CheckTrustRules checks if…", "matchTrustRule returns true if…"), as do 53 more docs in manager.go and types.go; no linter here requires the Go form. Doc lines unchanged.

Where: internal/approval/manager.go:929, internal/approval/manager.go:942.

70 declaration docs in the six files open with the declaration's own name (Go's 'Name verbs…' form), against the user's style rule; no revive/stylecheck config requires it. None is on a changed comment line.

Where: internal/proxy/senderinfo.go:13, internal/proxy/senderinfo.go:39-40, internal/proxy/proxy.go:56, internal/proxy/manager.go:214, internal/proxy/service.go:27, internal/proxy/collection.go:29.

Found by the /unsmell run of 2026-11-10 (b3-12, b1-11, b2-11).

## Next
- Drop the name and start at the verb or noun phrase; move or delete the orphaned runService line.
- Drop the names: 'Returns the first trust rule that matches the request, or nil.' on CheckTrustRules; 'Checks whether the request matches rule.' on matchTrustRule; the rest of the file in one pass.
- Each doc drops the name and starts at the verb or noun phrase; tautologies such as 'NewService creates a new Service handler.' are deleted.
