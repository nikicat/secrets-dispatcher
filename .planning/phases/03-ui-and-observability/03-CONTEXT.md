# Phase 3: UI and Observability - Context

**Gathered:** 2026-02-24
**Status:** Ready for planning

<domain>
## Phase Boundary

Extend all display surfaces — web UI, CLI, and desktop notifications — to present `gpg_sign` request context distinctly from `get_secret` requests. The backend already fully supports `gpg_sign` requests (Phase 2); this phase adds the missing display layer so users can understand what they're signing and which session is requesting it.

</domain>

<decisions>
## Implementation Decisions

### Visual distinction (Web UI)
- Purple/violet accent color for gpg_sign cards (contrasts with blue get_secret cards)
- Type badge on ALL card types ("GPG Sign", "Secret", "Search") — explicit, accessible, works without color perception
- Same card shell structure (header, sender, expiry, buttons) with different inner content section for gpg_sign
- History view gets consistent treatment: purple accent and type badge for gpg_sign entries

### Commit context layout
- Commit subject line (first line) shown prominently as headline
- Commit body collapsed behind "Show full message" toggle if present
- Changed files: show count + first 5 filenames visible, expandable to see all
- Key ID and author visible at a glance; committer and parent hash available but not prominent (secondary/tucked away)
- CLI `show` command uses git-like format (mimic `git log` style) rather than generic key-value pairs

### Session identity
- PID + repo name combo identifies the requesting session
- Positioned at top of card, next to the type badge — first thing scanned
- Applied to ALL card types for consistent visual scanning (not just gpg_sign)
- No sidebar changes — card identity line is sufficient for disambiguation

### Notification content
- gpg_sign notification title: "Commit Signing Request"
- get_secret notification title: renamed from "Secret Access Request" to "Secret Request" (shorter, parallel naming)
- gpg_sign body: repo name + commit subject line only (minimal — web UI for full details)
- Different freedesktop icon for gpg_sign notifications (signing/key-themed, distinct from dialog-password)

### Claude's Discretion
- Exact purple shade that fits the dark theme palette
- Specific freedesktop icon name for signing notifications
- Badge styling details (pill shape, font size, etc.)
- Exact toggle/expand interaction patterns for commit body and file list
- CLI `list` column layout adjustments for gpg_sign type
- How secondary metadata (committer, parent hash) is accessed in the web UI

</decisions>

<specifics>
## Specific Ideas

- CLI `show` for gpg_sign should feel like `git log` output — familiar to git users
- Type badges on all cards, not just gpg_sign — accessibility and explicit identification
- Session identity (PID + repo) at top of ALL cards, not just gpg_sign — consistent scanning pattern
- "Secret Access Request" → "Secret Request" for notification naming parity

</specifics>

<deferred>
## Deferred Ideas

None — discussion stayed within phase scope

</deferred>

---

*Phase: 03-ui-and-observability*
*Context gathered: 2026-02-24*
