---
status: todo
tags: [unsmell]
---
# Doc comments open with their declaration's name

## Goal
Docs opening with their declaration's name (setFlags).

Where: internal/config/config.go:70, internal/config/config.go:252, internal/config/config.go:256.

44 docs in internal/approval/manager.go open with their own names (notify, addHistory, Approve, Deny, …); no linter here requires the Go form. Doc lines unchanged.

Where: internal/approval/manager.go:296, internal/approval/manager.go:485, internal/approval/manager.go:532.

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

Ten docs in internal/cli open with their own names: Client, NewClient,
ItemInfo, PendingResponse, HistoryResponse, ErrorResponse and History in
client.go, and Formatter, NewFormatter and FormatHistory in format.go. Eight
in internal/daemon (Config and its BusAddress and Version fields, Run,
Dispatcher, NewDispatcher, Ping and GetVersion) and persistentNotifier in
internal/notification/slow_upstream.go do the same (2026-11-21 23:24 run,
b4-24, rc2-c-13).

Where: internal/cli/client.go:19, internal/cli/format.go:16,
internal/daemon/daemon.go:13, internal/daemon/dispatcher.go:19,
internal/notification/slow_upstream.go:12.

## Next
- Drop the name and start at the verb or noun phrase.
- Each doc drops the name and starts at the verb or noun phrase; tautologies such as 'NewService creates a new Service handler.' are deleted.
