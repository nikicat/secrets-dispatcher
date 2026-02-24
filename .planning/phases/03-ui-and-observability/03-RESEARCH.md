# Phase 3: UI and Observability - Research

**Researched:** 2026-02-24
**Domain:** Svelte 5, Go CLI formatting, freedesktop desktop notifications
**Confidence:** HIGH

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Visual distinction (Web UI)**
- Purple/violet accent color for gpg_sign cards (contrasts with blue get_secret cards)
- Type badge on ALL card types ("GPG Sign", "Secret", "Search") — explicit, accessible, works without color perception
- Same card shell structure (header, sender, expiry, buttons) with different inner content section for gpg_sign
- History view gets consistent treatment: purple accent and type badge for gpg_sign entries

**Commit context layout**
- Commit subject line (first line) shown prominently as headline
- Commit body collapsed behind "Show full message" toggle if present
- Changed files: show count + first 5 filenames visible, expandable to see all
- Key ID and author visible at a glance; committer and parent hash available but not prominent (secondary/tucked away)
- CLI `show` command uses git-like format (mimic `git log` style) rather than generic key-value pairs

**Session identity**
- PID + repo name combo identifies the requesting session
- Positioned at top of card, next to the type badge — first thing scanned
- Applied to ALL card types for consistent visual scanning (not just gpg_sign)
- No sidebar changes — card identity line is sufficient for disambiguation

**Notification content**
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

### Deferred Ideas (OUT OF SCOPE)

None — discussion stayed within phase scope
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| DISP-01 | Desktop notification fires when a signing request arrives with commit summary | Notification handler already dispatches per `OnEvent`; need type-switch branch for `gpg_sign` with different icon and title |
| DISP-02 | Web UI displays signing request context (repo, message, author, files, key ID) | `GPGSignInfo` already in `PendingRequest` JSON; `RequestCard.svelte` needs a new `{#if request.type === "gpg_sign"}` branch |
| DISP-03 | Web UI visually distinguishes `gpg_sign` from `get_secret` (different color/icon/label) | CSS variable `--color-gpg-sign` + type badge component; card border/left-accent via CSS conditional class |
| DISP-04 | CLI `list` and `show` display `gpg_sign` type with commit context | `FormatRequests` needs a summary column for gpg_sign; `formatRequest` needs a gpg_sign branch; `PendingRequest` in `cli` package needs `GPGSignInfo` field |
| DISP-05 | Session/client identity shown prominently for parallel session disambiguation | PID + repo name line at top of card (and history entry) for ALL request types; already exists as `sender_info` in the data |
| DISP-06 | File count summary shown in list view for quick scanning | `secretSummary()` equivalent for gpg_sign: `"N files"` or `"1 file"` pulled from `GPGSignInfo.ChangedFiles` |
</phase_requirements>

## Summary

Phase 3 is a pure display layer addition. The backend already produces `GPGSignInfo` on every `gpg_sign` request — the data is fully available in the JSON API responses that all three display surfaces consume. There are no new API endpoints, no new backend types, and no new data flows to implement. Every change in this phase is an extension of an existing branch (`switch req.Type`) or an additional `{#if}` block in Svelte templates.

The three display surfaces are independent of each other: Go `notification` package (`desktop.go`), Go `cli` package (`format.go` + `client.go`), and the Svelte 5 web UI (`RequestCard.svelte`, `App.svelte`, `types.ts`). Each can be implemented and tested in isolation. The Svelte frontend is built with Deno + Vite and embedded into the Go binary at build time via `//go:embed`.

The codebase uses Svelte 5's runes API (`$state`, `$props`, `$effect`) — not the legacy Svelte 4 API. All new Svelte code must follow the runes pattern already established in `RequestCard.svelte` and `App.svelte`.

**Primary recommendation:** Implement in three discrete tasks — (1) Go notification handler, (2) CLI formatter, (3) Svelte web UI — in that order, since the Go tests can be run in isolation without a browser and give fast feedback.

## Standard Stack

### Core (already in use, confirmed from codebase)

| Component | Version | Purpose | Notes |
|-----------|---------|---------|-------|
| Svelte | ^5.0.0 | Web UI reactive framework | Runes API: `$state`, `$props`, `$effect` |
| Vite | ^6.0.0 | Build tool for frontend | `deno task build` produces dist/ |
| TypeScript | (bundled) | Type safety in frontend | All `.ts` files, strict mode |
| Go | 1.25.6 | Backend + CLI | Standard library only for new code |
| godbus/dbus/v5 | v5.2.2 | D-Bus for desktop notifications | Already wired in `notification/desktop.go` |

### No New Dependencies Needed

All display work uses existing code paths. No new Go modules or npm packages are required.

## Architecture Patterns

### Existing Project Structure (relevant files)

```
internal/
├── notification/
│   └── desktop.go          # Add gpg_sign case to handleCreated() + formatBody()
├── cli/
│   ├── client.go           # Add GPGSignInfo field to PendingRequest struct
│   └── format.go           # Add gpg_sign branches to FormatRequests() + formatRequest()
└── approval/
    └── gpgsign.go          # GPGSignInfo struct — READ ONLY, no changes needed

web/src/
├── lib/
│   ├── types.ts            # Add GPGSignInfo interface + update PendingRequest union type
│   ├── RequestCard.svelte  # Add gpg_sign content section + type badge + session identity
│   └── notifications.ts    # Add gpg_sign case to showRequestNotification() + formatBody()
├── App.svelte              # Update history entries: type badge + session identity for all types
└── app.css                 # Add --color-gpg-sign CSS variable
```

### Pattern 1: Go notification handler — type switch

The existing `handleCreated()` hard-codes `"Secret Access Request"` and `formatBody()` switches on `req.Type`. The change is minimal: rename the get_secret title, add a gpg_sign branch with a different icon and title:

```go
// Source: internal/notification/desktop.go — existing handleCreated pattern
func (h *Handler) handleCreated(req *approval.Request) {
    summary, icon := h.notificationMeta(req)
    body := h.formatBody(req)
    // call notifier.Notify with icon parameter (requires Notify signature change)
}

func (h *Handler) notificationMeta(req *approval.Request) (summary, icon string) {
    switch req.Type {
    case approval.RequestTypeGPGSign:
        return "Commit Signing Request", "document-send" // or "emblem-important"
    default:
        return "Secret Request", "dialog-password"
    }
}
```

**Notify signature change:** The current `Notifier.Notify(summary, body string)` does not accept an icon parameter — the icon is hard-coded as `"dialog-password"` inside `DBusNotifier.Notify`. To support per-type icons, either:
- (a) Add `icon string` parameter to the `Notifier` interface and `DBusNotifier.Notify`, OR
- (b) Add a separate `NotifyWithIcon(summary, body, icon string)` method

Option (a) is cleaner — there are only two callers (production and mock) and the mock will need updating too. This is the recommended approach.

### Pattern 2: CLI formatter — gpg_sign branches

The `cli.PendingRequest` struct in `client.go` currently lacks `GPGSignInfo`. The API response already includes `gpg_sign_info` — the struct just needs the field added:

```go
// Source: internal/cli/client.go — add to PendingRequest
type GPGSignInfo struct {
    RepoName     string   `json:"repo_name"`
    CommitMsg    string   `json:"commit_msg"`
    Author       string   `json:"author"`
    ChangedFiles []string `json:"changed_files"`
    KeyID        string   `json:"key_id"`
    Committer    string   `json:"committer"`
    ParentHash   string   `json:"parent_hash,omitempty"`
}

type PendingRequest struct {
    // ... existing fields ...
    GPGSignInfo *GPGSignInfo `json:"gpg_sign_info,omitempty"`
}
```

For `FormatRequests()` (the list view), the "SECRET" column becomes a general "SUMMARY" column. For `gpg_sign` rows it shows `"N files"` from `len(GPGSignInfo.ChangedFiles)`. For `formatRequest()` (the show command), a gpg_sign branch renders git-log style output:

```go
// Proposed git-log style for `show` on a gpg_sign request
func (f *Formatter) formatGPGSignRequest(req *PendingRequest) {
    info := req.GPGSignInfo
    fmt.Fprintf(f.w, "commit   (pending signature)\n")
    fmt.Fprintf(f.w, "Author:  %s\n", info.Author)
    fmt.Fprintf(f.w, "Repo:    %s\n", info.RepoName)
    fmt.Fprintf(f.w, "Key:     %s\n", info.KeyID)
    fmt.Fprintf(f.w, "\n    %s\n\n", commitSubject(info.CommitMsg))
    // Changed files list
    fmt.Fprintf(f.w, "Changed files (%d):\n", len(info.ChangedFiles))
    for _, f := range info.ChangedFiles {
        fmt.Fprintf(f.w, "  %s\n", f)
    }
    // Secondary metadata tucked at end
    if info.Committer != "" {
        fmt.Fprintf(f.w, "\nCommitter: %s\n", info.Committer)
    }
    if info.ParentHash != "" {
        fmt.Fprintf(f.w, "Parent:    %s\n", info.ParentHash)
    }
}
```

### Pattern 3: Svelte web UI — RequestCard.svelte restructuring

The card currently has two branches: `{#if request.type === "search"}` and `{:else}` (get_secret). A `gpg_sign` branch must be added as a full `{:else if request.type === "gpg_sign"}` block. The card's shared header structure stays unchanged; only the inner content section changes.

**Type badge** (new, applies to ALL cards):
```svelte
<!-- In card header, alongside existing sender-info -->
<span class="type-badge type-badge--{request.type}">
  {typeBadgeLabel(request.type)}
</span>
```

```ts
function typeBadgeLabel(type: string): string {
  switch (type) {
    case "gpg_sign": return "GPG Sign";
    case "search": return "Search";
    default: return "Secret";
  }
}
```

**Session identity line** (new, applies to ALL cards):

The decision is "PID + repo name". For gpg_sign, repo name comes from `request.gpg_sign_info.repo_name`. For other types, only PID is available (no repo concept). The identity line should show what's available:

```svelte
<span class="session-identity">
  {#if request.type === "gpg_sign" && request.gpg_sign_info}
    PID {request.sender_info.pid} · {request.gpg_sign_info.repo_name}
  {:else}
    PID {request.sender_info.pid}
  {/if}
</span>
```

**GPG sign content section:**

```svelte
{:else if request.type === "gpg_sign" && request.gpg_sign_info}
  {@const info = request.gpg_sign_info}
  <div class="gpg-sign-content">
    <p class="commit-subject">{commitSubject(info.commit_msg)}</p>

    {#if commitBody(info.commit_msg)}
      <!-- collapsed toggle -->
      <details class="commit-body-toggle">
        <summary>Show full message</summary>
        <pre class="commit-body">{commitBody(info.commit_msg)}</pre>
      </details>
    {/if}

    <div class="commit-meta">
      <span class="meta-label">Author</span>
      <span class="meta-value">{info.author}</span>
      <span class="meta-label">Key</span>
      <span class="meta-value mono">{info.key_id}</span>
    </div>

    <div class="changed-files">
      <span class="section-label">Changed files ({info.changed_files.length})</span>
      {#each info.changed_files.slice(0, 5) as file}
        <div class="file-path mono">{file}</div>
      {/each}
      {#if info.changed_files.length > 5}
        <details>
          <summary>{info.changed_files.length - 5} more files</summary>
          {#each info.changed_files.slice(5) as file}
            <div class="file-path mono">{file}</div>
          {/each}
        </details>
      {/if}
    </div>

    <!-- Secondary metadata (tucked away) -->
    <details class="secondary-meta">
      <summary>More details</summary>
      {#if info.committer}<div>Committer: {info.committer}</div>{/if}
      {#if info.parent_hash}<div class="mono">Parent: {info.parent_hash}</div>{/if}
    </details>
  </div>
```

**CSS additions in app.css** (one new variable, no existing rules changed):
```css
:root {
  /* existing ... */
  --color-gpg-sign: #7c3aed;          /* violet-700 — fits dark theme */
  --color-gpg-sign-bg: rgba(124, 58, 237, 0.08);
}
```

Card left-accent via class:
```css
.card.card--gpg_sign {
  border-left: 3px solid var(--color-gpg-sign);
}
.type-badge--gpg_sign {
  color: var(--color-gpg-sign);
  background-color: var(--color-gpg-sign-bg);
}
```

**TypeScript type update:**

```ts
// Source: web/src/lib/types.ts
export interface GPGSignInfo {
  repo_name: string;
  commit_msg: string;
  author: string;
  committer: string;
  key_id: string;
  fingerprint?: string;
  changed_files: string[];
  parent_hash?: string;
}

export interface PendingRequest {
  // ... existing fields ...
  type: "get_secret" | "search" | "gpg_sign";  // add "gpg_sign"
  gpg_sign_info?: GPGSignInfo;
}
```

**History view in App.svelte:**

History entries need the same type badge + a content summary for gpg_sign. The `.history-items` span currently renders `entry.request.items.map(...)`. For gpg_sign it should render the commit subject instead:

```svelte
<span class="history-items">
  {historyItemsSummary(entry.request)}
</span>
```
```ts
function historyItemsSummary(req: PendingRequest): string {
  if (req.type === "gpg_sign" && req.gpg_sign_info) {
    return commitSubject(req.gpg_sign_info.commit_msg);
  }
  return req.items.map(i => i.label || i.path).join(", ");
}
```

### Pattern 4: `{:else if}` vs separate component

The user already decided "same card shell, different inner content section." This means extending the existing `RequestCard.svelte` rather than creating a `GPGSignCard.svelte`. The existing pattern of `{#if}` / `{:else if}` / `{:else}` inside one component is exactly what's already used for `search` vs `get_secret`. Follow that pattern — don't create a new component.

### Anti-Patterns to Avoid

- **Creating a separate GPGSignCard component:** The existing RequestCard already handles multiple types in-file. Adding a branch is simpler and consistent. A separate component adds file indirection with no benefit at this scale.
- **Adding icon support via a new Notifier interface method:** Prefer modifying the existing `Notify(summary, body string)` signature to `Notify(summary, body, icon string)` rather than adding a second method. The mock in `desktop_test.go` already captures `summary` and `body` — extend it to capture `icon` too.
- **Using `<details>` for the file expand in Svelte:** `<details>/<summary>` is native HTML, requires zero JS, and fits the scope perfectly. Don't reach for a JS-driven accordion component.
- **Renaming the "SECRET" column to "SUMMARY":** The column can stay "SUMMARY" or "INFO" — pick one name and be consistent in both `FormatRequests` (list) and `FormatHistory`.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Expand/collapse for commit body and file list | Custom JS toggle state | Native `<details>/<summary>` HTML element | Zero JS, browser-native, accessible, already works in all modern browsers |
| Commit subject extraction | Regex/index split | Simple `strings.SplitN(msg, "\n", 2)[0]` in Go and `msg.split('\n')[0]` in TS | Commit format is well-defined: first line is subject |
| Purple color value | Color picker | `#7c3aed` (Tailwind violet-700) or `#8b5cf6` (violet-500) | Both read well on `#1a1a2e` bg; violet-700 is more muted, appropriate for dark theme |

## Common Pitfalls

### Pitfall 1: Notify interface signature mismatch

**What goes wrong:** The `Notifier` interface only has `Notify(summary, body string)`. If you add icon support by passing it directly to the D-Bus call inside `DBusNotifier.Notify` without updating the interface, the mock test won't verify icon dispatch, and the interface becomes inconsistent with implementation.

**Why it happens:** The icon is currently hard-coded inside the concrete struct. Easy to overlook when extending.

**How to avoid:** Update the `Notifier` interface signature first, then update `DBusNotifier`, then update the mock in `desktop_test.go`. All three change together.

**Warning signs:** Compilation succeeds but `mockNotifier` fields don't capture `icon`.

### Pitfall 2: `cli.PendingRequest` missing `GPGSignInfo`

**What goes wrong:** `cli.PendingRequest` in `internal/cli/client.go` is a manually-maintained duplicate of `api.PendingRequest`. It currently lacks the `GPGSignInfo` field. If you add formatting code that references `req.GPGSignInfo` without adding the struct field, it won't compile.

**Why it happens:** The two structs diverged — `api.PendingRequest` has `GPGSignInfo *approval.GPGSignInfo` but `cli.PendingRequest` was not updated when Phase 1 added the field.

**How to avoid:** Add `GPGSignInfo *GPGSignInfo` to `cli.PendingRequest` and define a matching `cli.GPGSignInfo` struct (same JSON tags). This is intentional duplication — the CLI package deliberately doesn't import from `internal/api` or `internal/approval`.

**Warning signs:** `go build ./...` passes but `go vet ./...` or JSON decode produces nil GPGSignInfo on gpg_sign requests.

### Pitfall 3: Type badge affects ALL card types — test get_secret regression

**What goes wrong:** Adding a `type-badge` to the card header for all types means existing get_secret and search cards also get a badge. This is desired per the decision, but it's easy to break the get_secret CSS layout (the header currently has `item-summary` + `sender-info` + `expires` in a flex row).

**Why it happens:** The badge is a new element inserted into the card header, which may push the expiry timer to a second line if not accounted for.

**How to avoid:** Use `flex-wrap: wrap` or ensure the badge doesn't add significant width. Test all three card types (get_secret, search, gpg_sign) after adding the badge.

**Warning signs:** Expiry timer wraps onto a new line on narrow viewport; header height increases unexpectedly.

### Pitfall 4: Svelte 5 `{@const}` inside `{#if}` blocks

**What goes wrong:** Using `{@const info = request.gpg_sign_info}` inside a `{:else if}` block and then using `info!.field` — TypeScript may still see `info` as possibly undefined even after the narrowing check.

**Why it happens:** Svelte 5 templates don't have TypeScript narrowing as precise as `.tsx` files.

**How to avoid:** Use a non-null assertion `info!.field` or declare `{@const info = request.gpg_sign_info!}` inside the already-narrowed branch where we know it's present.

**Warning signs:** `svelte-check` reports "Object is possibly undefined" despite the `{:else if ... && request.gpg_sign_info}` guard.

### Pitfall 5: Notification title rename breaks existing tests

**What goes wrong:** The locked decision renames `"Secret Access Request"` to `"Secret Request"` in the desktop notification (`notification/desktop.go`). The existing test `TestHandler_OnEvent_RequestCreated` asserts `call.summary != "Secret Access Request"`. That test will now fail.

**Why it happens:** The test hard-codes the current title string.

**How to avoid:** Update the test assertion in `desktop_test.go` to `"Secret Request"` at the same time as the production code change. The browser notification `notifications.ts` also uses `"Secret Access Request"` — update both.

**Warning signs:** `go test ./internal/notification/...` fails immediately after changing the title.

### Pitfall 6: D-Bus freedesktop icon names are theme-dependent

**What goes wrong:** Using an icon name that doesn't exist in the user's icon theme produces a silent fallback (empty icon). Common "signing" icon names like `"document-send"`, `"emblem-important"`, `"security-medium"`, `"key"` vary by theme.

**Why it happens:** Freedesktop icon names are standardized in the Icon Naming Specification but not all themes implement all names, and the spec doesn't include a dedicated "signing" category.

**How to avoid:** The three most-likely-present options across themes are:
- `"emblem-important"` — widely available, signals attention
- `"document-send"` — action-oriented, widely available
- `"security-medium"` — available in GNOME/Papirus

The exact choice is in Claude's discretion. Recommend `"emblem-important"` as it has the widest theme coverage and conveys "this requires attention." The choice degrades gracefully — if the icon isn't found, the notification body still carries all needed information.

## Code Examples

### commitSubject helper (Go)

```go
// Source: consistent with git log format, first line = subject
func commitSubject(msg string) string {
    if i := strings.IndexByte(msg, '\n'); i >= 0 {
        return msg[:i]
    }
    return msg
}
```

### commitBody helper (Go, for show command)

```go
func commitBody(msg string) string {
    if i := strings.IndexByte(msg, '\n'); i >= 0 {
        body := strings.TrimLeft(msg[i:], "\n")
        return strings.TrimRight(body, "\n")
    }
    return ""
}
```

### commitSubject helper (TypeScript)

```ts
function commitSubject(msg: string): string {
  return msg.split('\n')[0];
}

function commitBody(msg: string): string {
  const lines = msg.split('\n');
  if (lines.length <= 1) return '';
  // Skip blank separator line after subject
  const body = lines.slice(1).join('\n').replace(/^\n/, '');
  return body.trimEnd();
}
```

### gpg_sign summary for CLI list view

```go
// In secretSummary() (rename to requestSummary() or add a branch):
func requestSummary(req PendingRequest) string {
    if req.GPGSignInfo != nil {
        n := len(req.GPGSignInfo.ChangedFiles)
        if n == 1 {
            return "1 file"
        }
        return fmt.Sprintf("%d files", n)
    }
    // existing get_secret / search logic unchanged
    if len(req.Items) == 0 {
        if len(req.SearchAttributes) > 0 {
            return formatAttrs(req.SearchAttributes)
        }
        return "-"
    }
    if len(req.Items) == 1 {
        return req.Items[0].Label
    }
    return fmt.Sprintf("%d items", len(req.Items))
}
```

### Desktop notification for gpg_sign (Go)

```go
// In notification/desktop.go
func (h *Handler) notificationMeta(req *approval.Request) (summary, icon string) {
    switch req.Type {
    case approval.RequestTypeGPGSign:
        return "Commit Signing Request", "emblem-important"
    default:
        return "Secret Request", "dialog-password"
    }
}

func (h *Handler) formatGPGSignBody(req *approval.Request) string {
    info := req.GPGSignInfo
    if info == nil {
        return ""
    }
    var b strings.Builder
    b.WriteString(fmt.Sprintf("Repo: %s\n", info.RepoName))
    b.WriteString(commitSubject(info.CommitMsg))
    return b.String()
}
```

### Browser notification for gpg_sign (TypeScript)

```ts
// In notifications.ts
function formatBody(request: PendingRequest): string {
  if (request.type === "gpg_sign" && request.gpg_sign_info) {
    return `Repo: ${request.gpg_sign_info.repo_name}\n${commitSubject(request.gpg_sign_info.commit_msg)}`;
  }
  // ... existing get_secret / search logic unchanged
}

export function showRequestNotification(request: PendingRequest): void {
  const title = request.type === "gpg_sign"
    ? "Commit Signing Request"
    : "Secret Request";
  // ... rest unchanged
}
```

## State of the Art

| Old Approach | Current Approach | Impact |
|--------------|-----------------|--------|
| Native `<details>` considered legacy | `<details>/<summary>` is fully standard and recommended for progressive disclosure | Use it — no need for JS accordion |
| Svelte 4 `$: reactive` | Svelte 5 runes (`$state`, `$derived`, `$effect`) | All new code MUST use runes; codebase already uses them |

**Confirmed current in codebase:**
- `$state`, `$props`, `$effect` — all used in `RequestCard.svelte` and `App.svelte`
- `{@const}` — available in Svelte 5 template syntax for local bindings

## Open Questions

1. **Notify interface icon parameter placement**
   - What we know: `Notifier.Notify(summary, body string)` is defined; `DBusNotifier` hard-codes `"dialog-password"`.
   - What's unclear: Whether to add `icon` as a third positional param or pass a config struct.
   - Recommendation: Third positional param (`Notify(summary, body, icon string)`) — consistent with how the D-Bus method takes positional args, minimal diff, mock update is trivial.

2. **`--color-gpg-sign` value**
   - What we know: Dark background is `#1a1a2e`; primary blue is `#4a9eff`; the user wants purple/violet.
   - Recommendation: `#8b5cf6` (Tailwind violet-500) for badges/text on dark bg — bright enough to read; `#7c3aed` (violet-700) for left border accent — more muted, appropriate for a border.

3. **CLI `list` column rename**
   - What we know: Current column header is "SECRET". For gpg_sign rows, the column shows file count, not a secret name.
   - Recommendation: Rename to "SUMMARY" — generic enough to cover all request types. Update both `list` and `history` table headers together for consistency.

## Sources

### Primary (HIGH confidence)

- Direct codebase inspection — all findings are derived from reading actual source files. No external sources needed for this phase since it is pure extension of existing patterns.

### Internal file inventory examined

- `internal/notification/desktop.go` — current notification handler pattern
- `internal/notification/desktop_test.go` — existing test structure and assertions
- `internal/cli/client.go` — `PendingRequest` struct (missing `GPGSignInfo`)
- `internal/cli/format.go` — `FormatRequests`, `formatRequest`, `secretSummary` implementations
- `internal/approval/gpgsign.go` — `GPGSignInfo` struct (source of truth for field names/types)
- `internal/approval/manager.go` — `Request` type, observer pattern
- `internal/api/types.go` — API `PendingRequest` (has `GPGSignInfo *approval.GPGSignInfo`)
- `web/src/lib/types.ts` — frontend type definitions (missing `GPGSignInfo`, type union missing `"gpg_sign"`)
- `web/src/lib/RequestCard.svelte` — card component (has search/get_secret branches, needs gpg_sign)
- `web/src/lib/notifications.ts` — browser notification formatting (title + body)
- `web/src/App.svelte` — history list rendering
- `web/src/app.css` — CSS variables (dark theme palette)
- `web/deno.json` — confirms Svelte 5, Vite 6, Deno toolchain
- `main.go` — CLI command dispatch, confirms list/show/history all route through `runCLI` → `cli.Formatter`
- `Makefile` — confirms `go test -race ./...` is the test command

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — inspected actual source files; confirmed Svelte 5 runes, Deno/Vite, Go std lib only
- Architecture: HIGH — all changes are extensions of existing switch/if branches; no new abstractions
- Pitfalls: HIGH — derived from code reading (struct duplication, interface signature, test assertions)

**Research date:** 2026-02-24
**Valid until:** 2026-04-24 (stable codebase; no external dependencies introduced)
