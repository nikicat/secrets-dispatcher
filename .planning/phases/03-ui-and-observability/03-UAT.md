---
status: testing
phase: 03-ui-and-observability
source: 03-01-SUMMARY.md, 03-02-SUMMARY.md, 03-03-SUMMARY.md
started: 2026-02-24T20:30:00Z
updated: 2026-02-24T20:30:00Z
---

## Current Test

number: 1
name: Desktop notification for gpg_sign request
expected: |
  When a gpg_sign request arrives (e.g., via `git commit -S`), a desktop notification fires with:
  - Title: "Commit Signing Request" (not "Secret Access Request")
  - Body contains the repo name and first line of the commit message
  - Uses a different icon than the normal secret notification (emblem-important vs dialog-password)
awaiting: user response

## Tests

### 1. Desktop notification for gpg_sign request
expected: When a gpg_sign request arrives, desktop notification shows title "Commit Signing Request", body with repo name and commit subject line, and uses emblem-important icon
result: [pending]

### 2. Desktop notification title rename for get_secret
expected: When a get_secret request arrives, desktop notification title now says "Secret Request" (not "Secret Access Request")
result: [pending]

### 3. CLI list shows gpg_sign with file count
expected: Running `secrets-dispatcher list` while a gpg_sign request is pending shows a row with TYPE=gpg_sign and SUMMARY column showing file count (e.g., "3 files"). Column header reads "SUMMARY" not "SECRET"
result: [pending]

### 4. CLI show displays gpg_sign in git-log style
expected: Running `secrets-dispatcher show <id>` for a gpg_sign request shows git-log style output: Repo, Author, Key ID on labeled lines, then indented commit subject (and body if multi-line), then "Changed files (N):" with file list. Committer line only appears if different from Author
result: [pending]

### 5. Web UI gpg_sign card visual distinction
expected: A pending gpg_sign request appears as a card with purple/violet left border accent, visually distinct from blue get_secret cards. Card shows: commit subject as headline, author and key ID at a glance, changed files (first 5 visible, expandable if more), and expandable commit body toggle if message has multiple lines
result: [pending]

### 6. Type badges on all card types
expected: ALL pending request cards show a type badge — "GPG Sign" on gpg_sign cards, "Secret" on get_secret cards, "Search" on search cards. Badges appear consistently on every card type
result: [pending]

### 7. Session identity on all cards
expected: ALL cards show session identity at the top next to the type badge. For gpg_sign: PID + repo name. For get_secret/search: PID (+ unit name if available). This lets you distinguish which session is requesting
result: [pending]

### 8. Web UI history entries for gpg_sign
expected: After approving/denying a gpg_sign request, the history section shows it with a purple-accented type badge and commit subject as the summary text
result: [pending]

### 9. Browser notifications use correct titles
expected: Browser notification for gpg_sign shows "Commit Signing Request" title with repo + commit subject. Browser notification for get_secret shows "Secret Request" title
result: [pending]

## Summary

total: 9
passed: 0
issues: 0
pending: 9
skipped: 0

## Gaps

[none yet]
