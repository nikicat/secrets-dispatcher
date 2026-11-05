---
status: todo
tags: [rules, ui]
---
# Create and update trust rules from a request's actions

## Goal
Turning a request into a durable rule today means the `secrets-rule` agent skill
or hand-editing `config.yaml`; the UI only offers the temporary "approve and
auto-approve" rule (ADR 0004). Add actions on a request, pending or in history,
that create a durable rule from it or adjust the rule that matched it.

## Next
Decide where the rule is written (append to `config.yaml` and restart, or a
separate rules file the daemon reloads) and which matchers to prefill from the
request (`exe`, per ADR 0005).
