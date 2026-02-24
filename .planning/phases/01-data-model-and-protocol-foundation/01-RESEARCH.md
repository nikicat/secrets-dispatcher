# Phase 1: Data Model and Protocol Foundation - Research

**Researched:** 2026-02-24
**Domain:** Go data model extension + HTTP API handler + WebSocket event integration
**Confidence:** HIGH (codebase read directly; all patterns are internal)

## Summary

Phase 1 is almost entirely an internal Go code extension exercise. The codebase is already
well-structured: `internal/approval` owns the canonical data model and blocking approval
pipeline, `internal/api` owns HTTP routes and WebSocket broadcasting. Adding `gpg_sign`
request support means adding a new `RequestType` constant, a new `GPGSignInfo` struct,
threading it through the existing `Request` struct, and writing a POST handler that calls
`RequireApproval` and blocks until the user decides.

The existing `RequireApproval` function already handles timeouts, observer notification,
history recording, and cancellation via `ctx.Done()` — so ERR-03 (signing requests expire
via the existing timeout mechanism) requires zero new code in the manager. The approval
pipeline is fully generic: any `RequestType` flows through the same goroutine-blocked
channel mechanism. The only new type-specific logic is the HTTP handler and the
`GPGSignInfo` payload carrier.

The POST + WebSocket hybrid approach chosen in CONTEXT.md fits the existing architecture
exactly. The WebSocket already broadcasts `request_created` and `request_resolved` events
for every `approval.Event`. The `gpg_sign` result (signature bytes) needs to be carried on
the `request_resolved` event. This requires adding a `Signature` field to the resolved
event payload, which means extending `WSMessage` and the approval `Request` struct to carry
the result bytes after approval.

**Primary recommendation:** Add `GPGSignInfo` to `internal/approval/manager.go` (new file),
add `RequestTypeGPGSign` constant, extend `Request.result` to carry signature bytes, add
`POST /api/v1/gpg-sign/request` handler following the exact `HandleApprove`/`HandleDeny`
pattern, and extend `WSMessage` with a `Signature` field for resolved gpg_sign events.

---

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

- **Phase 1 Scope**: Handler is fully working — creates `gpg_sign` approval request, blocks
  on user decision, returns result via WebSocket event. No gpg invocation. No thin client.
- **Unit tests only** — no manual curl smoke tests required.
- **API Contract**:
  1. Client POSTs to `POST /api/v1/gpg-sign/request` with commit data + context → returns
     request ID
  2. Client listens on existing WebSocket for approval result event carrying the signature
  - Reuses existing WebSocket infrastructure (no blocking HTTP pattern)
  - WebSocket event for `gpg_sign` MUST carry actual signature bytes (atomic delivery)
  - On denial or timeout, WebSocket event carries error status
- **gpg.program installation** (locked for Phase 2): shell wrapper `.sh` script calling
  `secrets-dispatcher gpg-sign -- "$@"`; user sets `git config --global gpg.program`

### Claude's Discretion

- Exact field naming conventions in `GPGSignInfo` struct
- Whether `GPGSignInfo` is a pointer or embedded on `Request`
- Internal organization of new types across files

### Deferred Ideas (OUT OF SCOPE)

None — discussion stayed within phase scope
</user_constraints>

---

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| SIGN-06 | Daemon creates `gpg_sign` approval request with full commit context and blocks on user decision | Add `RequestTypeGPGSign` + `GPGSignInfo` struct; call existing `RequireApproval`-equivalent that blocks on `req.done` channel; extend `Request` to carry context |
| SIGN-09 | Key ID / fingerprint extracted from gpg args and shown in approval context | `GPGSignInfo.KeyID` / `GPGSignInfo.Fingerprint` fields; sourced from POST body sent by thin client (Phase 2); in Phase 1 the fields exist on the struct and are accepted from the API request |
| ERR-03 | Signing requests expire via existing timeout mechanism | Zero new code needed — `RequireApproval` (or equivalent) already selects on `timer.C`; `gpg_sign` requests use the same manager timeout |
</phase_requirements>

---

## Standard Stack

### Core (already in use — no new dependencies)

| Package | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/google/uuid` | already imported | Request ID generation | Already used in `approval/manager.go` |
| `github.com/coder/websocket` | already imported | WebSocket transport | Already used in `api/websocket.go` |
| `encoding/json` | stdlib | JSON decode POST body / encode responses | Already used everywhere |
| `net/http` | stdlib | HTTP handler | Already used everywhere |
| `context` | stdlib | Blocking / cancellation | Already used in `RequireApproval` |

### Supporting (no new additions needed)

No new dependencies are introduced in Phase 1. All required primitives exist.

### Alternatives Considered

None — the decision to reuse existing infrastructure is locked.

## Architecture Patterns

### Existing Codebase Structure (relevant subset)

```
internal/
├── approval/
│   ├── manager.go      # Manager, RequireApproval, Approve, Deny, observer notify
│   └── types.go        # SenderInfo (only file currently)
├── api/
│   ├── types.go        # PendingRequest, HistoryEntry, WSMessage, etc.
│   ├── handlers.go     # Handlers struct, HandleApprove, HandleDeny, etc.
│   ├── server.go       # Route registration (apiMux.HandleFunc)
│   └── websocket.go    # WSHandler, WSMessage broadcast
```

New files to add:

```
internal/
├── approval/
│   └── gpgsign.go      # GPGSignInfo struct, RequestTypeGPGSign constant
├── api/
│   └── gpgsign.go      # GPGSignRequest, GPGSignResponse types, HandleGPGSignRequest handler
```

### Pattern 1: Adding a New RequestType

`RequestType` is a `string` typedef in `internal/approval/manager.go`. Adding `gpg_sign`:

```go
// internal/approval/gpgsign.go
package approval

// RequestTypeGPGSign is used when git requests a commit signature via gpg.program.
const RequestTypeGPGSign RequestType = "gpg_sign"

// GPGSignInfo carries the commit context fields for a signing approval request.
type GPGSignInfo struct {
    RepoName     string   `json:"repo_name"`
    CommitMsg    string   `json:"commit_msg"`
    Author       string   `json:"author"`
    Committer    string   `json:"committer"`
    KeyID        string   `json:"key_id"`
    Fingerprint  string   `json:"fingerprint,omitempty"`
    ChangedFiles []string `json:"changed_files"`
    ParentHash   string   `json:"parent_hash,omitempty"`
}
```

### Pattern 2: Extending `approval.Request` to carry GPGSignInfo

`Request` in `manager.go` must be extended. Since `GPGSignInfo` is only set for `gpg_sign`
requests, use a pointer field (nil for other request types):

```go
// In Request struct (manager.go):
GPGSignInfo *GPGSignInfo `json:"gpg_sign_info,omitempty"`
```

This follows the same optional-field pattern as `SearchAttributes map[string]string
\`json:"search_attributes,omitempty"\`` already on `Request`.

### Pattern 3: Extending Request to carry signature bytes on approval

The current `Request` struct carries the approval result as `result bool`. For `gpg_sign`,
the approving action must supply signature bytes so the WebSocket event can deliver them
atomically. Extend `Request`:

```go
// In Request struct (manager.go):
signature []byte // set by Approve path for gpg_sign requests; not exported
```

The manager's `Approve()` method is currently `func (m *Manager) Approve(id string) error`.
For `gpg_sign` approval, the caller (the web UI's approve button) does NOT supply the
signature — the signature comes from Phase 2 when the daemon calls real gpg. In Phase 1,
the handler returns mock/placeholder signature data on approval (per CONTEXT.md).

Therefore in Phase 1: when `Approve(id)` is called on a `gpg_sign` request, the manager
sets `req.signature = []byte("PLACEHOLDER_SIGNATURE")` (or similar) before closing
`req.done`. The POST handler then reads this from the request after unblocking.

**Alternative approach:** The POST handler itself generates the placeholder and does not
involve the manager's `Approve` path for carrying the signature. After `req.done` closes
(approval), the handler injects placeholder bytes directly. This is simpler for Phase 1
since Phase 2 will replace this anyway.

### Pattern 4: POST Handler Structure

Follows the exact pattern of `HandleApprove` / `HandleDeny`. New handler in
`internal/api/gpgsign.go`:

```go
// HandleGPGSignRequest handles POST /api/v1/gpg-sign/request
func (h *Handlers) HandleGPGSignRequest(w http.ResponseWriter, r *http.Request) {
    if r.Method != http.MethodPost {
        writeError(w, "method not allowed", http.StatusMethodNotAllowed)
        return
    }

    var req GPGSignRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        writeError(w, "invalid request body", http.StatusBadRequest)
        return
    }

    // Validate required fields ...

    // Create approval request (blocks until approved/denied/timeout)
    id, err := h.manager.RequestGPGSign(r.Context(), req)
    // ... return GPGSignResponse{RequestID: id} or error
}
```

The blocking call must happen in the goroutine serving the HTTP request. Since the decision
is delivered to the thin client via WebSocket (not the HTTP response), the POST can return
the request ID immediately and not block. The WebSocket then delivers the result.

**Clarification on blocking vs non-blocking POST:**

Per CONTEXT.md, the POST returns the request ID immediately, then the client listens on
WebSocket for the result. This means the POST handler should NOT block — it creates the
request, registers it in the manager, and returns `{request_id: "..."}`. The approval
manager's observer pipeline then broadcasts the result over WebSocket when resolved.

This is architecturally different from the existing `RequireApproval` which blocks the
calling goroutine. For `gpg_sign`, we need a non-blocking "create and return ID" variant.

### Pattern 5: Non-Blocking Request Creation

The existing `RequireApproval` creates the `Request` and then blocks. For `gpg_sign`, we
need to split: create and register the request (return ID), then the WebSocket observer
delivers the result. This requires a new method on Manager:

```go
// CreateGPGSignRequest creates a pending gpg_sign request and returns its ID.
// It does NOT block. The result is delivered via the observer pipeline (WebSocket).
func (m *Manager) CreateGPGSignRequest(ctx context.Context, info *GPGSignInfo, client string) (string, error) {
    now := time.Now()
    req := &Request{
        ID:          uuid.New().String(),
        Client:      client,
        CreatedAt:   now,
        ExpiresAt:   now.Add(m.timeout),
        Type:        RequestTypeGPGSign,
        GPGSignInfo: info,
        done:        make(chan struct{}),
    }
    m.mu.Lock()
    m.pending[req.ID] = req
    m.mu.Unlock()
    m.notify(Event{Type: EventRequestCreated, Request: req})
    // Timeout goroutine — mirrors what RequireApproval does inline
    go func() {
        select {
        case <-req.done:
            // resolved by Approve or Deny
        case <-time.After(m.timeout):
            m.mu.Lock()
            delete(m.pending, req.ID)
            m.mu.Unlock()
            m.notify(Event{Type: EventRequestExpired, Request: req})
        case <-ctx.Done():
            m.mu.Lock()
            delete(m.pending, req.ID)
            m.mu.Unlock()
            m.notify(Event{Type: EventRequestCancelled, Request: req})
        }
    }()
    return req.ID, nil
}
```

**ERR-03 compatibility:** The timeout goroutine above fires `EventRequestExpired` via the
existing observer pipeline, satisfying ERR-03 with no changes to the approval manager's
core machinery.

### Pattern 6: WebSocket Event Extension for gpg_sign result

When `Approve(id)` fires `EventRequestApproved` for a `gpg_sign` request, the WebSocket
`OnEvent` handler must include signature bytes in the `request_resolved` message. Extend
`WSMessage`:

```go
// For gpg_sign approval result
Signature string `json:"signature,omitempty"` // base64-encoded; empty for non-gpg_sign
```

In Phase 1, the signature is a placeholder (`[]byte("PLACEHOLDER_SIGNATURE")`). In Phase 2
it becomes real gpg output.

### Pattern 7: Route Registration

In `internal/api/server.go`, inside `newServerWithHandlers`:

```go
apiMux.HandleFunc("/api/v1/gpg-sign/request", handlers.HandleGPGSignRequest)
```

This follows the existing flat-path pattern for simple routes.

### Pattern 8: PendingRequest serialization for gpg_sign

The existing `HandlePendingList` converts `approval.Request` → `api.PendingRequest`. It
must be extended to include `GPGSignInfo` when the request type is `gpg_sign`. Extend
`api.PendingRequest`:

```go
GPGSignInfo *approval.GPGSignInfo `json:"gpg_sign_info,omitempty"`
```

And update the conversion loop in `HandlePendingList` and `convertHistoryEntry`.

### Anti-Patterns to Avoid

- **Blocking the POST handler goroutine:** The POST handler should return the request ID
  immediately. Do not call `RequireApproval` (which blocks) from the HTTP handler — this
  would hang the HTTP request for up to 5 minutes and break the WebSocket delivery model.
- **Creating a separate approval pipeline for gpg_sign:** The existing observer/event system
  handles all request types uniformly. Use `Subscribe`/`OnEvent` as-is.
- **Storing signature bytes in `Manager.Approve`:** `Approve(id string) error` has no
  signature parameter. In Phase 1, the placeholder is injected by the handler after
  `EventRequestApproved` fires. In Phase 2 this will be wired differently. Do not change
  the `Approve` signature now.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| Request ID generation | Custom UUID logic | `github.com/google/uuid` | Already imported, proven |
| JSON encoding/decoding | Manual marshaling | `encoding/json` | Standard; matches entire codebase |
| WebSocket messaging | New transport | `github.com/coder/websocket` | Already in use |
| Timeout/expiry | Custom timer logic | `time.After` in goroutine (mirrors existing pattern) | Existing `RequireApproval` shows the pattern |

**Key insight:** This phase adds no new external dependencies. Everything is an internal
extension of existing patterns.

## Common Pitfalls

### Pitfall 1: Making the POST handler block

**What goes wrong:** Calling `RequireApproval` (which blocks) from `HandleGPGSignRequest`
ties up the HTTP server goroutine for up to the full timeout (default 5 minutes). The thin
client's POST would hang waiting, defeating the non-blocking POST + WebSocket design.

**Why it happens:** `RequireApproval` was designed for the D-Bus proxy, which owns a
goroutine per session and can afford to block indefinitely.

**How to avoid:** Use `CreateGPGSignRequest` (non-blocking) that registers the request and
returns the ID. The thin client's WebSocket connection receives the result event.

**Warning signs:** Test times out waiting for POST response; handler goroutine is blocked.

### Pitfall 2: Forgetting to extend `HandlePendingList` and `convertHistoryEntry`

**What goes wrong:** `gpg_sign` requests show up in the pending list and history but with
nil `GPGSignInfo`, breaking Phase 3 display work.

**Why it happens:** Both functions have explicit field-by-field conversion code that must
be updated when new fields are added to `approval.Request`.

**How to avoid:** Grep for all `approval.Request` → `api.PendingRequest` conversion sites
before declaring Phase 1 done. There are two: `HandlePendingList` (line ~89) and
`convertHistoryEntry` (line ~199) in `handlers.go`.

### Pitfall 3: Timeout goroutine leak

**What goes wrong:** If the context is cancelled (server shutdown), the timeout goroutine
may block on `req.done` forever if `done` is never closed.

**Why it happens:** `req.done` is only closed by `Approve` or `Deny`. If neither is called
and the context fires `ctx.Done()`, the goroutine cleans up correctly. But if `req.done`
is closed by a concurrent path after `ctx.Done()` fires, there is no double-close issue
(channel close is atomic and only done once per request). Verify that `Approve`/`Deny`
never close an already-closed channel — the existing code does this safely via mutex.

**How to avoid:** Follow the same goroutine pattern as `RequireApproval` exactly. The
existing code has no leak because `delete(m.pending, req.ID)` happens before `done` closes.

### Pitfall 4: WebSocket event missing signature for gpg_sign results

**What goes wrong:** Thin client (Phase 2) receives `request_resolved` for its gpg_sign
request but finds no signature in the event. Thin client has no way to retrieve it.

**Why it happens:** The existing WebSocket event for `request_resolved` only carries `id`
and `result` (approved/denied string). No field for signature bytes exists yet.

**How to avoid:** Extend `WSMessage` with `Signature string \`json:"signature,omitempty"\``
in Phase 1. Even with placeholder bytes, the field must be present so Phase 2 can rely on
the contract. The CONTEXT.md decision says: "WebSocket event must carry the actual signature
bytes (atomic delivery — no separate fetch)."

### Pitfall 5: Duplicate `SenderInfo` type

**What goes wrong:** `approval.SenderInfo` and `api.SenderInfo` are currently duplicate
structs (verified in codebase). Adding a third struct for GPGSign context would compound
this pattern.

**Why it happens:** Historical divergence between API layer and approval layer types.

**How to avoid:** `GPGSignInfo` belongs only in `internal/approval` (the canonical data
model). The `api` layer uses `*approval.GPGSignInfo` directly in `api.PendingRequest`
rather than re-declaring fields. This avoids a third duplicate struct. The precedent of
`api.SenderInfo` duplicating `approval.SenderInfo` is a known wart — don't extend it.

## Code Examples

### Verified: Existing RequestType declaration pattern

```go
// Source: internal/approval/manager.go lines 54-57
type RequestType string

const (
    RequestTypeGetSecret RequestType = "get_secret"
    RequestTypeSearch    RequestType = "search"
)
```

Add alongside: `RequestTypeGPGSign RequestType = "gpg_sign"`

### Verified: Existing observer notification on timeout

```go
// Source: internal/approval/manager.go lines 263-265
case <-timer.C:
    m.notify(Event{Type: EventRequestExpired, Request: req})
    return ErrTimeout
```

ERR-03 is satisfied by this mechanism. The timeout goroutine in `CreateGPGSignRequest`
mirrors this pattern.

### Verified: Existing WSMessage structure

```go
// Source: internal/api/websocket.go lines 29-50
type WSMessage struct {
    Type    string `json:"type"`
    // ...
    ID     string `json:"id,omitempty"`
    Result string `json:"result,omitempty"`
    // ADD: Signature string `json:"signature,omitempty"`
}
```

### Verified: Route registration pattern

```go
// Source: internal/api/server.go lines 44-49
apiMux.HandleFunc("/api/v1/status", handlers.HandleStatus)
apiMux.HandleFunc("/api/v1/pending", handlers.HandlePendingList)
apiMux.HandleFunc("/api/v1/log", handlers.HandleLog)
apiMux.HandleFunc("/api/v1/ws", wsHandler.HandleWS)
// ADD: apiMux.HandleFunc("/api/v1/gpg-sign/request", handlers.HandleGPGSignRequest)
```

### Verified: JSON decode + writeJSON pattern for POST handlers

```go
// Source: internal/api/handlers.go lines 255-260 (HandleAuth)
var req AuthRequest
if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
    writeError(w, "invalid request body", http.StatusBadRequest)
    return
}
// ... process ...
writeJSON(w, ActionResponse{Status: "authenticated"})
```

## State of the Art

| Old Approach | Current Approach | Impact for Phase 1 |
|--------------|------------------|--------------------|
| `RequireApproval` blocking the caller | Non-blocking `CreateGPGSignRequest` + WebSocket delivery | Phase 1 must implement the non-blocking variant |
| Single `result bool` on `Request` | Needs `signature []byte` for gpg_sign | Extend `Request` struct (internal field) |

**Nothing deprecated:** This is a greenfield extension on a codebase that is already modern.

## Open Questions

1. **Where does `client` come from in the POST body?**
   - What we know: `approval.Request.Client` is set from D-Bus proxy context in existing
     code. For gpg_sign POSTs, the thin client must supply it.
   - What's unclear: Should `client` be derived server-side (e.g. from auth session) or
     sent by the thin client in the POST body? Phase 1 has no auth for the thin client.
   - Recommendation: Accept `client` as a field in `GPGSignRequest` POST body (thin client
     sends its hostname or a configured name). Keep it simple for Phase 1.

2. **Is the `/api/v1/gpg-sign/request` endpoint auth-protected?**
   - What we know: All routes under `/api/` go through `auth.Middleware`. The thin client
     (Phase 2) will need to authenticate. Phase 1 has no thin client.
   - What's unclear: Should the gpg-sign endpoint be exempt from auth (like `/api/v1/auth`)
     or require the same cookie/token auth?
   - Recommendation: Register under the authenticated `apiMux` for Phase 1 (unit tests can
     use `NewDisabledManager` and bypass auth). Phase 2 can revisit if the thin client needs
     a special auth path.

3. **Should `CreateGPGSignRequest` use `context.Background()` or the request's context?**
   - What we know: The timeout goroutine needs a context for `ctx.Done()`. The HTTP request
     context cancels when the HTTP connection closes.
   - What's unclear: Should connection drop cancel the pending approval?
   - Recommendation: Use `context.Background()` for the goroutine. The thin client in
     Phase 2 will have its own connection lifecycle. A dropped POST connection should not
     cancel a pending approval that the user is reviewing in the web UI.

## Sources

### Primary (HIGH confidence)

- Direct codebase read: `internal/approval/manager.go` — manager patterns, `RequireApproval`, observer pipeline
- Direct codebase read: `internal/approval/types.go` — `SenderInfo`, existing type declarations
- Direct codebase read: `internal/api/handlers.go` — handler patterns, conversion code
- Direct codebase read: `internal/api/server.go` — route registration pattern
- Direct codebase read: `internal/api/types.go` — `PendingRequest`, `WSMessage`, `HistoryEntry`
- Direct codebase read: `internal/api/websocket.go` — WebSocket event structure

### Secondary (MEDIUM confidence)

None needed — all findings are from direct codebase inspection.

### Tertiary (LOW confidence)

None.

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — read directly from go.mod imports and codebase
- Architecture: HIGH — read directly from existing handler and manager code
- Pitfalls: HIGH — derived from reading the actual conversion code and approval pipeline

**Research date:** 2026-02-24
**Valid until:** Until codebase changes (stable internal code, not a fast-moving ecosystem)
