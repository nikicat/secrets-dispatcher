# Architecture Patterns: GPG Signing Proxy

**Domain:** GPG commit signing proxy integrated into existing approval pipeline
**Researched:** 2026-02-24
**Confidence:** HIGH — based on direct codebase analysis

---

## Existing Architecture Overview

The codebase follows a clean layered architecture with a single central `approval.Manager` at the core. All approval-requiring channels (D-Bus proxy, future GPG proxy) gate through the same manager and share the same observer-driven notification infrastructure.

```
git / gpg-agent
      |
      | (gpg.program interface: args + stdin)
      v
[gpg-sign subcommand]  ←── new thin client
      |
      | POST /api/v1/gpg-sign/request  (HTTP to daemon)
      v
[API Server]  ──────────────────────────────────────────────────────────
      |                                                                  |
      v                                                                  v
[approval.Manager]                                               [WSHandler]
      |         \                                                        |
      |          → observers: desktop notifications,                     |
      |            WebSocket broadcast                                   |
      |                                                                  |
      v (user approves via web UI / CLI)                                 |
[approval.Manager.Approve(id)]                                           |
      |                                                                  |
      v                                                                  |
[GPG Sign Handler in daemon]  ← wakes from channel                      |
      |                                                                  |
      | exec real gpg                                                    |
      v                                                                  |
[gpg binary]  ──→ ASCII-armored signature                               |
      |                                                                  |
      v                                                                  |
[signature result stored on Request]                                     |
      |                                                                  |
      v                                                                  |
WebSocket: "request_resolved" + signature payload  ──────────────────────
      |
      v
[gpg-sign client] reads signature from WebSocket
      |
      v
stdout (git reads this as the PGP signature)
```

---

## Component Boundaries

### Existing Components (unchanged interface)

| Component | Package | Responsibility | Notes |
|-----------|---------|---------------|-------|
| Approval Manager | `internal/approval` | Tracks pending requests, blocks callers, notifies observers | Used unmodified |
| REST API Server | `internal/api` | HTTP routing, auth middleware, request handlers | Needs new routes |
| WebSocket Handler | `internal/api` | Broadcasts approval events to browser clients | Needs signature delivery |
| Notification Handler | `internal/notification` | Desktop notification on new request | Used unmodified — sees GPG requests as any other request |
| CLI Client | `internal/cli` | HTTP client for approve/deny/list/show | Used unmodified — displays GPG requests via existing list/show |
| Auth | `internal/api` | JWT + cookie auth | Used unmodified |
| D-Bus Proxy | `internal/proxy` | Secret Service proxy | Untouched |

### New Components

| Component | Location | Responsibility |
|-----------|----------|---------------|
| `gpg-sign` subcommand | `main.go` (new case) | Thin client: parse git args, collect context, call daemon, return signature |
| GPG context collector | `internal/gpgsign/context.go` | Parse commit object from stdin; extract author, committer, message, repo path; run `git diff-tree` for file list |
| GPG sign request handler | `internal/api/handlers.go` (new handler) | Accept signing requests, call approval manager, invoke real gpg, return signature |
| GPG API types | `internal/api/types.go` (additions) | `GPGSignRequest`, `GPGSignResponse` structs |
| Approval type extension | `internal/approval/types.go` | New `RequestType = "gpg_sign"` constant + `GPGSignInfo` struct on `Request` |

### Modified Components

| Component | Change Required |
|-----------|----------------|
| `approval.Request` | Add `GPGSignInfo` field (repo, author, message, files, key ID, raw commit object) |
| `approval.Manager.RequireApproval` | Either extend signature or add new `RequireGPGSignApproval` method that blocks and returns `(signature []byte, error)` |
| `internal/api/types.go` | Add `GPGSignInfo` to `PendingRequest` for API responses |
| `internal/api/handlers.go` | Add `HandleGPGSign` endpoint |
| `internal/api/server.go` | Register `/api/v1/gpg-sign` route |
| `internal/api/websocket.go` | Include `GPGSignInfo` when broadcasting `request_created` events |
| `internal/cli/format.go` | Format GPG sign requests distinctly in list/show output |
| `internal/notification/desktop.go` | Format GPG sign request notifications with commit summary |
| Web UI | Display GPG sign context (repo, message, author, changed files) in approval card |

---

## Data Flow: Full Signing Sequence

### Step 1 — Git invokes gpg.program

```
git commit -S
  → exec: secrets-dispatcher gpg-sign --status-fd=2 -bsau <key-id>
  → stdin: raw commit object bytes
```

Git passes the commit object on stdin and reads the PGP signature from stdout. The `-bsau` flags specify: binary detached signature (`-b`), sign (`-s`), ASCII armor (`-a`), user key (`-u <key-id>`). Git also passes `--status-fd=2` so GPG status messages (NEWSIG, SIG_CREATED, etc.) go to stderr. The thin client must write these status lines to stderr to satisfy git's verification.

### Step 2 — gpg-sign client: context collection

```
secrets-dispatcher gpg-sign [args from git]
  1. Read raw commit object bytes from stdin (save for forwarding to daemon)
  2. Parse commit object: extract author, committer, commit message
  3. Determine repo root: walk up from $PWD looking for .git/
  4. Extract repo name from remote URL or directory basename
  5. Run: git -C <repo-root> diff-tree --no-commit-id -r --name-only HEAD
     → produces list of changed files
  6. Build GPGSignRequest payload
```

Note: Changed files require a special approach since the commit is not yet written. Use `git diff --cached --name-only` before signing, or accept that the commit object's tree can be diffed against its parent via `git diff-tree` after the fact. The simpler approach is to use `git diff --cached --name-only` from the repo root — this is available before the commit is finalized and is what git's signing hook has access to.

### Step 3 — HTTP request to daemon

```
POST http://127.0.0.1:8484/api/v1/gpg-sign/request
Authorization: Bearer <token from state dir>
Content-Type: application/json

{
  "key_id": "ABC123",
  "commit_object": "<base64-encoded raw bytes>",
  "repo_name": "secrets-dispatcher",
  "repo_path": "/home/nb/src/secrets-dispatcher",
  "author": "Name <email> timestamp",
  "committer": "Name <email> timestamp",
  "message": "feat: add GPG signing proxy",
  "changed_files": ["main.go", "internal/gpgsign/context.go"]
}
```

The daemon endpoint is synchronous from the client's perspective: it blocks until the user approves/denies or timeout, then returns the signature or an error.

### Step 4 — Daemon: approval request creation

```
HandleGPGSign:
  1. Decode request body
  2. Create approval.Request with Type="gpg_sign", GPGSignInfo populated
  3. Call approvalMgr.RequireGPGSignApproval(ctx, req) — blocks
  4. On approval: exec gpg with original args + stdin from commit_object
  5. Capture gpg stdout (ASCII armor signature) and stderr (status lines)
  6. Return signature + status lines in response
  7. On denial: return 403
  8. On timeout: return 408
```

### Step 5 — Observer notifications (automatic, no new code)

When `approval.Manager` receives the new request:
- Desktop notifier (`notification.Handler.OnEvent`) fires: sends OS notification "GPG Sign Request: feat: add GPG signing proxy"
- WebSocket handler (`wsConnection.OnEvent`) fires: broadcasts `request_created` with full `GPGSignInfo`

### Step 6 — User approval (existing flow, no new code)

User sees request in web UI or CLI, clicks approve. Existing `POST /api/v1/pending/{id}/approve` handler calls `approval.Manager.Approve(id)`. The blocked `HandleGPGSign` handler wakes up.

### Step 7 — Daemon: invoke real gpg

```go
cmd := exec.CommandContext(ctx, "gpg", "--status-fd=2", "-bsau", keyID)
cmd.Stdin = bytes.NewReader(commitObject)
cmd.Stdout = &signatureBuffer
cmd.Stderr = &statusBuffer
err := cmd.Run()
```

The daemon passes the identical args and stdin that git originally sent. GPG may invoke pinentry for passphrase if not cached — this is intentional per project design.

### Step 8 — Return signature to thin client

```json
HTTP 200
{
  "signature": "<base64 ASCII-armored PGP signature>",
  "status_output": "... gpg status lines ..."
}
```

### Step 9 — gpg-sign client writes to stdout/stderr

```
gpg-sign:
  1. Decode base64 signature
  2. Write signature bytes to stdout  ← git reads this as the PGP sig
  3. Write status_output to stderr     ← git reads this for NEWSIG etc.
  4. Exit 0
```

Git reads the signature from stdout and embeds it in the commit object.

---

## Approval Manager Extension

The current `RequireApproval` only returns `error` (approved=nil, denied/timeout=error). For GPG signing, the daemon needs to return the signature bytes on approval. Two options:

**Option A — New method (recommended):** Add `RequireGPGSignApproval` that blocks and returns `(approved bool, error)`, then the handler calls gpg afterward.

**Option B — Generic result channel:** Add a `Result interface{}` field to `Request` that the `Approve()` path can optionally set before signaling the done channel.

Option A is cleaner: the approval manager stays focused on boolean decisions. The signature production happens in the HTTP handler after `RequireApproval` returns nil. The handler already has the context it needs (commit object, key ID). This matches the existing D-Bus proxy pattern: approval manager approves, then the proxy layer does the actual secret forwarding.

Recommended: keep `RequireApproval` unchanged. The HTTP handler for GPG sign:
1. Calls `RequireApproval` with the new `gpg_sign` request type — blocks
2. On `nil` return: executes gpg subprocess
3. Returns signature in HTTP response

---

## Approval Manager Type Extension

The `approval.Request` struct needs a discriminated union approach since it currently carries D-Bus-specific fields. For GPG sign requests, those fields are meaningless.

```go
// In internal/approval/types.go

const RequestTypeGPGSign RequestType = "gpg_sign"

// GPGSignInfo holds context for a GPG signing request.
type GPGSignInfo struct {
    KeyID        string   `json:"key_id"`
    RepoName     string   `json:"repo_name"`
    RepoPath     string   `json:"repo_path"`
    Author       string   `json:"author"`
    Committer    string   `json:"committer"`
    Message      string   `json:"message"`
    ChangedFiles []string `json:"changed_files"`
    CommitObject []byte   `json:"-"` // raw bytes, not serialized to observers
}

// Add to Request struct:
GPGSignInfo *GPGSignInfo `json:"gpg_sign_info,omitempty"`
```

The `CommitObject` is not propagated to observers (web UI, CLI, notifications) — only the descriptive context fields are. The raw commit bytes are stored on the request for the daemon to retrieve when it calls real gpg after approval.

The existing `Items`, `Session`, `SenderInfo`, `SearchAttributes` fields on `Request` are left as-is but will be zero/empty for GPG sign requests. This is acceptable — the type discriminator (`Type` field) tells readers which fields are relevant.

---

## API Layer Extension

### New endpoint

```
POST /api/v1/gpg-sign/request
```

This endpoint is the only new route. It is synchronous and long-lived (blocks up to the approval timeout). The existing `/api/v1/pending/{id}/approve` handles user approval as before.

**Why not a separate pending endpoint?** The existing `/api/v1/pending` already returns all pending requests regardless of type. GPG sign requests appear in the list naturally. The web UI and CLI filter/display by type.

### Route registration (in `internal/api/server.go`)

```go
apiMux.HandleFunc("/api/v1/gpg-sign/request", handlers.HandleGPGSign)
```

### Authentication for gpg-sign client

The thin client needs a Bearer token to call the daemon. It reads from the same state directory as CLI commands. No new auth mechanism needed — the existing `api.LoadAuth(stateDir)` + `auth.Token()` pattern works.

---

## WebSocket Extension

The `WSMessage` type needs `GPGSignInfo` added to `PendingRequest` so the web UI receives GPG sign context on `request_created` events. No structural changes to the WebSocket protocol — it's an additive field.

---

## Build Order (dependency graph)

```
1. approval types (internal/approval/types.go)
   └─ Add RequestTypeGPGSign + GPGSignInfo struct
   └─ Add GPGSignInfo field to Request

2. approval manager (internal/approval/manager.go)
   └─ No interface change needed — RequireApproval works as-is

3. API types (internal/api/types.go)
   └─ Add GPGSignInfo to PendingRequest
   └─ Add GPGSignRequest / GPGSignResponse request/response types

4. GPG context collector (internal/gpgsign/context.go)  [NEW PACKAGE]
   └─ Parse git commit object format (author/committer/message)
   └─ Detect repo root + name
   └─ Collect changed files via git diff --cached

5. API handler (internal/api/handlers.go)
   └─ Add HandleGPGSign — depends on steps 1, 3, 4

6. API route (internal/api/server.go)
   └─ Register /api/v1/gpg-sign/request — depends on step 5

7. Notification formatting (internal/notification/desktop.go)
   └─ Add GPG sign case to formatBody — depends on step 1

8. CLI formatting (internal/cli/format.go)
   └─ Add GPG sign display in FormatRequest/FormatRequests — depends on step 3

9. gpg-sign subcommand (main.go + internal/gpgsign/client.go)  [NEW]
   └─ Parse git args, call collector, POST to daemon, pipe response to stdout
   └─ Depends on steps 3, 4, 6

10. Web UI updates
    └─ Display GPGSignInfo fields in approval card
    └─ Depends on steps 3, 6
```

Steps 1–3 are pure data model changes with no external dependencies. Steps 4–6 are the daemon-side implementation. Steps 7–8 extend existing UI layers. Step 9 is the new binary entrypoint. Step 10 is UI polish.

Steps 4 and 9 can be developed independently once step 3 types are stable.

---

## Key Design Decisions

### Decision 1: Synchronous HTTP for signature delivery

The gpg-sign client uses a single synchronous POST that blocks until the daemon returns the signature. The alternative (client polls or subscribes via WebSocket) is more complex and not needed — git's subprocess call is inherently blocking. A synchronous HTTP response carrying the signature is the simplest correct design.

The timeout on this HTTP call should match the daemon's approval timeout. The client should pass a context with that timeout. On timeout, git will receive an error from the gpg.program invocation and report "gpg failed to sign the data".

### Decision 2: Commit object stored on Request, not passed through approval events

The raw commit bytes are not serialized into JSON events. Only human-readable context (repo, author, message, files) flows through the observer pipeline. This keeps WebSocket message sizes reasonable and avoids leaking raw git internals to the web UI. The daemon handler retrieves commit bytes from the in-memory `Request` struct after approval.

### Decision 3: No new API for pending GPG requests

GPG sign requests use the same `GET /api/v1/pending` list as D-Bus requests. Type discriminator (`type: "gpg_sign"`) lets clients render them differently. This avoids forking the CLI and web UI display logic unnecessarily.

### Decision 4: Real gpg exec happens in daemon, not in gpg-sign client

This is already established in PROJECT.md. The implication: the daemon must have access to the user's GPG agent socket. Since the daemon runs as the same user (`secrets-dispatcher serve`), it inherits the `GPG_TTY` and `GNUPGHOME` environment. When gpg needs a passphrase, pinentry appears as normal. The daemon must not set `--batch` or `--no-tty` flags that suppress the passphrase prompt.

### Decision 5: gpg-sign client auth uses existing state dir token

The gpg-sign thin client runs as the same user as the daemon. It loads the auth token from `$XDG_STATE_HOME/secrets-dispatcher/` exactly like the existing CLI commands. No credential problem — same user, same filesystem.

---

## Scalability Considerations

| Concern | Current Scale | GPG Impact |
|---------|--------------|------------|
| Concurrent signing requests | Rare (one git commit at a time per user) | Multiple Claude Code sessions could trigger parallel commits; each creates one pending request; manager handles concurrency |
| Request expiry | 5 min default | Appropriate; git will time out waiting for gpg.program before then |
| WebSocket broadcast size | Small (approval events) | GPG events are similar size; changed files list could be large for mega-commits (mitigate: cap at 50 files shown) |

---

## Error Cases and Handling

| Error Condition | Daemon Response | Client Behavior |
|----------------|----------------|-----------------|
| User denies | HTTP 403 | gpg-sign exits non-zero; git reports "gpg failed to sign the data" |
| Approval timeout | HTTP 408 | gpg-sign exits non-zero; git reports signing failure |
| Daemon not running | Connection refused | gpg-sign exits non-zero with error message to stderr |
| Real gpg fails (bad key, etc.) | HTTP 500 with error | gpg-sign exits non-zero; git reports signing failure |
| Context lost (parse error) | HTTP 400 | gpg-sign exits non-zero |

On denial, git will abort the commit. The user made that choice explicitly, so this is the correct behavior.

---

## Sources

- Direct codebase analysis: `internal/approval/manager.go`, `internal/api/handlers.go`, `internal/api/websocket.go`, `internal/api/server.go`, `internal/cli/client.go`, `internal/notification/desktop.go`, `main.go`
- `.planning/codebase/ARCHITECTURE.md` — existing architecture documentation
- `.planning/PROJECT.md` — milestone requirements and key decisions
- GPG interface: git calls `gpg.program` with args `--status-fd=2 -bsau <key-id>`, commit object on stdin; expects ASCII-armored detached signature on stdout and GPG status lines on stderr (HIGH confidence — confirmed by WebSearch against multiple sources)
