---
status: todo
tags: [refactor, notification]
---
# The notification Handler keeps dead state and two inverse maps by hand

## Goal
Done when each item below is fixed as its "Fix" says.

- `internal/notification/desktop.go:366-374`,
  `internal/notification/desktop.go:394`,
  `internal/notification/desktop.go:570-583` (speculative generality).
  Handler.cancelledRequests is written by handleCancelled and swept for expiry
  there, but nothing reads it (grep finds no reader, at HEAD either); the
  "auto_approve" action goes through approver.AutoApprove(reqID). Its comment
  "for auto-approve lookup" is false, and the 5-minute lifetime is an unnamed
  literal stated again in the comment. The changeset placed its new
  AutoApprovable guard (:565-568) right above this dead store.
  Fix: delete cancelledRequests, cancelledEntry, its init and the
  store-and-sweep block
- `internal/notification/desktop.go:376-382` (parameter bloat). NewHandler
  takes six parameters, among them a bool (showPIDs) and two adjacent
  durations (autoApproveDuration, notificationDelay) that transpose silently;
  its doc reads the parameters back.
  Fix: a HandlerConfig struct for baseURL, showPIDs and the two durations; the
  doc goes
- `internal/notification/desktop.go:702-744` (duplication). formatBody nests
  switch-in-if-in-switch, and the item label / item count / search attributes
  switch is written twice (:705-718 and :730-743), differing only in the <b>
  or <i> tag.
  Fix: A `requestSubject(req) string` returning the escaped label, count or
  attributes, wrapped in <b> or <i> by each branch.
- `internal/notification/desktop.go:415-425`,
  `internal/notification/desktop.go:536-539`,
  `internal/notification/desktop.go:551-563`,
  `internal/notification/desktop.go:600-603`,
  `internal/notification/desktop.go:613-628` (inline generic machinery).
  Handler keeps notifications and requests as two maps kept inverse by hand:
  tracking is written twice and forgetting three times, and handleCancelled's
  forget-and-close block repeats handleResolved's.
  Fix: A small bimap type (track, forgetRequest, forgetNotification) owning
  both maps; handleCancelled calls a shared closeFor(requestID).
- `internal/notification/desktop.go:478` (misplaced rule). notificationMeta is
  a Handler method that never uses its receiver
  Fix: a free function (or a table keyed by approval.RequestType)
- `internal/notification/desktop.go:640-642` (pass-through). escapeMarkup only
  calls markupEscaper.Replace, and its one caller aliases it again as esc
  (:661).
  Fix: formatBody uses markupEscaper.Replace as esc; escapeMarkup goes

Found by the 2026-11-19 unsmell run (b2-8, b2-16, b2-17, b2-18, b2-19, b2-20).
