---
status: todo
tags: [web, bug, security]
---
# The web UI labels deny rules "Approve"

## Goal
In the web UI's trust-rules list, every rule whose action isn't `ignore` is
labelled **Approve**, so a `deny` rule reads as "Approve". In a security tool,
the list should never show a rule as granting what it refuses. Done when each
rule shows its own action.

## State
`web/src/App.svelte:802` decides the label with
`{#if (rule.action ?? "approve") === "ignore"}Ignore{:else}Approve{/if}`.
The daemon now always sends an action, because `approval.RuleAction` defaults
to `approve`, so the `?? "approve"` fallback is no longer needed.

## Next
Render all three actions, with a distinct style for deny. Add a Playwright case
with one rule of each action through `routeSnapshot`. Then rebuild the embedded
dist.
