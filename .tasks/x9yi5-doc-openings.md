---
status: todo
tags: [unsmell]
---
# Doc comments open with their declaration's name

## Goal
Docs opening with their declaration's name (validate, namedGlob, globs, setFlags).

Where: internal/config/config.go:70, internal/config/config.go:226, internal/config/config.go:252, internal/config/config.go:256.

The docs of the two functions whose signatures the changeset changed open with their own names ("CheckTrustRules checks if…", "matchTrustRule returns true if…"), as do 53 more docs in manager.go and types.go; no linter here requires the Go form. Doc lines unchanged.

Where: internal/approval/manager.go:929, internal/approval/manager.go:942.

70 declaration docs in the six files open with the declaration's own name (Go's 'Name verbs…' form), against the user's style rule; no revive/stylecheck config requires it. None is on a changed comment line.

Where: internal/proxy/senderinfo.go:13, internal/proxy/senderinfo.go:39-40, internal/proxy/proxy.go:56, internal/proxy/manager.go:214, internal/proxy/service.go:27, internal/proxy/collection.go:29.

38 declaration docs in internal/notification/desktop.go and desktop_test.go
open with their own name (Notifier, Approver, Action, Closed, DBusNotifier, …).

Where: internal/notification/desktop.go:25, internal/notification/desktop.go:366, internal/notification/desktop_test.go:19.

Found by the /unsmell run of 2026-11-10 (b3-12, b1-11, b2-11), and the
2026-11-19 run (b2-15).

19 docs in internal/companion (check.go, provision.go) and two in
internal/gpgsign/run.go (resolveRepoName, collectChangedFiles) open with their
names; Provision's doc lists its steps (2026-11-20 run, r1c-14).

Where: internal/companion/check.go:15, internal/gpgsign/run.go.

## Next
- Drop the name and start at the verb or noun phrase.
- Drop the names: 'Returns the first trust rule that matches the request, or nil.' on CheckTrustRules; 'Checks whether the request matches rule.' on matchTrustRule; the rest of the file in one pass.
- Each doc drops the name and starts at the verb or noun phrase; tautologies such as 'NewService creates a new Service handler.' are deleted.
