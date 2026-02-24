# Phase 2: Core Signing Flow - Research

**Researched:** 2026-02-24
**Domain:** Go CLI subcommand, HTTP-over-Unix-socket, WebSocket client, GPG subprocess, commit object parsing
**Confidence:** HIGH

<user_constraints>
## User Constraints (from CONTEXT.md)

### Locked Decisions

**Installation & setup**
- `gpg-sign` is a subcommand of the `secrets-dispatcher` binary — not a separate binary or shell wrapper
- `secrets-dispatcher gpg-sign setup` command configures git's `gpg.program` setting
- Setup defaults to `--global` scope; user can pass `--local` for per-repo
- Setup only sets `gpg.program` — does NOT enable `commit.gpgsign=true` (user opts in to auto-signing separately)

**Daemon discovery**
- Daemon listens on a Unix socket at `$XDG_RUNTIME_DIR/secrets-dispatcher/api.sock`
- Thin client connects to the same Unix socket using HTTP-over-Unix-socket (reuses existing HTTP API handlers)
- Thin client opens WebSocket connection FIRST, then POSTs the signing request, then waits for `request_resolved` event matching the request ID — ensures no events are missed

**Real GPG invocation**
- Daemon finds real gpg by scanning PATH, skipping its own binary
- Args are parsed and reconstructed (not passed verbatim) — thin client extracts key ID etc. from git's gpg args, daemon builds its own gpg invocation
- Commit object (raw data from stdin) is sent to the daemon in the API request body; daemon feeds it to real gpg's stdin — all signing happens daemon-side
- Daemon assumes the user's gpg-agent is reachable (inherits environment); if gpg-agent isn't running, real gpg fails and the error propagates naturally

**Error UX**
- Named errors with actionable guidance — messages identify `secrets-dispatcher` by name and suggest fixes
- Daemon unreachable: `secrets-dispatcher: daemon unreachable at /run/user/.../api.sock. Is secrets-dispatcher running?` (exit 2)
- User denied: `secrets-dispatcher: signing request denied by user` (exit 1)
- Timeout: `secrets-dispatcher: signing request timed out (no response within Xs)` — distinct from denial
- Debug mode: `SECRETS_DISPATCHER_DEBUG=1` environment variable enables verbose stderr logging for connection troubleshooting

### Claude's Discretion
- Exact gpg arg parsing strategy (how to extract key ID, status-fd handling)
- WebSocket connection timeout and retry logic
- Unix socket directory creation and permissions
- How to detect "self" when scanning PATH for real gpg

### Deferred Ideas (OUT OF SCOPE)

None — discussion stayed within phase scope
</user_constraints>

<phase_requirements>
## Phase Requirements

| ID | Description | Research Support |
|----|-------------|-----------------|
| SIGN-01 | `gpg-sign` subcommand intercepts git's `gpg.program` call and blocks until user approves or denies | Git invocation pattern confirmed; thin client reads stdin commit object and blocks on WebSocket event |
| SIGN-02 | Thin client parses raw commit object from stdin to extract author, committer, message, and parent hash | Commit object format verified: plain text, one header per line, blank line before message body |
| SIGN-03 | Thin client resolves repository name from working directory via `git rev-parse --show-toplevel` | Standard `exec.Command("git", "rev-parse", "--show-toplevel")` — no library needed |
| SIGN-04 | Thin client collects changed files list via `git diff --cached --name-only` | Standard `exec.Command("git", "diff", "--cached", "--name-only")` — no library needed |
| SIGN-05 | Thin client sends commit data + context to daemon via API as JSON (commit object as string field) | HTTP POST to `/api/v1/gpg-sign/request` over Unix socket; `CommitObject string` added to `GPGSignInfo` |
| SIGN-07 | Daemon calls real `gpg` with original args verbatim after approval, captures signature and status output | `exec.Command` with reconstructed args (`--status-fd=2 -bsau <keyID>`); stdout = signature, stderr = status |
| SIGN-08 | Signature and gpg status output returned to thin client; client writes signature to stdout and status to stderr | WebSocket `request_resolved` message extended with `GPGStatus string` field; thin client decodes base64 signature |
| ERR-01 | Thin client exits non-zero with clear stderr message when daemon is unreachable | Unix socket connect failure → exit 2; `SECRETS_DISPATCHER_DEBUG=1` enables verbose logging |
| ERR-02 | Exit code from real gpg failures propagated through daemon to thin client | Daemon captures gpg exit code; WebSocket message carries `ExitCode int`; thin client exits with that code |
</phase_requirements>

## Summary

Phase 2 builds the complete signing pipeline: a thin client subcommand (`secrets-dispatcher gpg-sign`) that intercepts git's GPG invocation, forwards the signing request to the daemon over a Unix socket, and blocks until the user approves in the web UI. The daemon then calls real gpg, captures the signature, and delivers it back to the thin client via WebSocket.

**Critical empirical finding from testing:** Git does NOT shell-split `gpg.program` — it execvp's the full string as a single path. Setting `gpg.program` to `"secrets-dispatcher gpg-sign"` (with space) fails with "cannot exec: No such file or directory". This means `gpg-sign setup` MUST write a minimal shell wrapper script (e.g., `~/.local/bin/secrets-dispatcher-gpg`) that calls `exec secrets-dispatcher gpg-sign "$@"`, and configures git to use that wrapper path. This is consistent with STATE.md's note that "research recommends shell wrapper as simpler."

**Second critical finding:** The existing WebSocket handler (`HandleWS`) does its own cookie-only auth check, bypassing the middleware's Bearer token support. The thin client uses Bearer tokens (from the `.cookie` file), not session cookies. `HandleWS` must be fixed to accept Bearer tokens, or it will reject the thin client's WebSocket connection even though the middleware already authenticated it.

**Primary recommendation:** Implement Phase 2 as a new `internal/gpgsign/` package for the thin client, with surgical modifications to the approval and api packages to: (a) add Unix socket listening to the server, (b) fix WebSocket auth for Bearer tokens, (c) extend the approval Request to carry real gpg output, and (d) wire real gpg invocation into the approve flow.

## Standard Stack

### Core
| Library | Version | Purpose | Why Standard |
|---------|---------|---------|--------------|
| `github.com/coder/websocket` | v1.8.14 (already in go.mod) | WebSocket client in thin client | Already used for server side; supports custom HTTP client for Unix socket |
| `net/http` stdlib | Go 1.25.6 | HTTP-over-Unix-socket client | `http.Transport` with custom `DialContext` enables Unix socket transport |
| `os/exec` stdlib | Go 1.25.6 | Run real gpg subprocess | Standard; pipe stdin/stdout/stderr |
| `net` stdlib | Go 1.25.6 | Unix socket server listener | `net.Listen("unix", path)` |

### Supporting
| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| `bufio` stdlib | - | Parse commit object headers line-by-line | Used in ParseCommitObject |
| `os` stdlib | - | os.Executable() for self-detection | Skip self when scanning PATH for real gpg |
| `path/filepath` stdlib | - | XDG path construction | Computing socket paths |

### Alternatives Considered
| Instead of | Could Use | Tradeoff |
|------------|-----------|----------|
| Custom Unix socket transport | gorilla/websocket native Unix support | coder/websocket DialOptions.HTTPClient accepts custom transport — no alternative library needed |
| Shell wrapper for setup | Symlink or busybox-style argv0 dispatch | Shell wrapper is simplest; symlink requires either a separate binary on disk or argv0 sniffing in main() |

**Installation:** No new dependencies required. All needed packages are either already in go.mod or stdlib.

## Architecture Patterns

### Recommended Project Structure
```
internal/gpgsign/           # thin client (new package)
├── run.go                  # Run() — entry point for gpg-sign subcommand
├── setup.go                # SetupGitConfig() — writes wrapper, sets gpg.program
├── commit.go               # ParseCommitObject() — extract fields from stdin
├── daemon.go               # DaemonClient — Unix socket HTTP + WebSocket
└── gpg.go                  # FindRealGPG() — PATH scan skipping self

internal/approval/
├── gpgsign.go              # MODIFY: add CommitObject field to GPGSignInfo
└── manager.go              # MODIFY: add gpgStatus []byte + gpgExitCode int to Request

internal/api/
├── server.go               # MODIFY: add Unix socket listener
├── auth.go                 # MODIFY: add ValidateRequest() for cookie OR Bearer
├── websocket.go            # MODIFY: fix HandleWS auth; add GPGStatus to WSMessage; send real signature
└── handlers.go             # MODIFY: HandleApprove calls real gpg for gpg_sign requests

main.go                     # MODIFY: add "gpg-sign" subcommand; pass Unix socket path to server
```

### Pattern 1: Git's GPG Program Invocation Interface

**What:** Git calls `gpg.program` as: `<program> --status-fd=2 -bsau <keyID>`, feeding the raw commit object to stdin. It reads the PGP signature from stdout and status lines from stderr (because `--status-fd=2` redirects status to fd 2 = stderr).

**Verified by:** Live test with fake gpg script capturing args and stdin.

**Exact invocation observed:**
```
ARGS: --status-fd=2 -bsau 846FFFEFC1039264
```

**Stdin format (commit object):**
```
tree 8754a964a0ce1b6c5f7a88202174955bdcd58a98
author Alice Example <alice@example.com> 1771936651 +0200
committer Alice Example <alice@example.com> 1771936651 +0200

feat: add files
```

For non-root commits, a `parent <sha>` line appears after `tree`, before `author`.

**Git exit behavior:** If `gpg.program` exits non-zero, git prints `error: gpg failed to sign the data` and exits with code 128 (regardless of gpg's specific exit code). Git does NOT propagate gpg's exact exit code; it only cares whether gpg succeeded.

**Stdout expected:** ASCII-armored PGP signature:
```
-----BEGIN PGP SIGNATURE-----
...base64...
-----END PGP SIGNATURE-----
```

**Stderr (status-fd=2) from real gpg:**
```
[GNUPG:] KEY_CONSIDERED <fingerprint> 2
[GNUPG:] BEGIN_SIGNING H10
[GNUPG:] SIG_CREATED D 1 10 00 <timestamp> <fingerprint>
```

### Pattern 2: HTTP-over-Unix-Socket Client

**What:** The thin client connects to the daemon's Unix socket using a custom `http.Transport`.

**When to use:** Thin client connecting to `$XDG_RUNTIME_DIR/secrets-dispatcher/api.sock`.

**Example:**
```go
// Source: stdlib net/http documentation
transport := &http.Transport{
    DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
        return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
    },
}
httpClient := &http.Client{Transport: transport}

// For WebSocket (coder/websocket supports custom HTTPClient):
conn, _, err := websocket.Dial(ctx, "ws://localhost/api/v1/ws", &websocket.DialOptions{
    HTTPClient: httpClient,
    HTTPHeader: http.Header{
        "Authorization": {"Bearer " + token},
    },
})
```

The URL hostname is ignored when using a custom transport; `"ws://localhost/api/v1/ws"` is conventional.

### Pattern 3: Unix Socket Server Listener

**What:** Add a second `net.Listener` to the existing HTTP server for Unix socket access.

**When to use:** Daemon startup — serve the same HTTP mux over both TCP (for browser) and Unix socket (for thin client).

**Example:**
```go
// In server.go, alongside existing TCP listener:
unixListener, err := net.Listen("unix", socketPath)
if err != nil {
    return nil, fmt.Errorf("listen unix %s: %w", socketPath, err)
}
// Restrict permissions: only owner can connect
os.Chmod(socketPath, 0600)

// The existing http.Server can serve multiple listeners:
go httpServer.Serve(unixListener)
go httpServer.Serve(tcpListener)
```

The socket directory (`$XDG_RUNTIME_DIR/secrets-dispatcher/`) already exists (it holds D-Bus socket files). The file itself needs `0600` permissions.

### Pattern 4: Finding Real GPG (Skipping Self)

**What:** Daemon must find the real `gpg` binary, not the `secrets-dispatcher` binary itself.

**When to use:** On approval of a `gpg_sign` request, before spawning the gpg subprocess.

**Example:**
```go
// Source: os, os/exec stdlib
func FindRealGPG() (string, error) {
    self, err := os.Executable()
    if err != nil {
        return "", err
    }
    selfInfo, err := os.Stat(self)
    if err != nil {
        return "", err
    }

    pathDirs := filepath.SplitList(os.Getenv("PATH"))
    for _, dir := range pathDirs {
        candidate := filepath.Join(dir, "gpg")
        info, err := os.Stat(candidate)
        if err != nil {
            continue
        }
        if os.SameFile(selfInfo, info) {
            continue // skip self
        }
        return candidate, nil
    }
    return "", errors.New("gpg not found in PATH")
}
```

`os.SameFile` uses inode comparison (works across symlinks correctly).

### Pattern 5: Commit Object Parsing

**What:** Parse the raw commit object from stdin to extract fields for `GPGSignInfo`.

**Header format:** one `key value` pair per line until blank line; body is the commit message.

**Example:**
```go
// Source: verified empirically
func ParseCommitObject(data []byte) (author, committer, message, parentHash string) {
    scanner := bufio.NewScanner(bytes.NewReader(data))
    var bodyLines []string
    inBody := false
    for scanner.Scan() {
        line := scanner.Text()
        if inBody {
            bodyLines = append(bodyLines, line)
            continue
        }
        if line == "" {
            inBody = true
            continue
        }
        if strings.HasPrefix(line, "author ") {
            author = strings.TrimPrefix(line, "author ")
        } else if strings.HasPrefix(line, "committer ") {
            committer = strings.TrimPrefix(line, "committer ")
        } else if strings.HasPrefix(line, "parent ") {
            parentHash = strings.TrimPrefix(line, "parent ")
        }
    }
    message = strings.Join(bodyLines, "\n")
    return
}
```

Note: `tree` line is present but not needed by `GPGSignInfo`. Multiple `parent` lines occur for merge commits — last one wins (or all can be collected, but single parent hash is sufficient for context display).

### Pattern 6: WebSocket Wait-for-Event (Thin Client)

**What:** Thin client opens WebSocket, POSTs request, waits for matching `request_resolved` event.

**When to use:** Main loop of the `gpg-sign` subcommand after posting the signing request.

**Example:**
```go
// Connect WebSocket FIRST (before POST) to avoid race
conn, _, err := websocket.Dial(ctx, wsURL, opts)

// POST signing request, get request ID
reqID := postSigningRequest(...)

// Read WebSocket events until matching request_resolved
for {
    _, data, err := conn.Read(ctx)
    // handle error, context timeout
    var msg WSMessage
    json.Unmarshal(data, &msg)
    switch msg.Type {
    case "request_resolved":
        if msg.ID != reqID {
            continue // different request
        }
        switch msg.Result {
        case "approved":
            // decode signature, write to stdout; write GPGStatus to stderr; exit 0
        case "denied":
            fmt.Fprintln(os.Stderr, "secrets-dispatcher: signing request denied by user")
            os.Exit(1)
        }
    case "request_expired":
        if msg.ID == reqID {
            fmt.Fprintln(os.Stderr, "secrets-dispatcher: signing request timed out")
            os.Exit(1)
        }
    }
}
```

### Anti-Patterns to Avoid
- **Passing gpg args verbatim to daemon:** CONTEXT says "args are parsed and reconstructed" — the daemon always rebuilds `--status-fd=2 -bsau <keyID>` from the stored `KeyID`, never stores a raw argv.
- **Bearer-only WebSocket auth:** `HandleWS` currently does cookie-only check. Must add Bearer token support before thin client can connect.
- **Setting `commit.gpgsign=true` in setup:** Explicitly out of scope per CONTEXT — setup only writes `gpg.program`.
- **Falling back to real gpg on daemon unreachable:** Explicitly forbidden in success criteria.

## Don't Hand-Roll

| Problem | Don't Build | Use Instead | Why |
|---------|-------------|-------------|-----|
| PGP signature format | Custom PGP output formatting | Real gpg subprocess | gpg produces the exact bytes git expects; any custom implementation will fail git verify-commit |
| Commit object format parsing | Custom git object parser | Simple line scanner | The format is simple (one-header-per-line then blank then body) — stdlib `bufio.Scanner` is sufficient |
| HTTP-over-Unix transport | Custom socket dialer | `http.Transport.DialContext` | stdlib covers this cleanly in 5 lines |
| JWT for thin client auth | Custom token scheme | Existing Bearer token from `.cookie` file | The auth system already supports Bearer tokens in the middleware; thin client reads `.cookie` |
| WebSocket event demultiplexing | Custom event bus | Simple for-loop with ID matching | Only one outstanding request per thin client invocation |

**Key insight:** The gpg subprocess IS the PGP engine. The daemon is a proxy — it stores the commit object, calls real gpg on approval, and relays the output. Do not try to produce PGP signatures without invoking real gpg.

## Common Pitfalls

### Pitfall 1: git Does Not Shell-Split gpg.program
**What goes wrong:** Setting `gpg.program` to `"secrets-dispatcher gpg-sign"` (with space) causes git to try to exec a binary with a literal space in its filename, which fails immediately.
**Why it happens:** Git uses `execvp` (or equivalent), not a shell — the value is the binary path, not a shell command.
**How to avoid:** The `gpg-sign setup` command must write a shell wrapper script (no spaces in its path) that calls `exec secrets-dispatcher gpg-sign "$@"`. Set `gpg.program` to the wrapper's path.
**Warning signs:** `fatal: cannot exec '/path/to/secrets-dispatcher gpg-sign': No such file or directory`

### Pitfall 2: WebSocket Auth Rejects Bearer Tokens
**What goes wrong:** The thin client authenticates with a Bearer token (from `.cookie` file), but `HandleWS` calls `ValidateSession` which only checks the session cookie. Even though `auth.Middleware` already validated the Bearer token and let the request through, `HandleWS` performs a second cookie-only check and returns 401.
**Why it happens:** `HandleWS` has its own auth guard that bypasses the middleware's Bearer support.
**How to avoid:** Add `auth.ValidateRequest(r *http.Request) bool` that checks cookie OR Bearer header (same logic as `Middleware`). Replace `ValidateSession` call in `HandleWS` with `ValidateRequest`.
**Warning signs:** WebSocket dial returns 401 even though API calls with the same Bearer token work.

### Pitfall 3: Race Between POST and WebSocket Event
**What goes wrong:** If the thin client POSTs the signing request before opening the WebSocket, the user might approve it so fast that the `request_resolved` WebSocket event fires before the thin client subscribed — and the thin client hangs forever waiting for an event it missed.
**Why it happens:** POST creates the request immediately; if WebSocket subscribes after POST, there's a window where the event is missed.
**How to avoid:** Open WebSocket FIRST, then POST. This is already a locked decision in CONTEXT.md. The WebSocket snapshot on connect only shows pending requests, not already-resolved ones.
**Warning signs:** Thin client hangs indefinitely after a very fast approval.

### Pitfall 4: CommitObject Field Missing from GPGSignInfo
**What goes wrong:** The daemon cannot call real gpg without the commit object bytes — they are not stored anywhere in the current `GPGSignInfo` struct.
**Why it happens:** Phase 1 didn't need the commit object (placeholder signature); Phase 2 does.
**How to avoid:** Add `CommitObject string` field to `GPGSignInfo` before implementing the thin client. The thin client reads stdin, stores in this field, and POSTs it. The daemon reads it from `req.GPGSignInfo.CommitObject` on approval.
**Warning signs:** Compilation error trying to access commit object on the stored request.

### Pitfall 5: Unix Socket Cleanup on Restart
**What goes wrong:** If the daemon crashes without cleaning up `api.sock`, the next start fails with "address already in use".
**Why it happens:** Unix domain sockets leave a filesystem artifact; `net.Listen("unix", path)` fails if the file already exists.
**How to avoid:** Before `net.Listen("unix", path)`, call `os.Remove(socketPath)` (ignore error). This is the standard pattern.
**Warning signs:** `listen unix /run/user/.../api.sock: bind: address already in use`

### Pitfall 6: GPG Status Output Goes to stderr, Not stdout
**What goes wrong:** `--status-fd=2` means gpg writes status lines to file descriptor 2 (stderr). If the subprocess captures combined stdout+stderr, the signature bytes and status lines get mixed together, corrupting the signature.
**Why it happens:** `--status-fd=2` is a gpg convention to separate machine-readable status from human-readable output.
**How to avoid:** Use separate `cmd.Stdout` and `cmd.Stderr` pipes. Stdout = signature, Stderr = status lines.
**Warning signs:** git rejects the signature with "no valid OpenPGP data found"; the signature contains mixed content.

### Pitfall 7: Thin Client Connecting to Wrong Endpoint
**What goes wrong:** The existing server route `/api/v1/gpg-sign/request` is protected by `auth.Middleware`. But the WebSocket endpoint `/api/v1/ws` also requires auth. The thin client must send `Authorization: Bearer <token>` on BOTH the POST and the WebSocket upgrade request.
**Why it happens:** Easy to forget auth on the WebSocket upgrade since it's a different request type.
**How to avoid:** Set `HTTPHeader: http.Header{"Authorization": {"Bearer " + token}}` in `websocket.DialOptions`. Verify the cookie file is readable and not empty before attempting connection.

## Code Examples

Verified patterns from official sources and live tests:

### Real GPG Invocation (Daemon Side)
```go
// Source: verified empirically — git sends --status-fd=2 -bsau <keyID>
func runRealGPG(gpgPath, keyID string, commitObject []byte) (signature []byte, status []byte, err error) {
    cmd := exec.Command(gpgPath, "--status-fd=2", "-bsau", keyID)
    cmd.Stdin = bytes.NewReader(commitObject)
    var sigBuf, statusBuf bytes.Buffer
    cmd.Stdout = &sigBuf
    cmd.Stderr = &statusBuf
    if err := cmd.Run(); err != nil {
        return nil, statusBuf.Bytes(), fmt.Errorf("gpg exited %w", err)
    }
    return sigBuf.Bytes(), statusBuf.Bytes(), nil
}
```

### Thin Client Main Loop
```go
// SIGN-01: intercept git's gpg call, block until approved or denied
func Run(args []string, stdin io.Reader) int {
    // Parse: --status-fd=2 -bsau <keyID>
    keyID := extractKeyID(args) // from -u or -bsau flag

    // Read commit object from stdin (SIGN-02)
    commitBytes, _ := io.ReadAll(stdin)

    // Collect git context (SIGN-03, SIGN-04)
    repoRoot, _ := gitRevParseTopLevel()
    repoName := filepath.Base(repoRoot)
    changedFiles, _ := gitDiffCachedNames()
    author, committer, message, parentHash := ParseCommitObject(commitBytes)

    // Load auth token (ERR-01 if missing → daemon not running)
    token, err := loadAuthToken() // reads $XDG_STATE_HOME/secrets-dispatcher/.cookie
    if err != nil {
        fmt.Fprintln(os.Stderr, "secrets-dispatcher: daemon unreachable ...")
        return 2
    }

    // Connect via Unix socket
    socketPath := unixSocketPath() // $XDG_RUNTIME_DIR/secrets-dispatcher/api.sock
    transport := unixTransport(socketPath)
    httpClient := &http.Client{Transport: transport}

    ctx := context.Background()

    // WebSocket FIRST (CONTEXT decision: ensures no missed events)
    wsConn, err := dialWebSocket(ctx, httpClient, token)
    if err != nil {
        fmt.Fprintf(os.Stderr, "secrets-dispatcher: daemon unreachable at %s. Is secrets-dispatcher running?\n", socketPath)
        return 2
    }
    defer wsConn.Close(websocket.StatusNormalClosure, "")

    // POST signing request (SIGN-05)
    reqID, err := postSigningRequest(ctx, httpClient, token, &GPGSignInfo{
        RepoName: repoName, CommitMsg: message, Author: author,
        Committer: committer, KeyID: keyID, ChangedFiles: changedFiles,
        ParentHash: parentHash, CommitObject: string(commitBytes),
    })
    if err != nil {
        fmt.Fprintf(os.Stderr, "secrets-dispatcher: failed to send request: %v\n", err)
        return 2
    }

    // Wait for resolution (SIGN-08, ERR-02)
    return waitForResolution(ctx, wsConn, reqID)
}
```

### gpg-sign setup (Wrapper Script Generation)
```go
// SetupGitConfig writes a wrapper script and configures git's gpg.program.
// The wrapper is needed because git's gpg.program cannot contain spaces.
func SetupGitConfig(scope string) error {
    self, err := os.Executable()
    if err != nil {
        return fmt.Errorf("find executable: %w", err)
    }

    // Write shell wrapper to ~/.local/bin/secrets-dispatcher-gpg
    wrapperDir := filepath.Join(xdgDataHome(), "bin") // or ~/.local/bin
    wrapperPath := filepath.Join(wrapperDir, "secrets-dispatcher-gpg")

    if err := os.MkdirAll(wrapperDir, 0755); err != nil {
        return err
    }
    content := fmt.Sprintf("#!/bin/sh\nexec %s gpg-sign \"$@\"\n", self)
    if err := os.WriteFile(wrapperPath, []byte(content), 0755); err != nil {
        return err
    }

    // Configure git
    gitArgs := []string{"config"}
    if scope == "local" {
        gitArgs = append(gitArgs, "--local")
    } else {
        gitArgs = append(gitArgs, "--global")
    }
    gitArgs = append(gitArgs, "gpg.program", wrapperPath)
    return exec.Command("git", gitArgs...).Run()
}
```

### Unix Socket Server (Daemon)
```go
// In Server.Start() or Server initialization:
socketPath := filepath.Join(runtimeDir, "secrets-dispatcher", "api.sock")
os.Remove(socketPath) // clean up stale socket
unixListener, err := net.Listen("unix", socketPath)
if err != nil {
    return fmt.Errorf("listen unix socket: %w", err)
}
os.Chmod(socketPath, 0600) // owner only
go s.httpServer.Serve(unixListener) // same mux as TCP
```

### WebSocket Auth Fix (HandleWS)
```go
// In auth.go — add combined validator:
func (a *Auth) ValidateRequest(r *http.Request) bool {
    // Check session cookie first
    if a.ValidateSession(r) {
        return true
    }
    // Fall back to Bearer token
    parts := strings.SplitN(r.Header.Get("Authorization"), " ", 2)
    if len(parts) == 2 && parts[0] == "Bearer" {
        return subtle.ConstantTimeCompare([]byte(parts[1]), []byte(a.token)) == 1
    }
    return false
}

// In websocket.go HandleWS — replace ValidateSession with ValidateRequest:
if !h.auth.ValidateRequest(r) {
    http.Error(w, `{"error": "unauthorized"}`, http.StatusUnauthorized)
    return
}
```

### Approve with Real GPG Signature
```go
// In handlers.go HandleApprove — intercept gpg_sign requests:
func (h *Handlers) HandleApprove(w http.ResponseWriter, r *http.Request) {
    // ... existing method check and ID extraction ...

    // Look up the request type before approving
    req := h.manager.GetPending(id) // need new getter on Manager
    if req != nil && req.Type == approval.RequestTypeGPGSign {
        // Call real gpg daemon-side (SIGN-07)
        gpgPath, err := h.gpgFinder.FindRealGPG()
        sig, status, err := runRealGPG(gpgPath, req.GPGSignInfo.KeyID, []byte(req.GPGSignInfo.CommitObject))
        if err != nil {
            // ERR-02: gpg exit code propagated — store on request, then notify via WebSocket
            h.manager.ApproveGPGFailed(id, status, gpgExitCode(err))
            writeJSON(w, ActionResponse{Status: "gpg_failed"})
            return
        }
        h.manager.ApproveWithSignature(id, sig, status)
    } else {
        h.manager.Approve(id)
    }
    writeJSON(w, ActionResponse{Status: "approved"})
}
```

### GPGSignInfo with CommitObject (New Field)
```go
// In internal/approval/gpgsign.go:
type GPGSignInfo struct {
    RepoName     string   `json:"repo_name"`
    CommitMsg    string   `json:"commit_msg"`
    Author       string   `json:"author"`
    Committer    string   `json:"committer"`
    KeyID        string   `json:"key_id"`
    Fingerprint  string   `json:"fingerprint,omitempty"`
    ChangedFiles []string `json:"changed_files"`
    ParentHash   string   `json:"parent_hash,omitempty"`
    // CommitObject is the raw commit object bytes (UTF-8 text) fed to gpg's stdin.
    // Phase 2 adds this; Phase 1 tests omit it (daemon will fail to sign without it).
    CommitObject string   `json:"commit_object,omitempty"`
}
```

### WSMessage Extensions (New Fields)
```go
// In api/websocket.go WSMessage struct:
type WSMessage struct {
    // ... existing fields ...

    // Signature carries base64-encoded signature bytes for gpg_sign request_resolved events.
    Signature string `json:"signature,omitempty"`
    // GPGStatus carries the raw [GNUPG:] status lines from gpg --status-fd=2.
    // Written to thin client's stderr so git can parse gpg status.
    GPGStatus string `json:"gpg_status,omitempty"`
    // ExitCode carries gpg's exit code for failed signing attempts (result = "gpg_failed").
    ExitCode int `json:"exit_code,omitempty"`
}
```

## State of the Art

| Old Approach | Current Approach | When Changed | Impact |
|--------------|------------------|--------------|--------|
| Placeholder signature in WebSocket | Real gpg output via ApproveWithSignature | Phase 2 | `git verify-commit HEAD` will actually work |
| TCP-only server | TCP + Unix socket listener | Phase 2 | Thin client can connect without network |
| Cookie-only WebSocket auth | Cookie OR Bearer WebSocket auth | Phase 2 | CLI tools can authenticate without browser cookie |

**Deprecated/outdated (in the codebase after Phase 2):**
- `PLACEHOLDER_SIGNATURE` literal in `websocket.go` `OnEvent` — replaced with real `req.signature`
- Comment "Phase 2 will wire real gpg output; Phase 1 leaves it unset" on `signature` field

## Open Questions

1. **Where to put the generated shell wrapper**
   - What we know: must be a single path (no spaces), must be writable by user, must be in PATH for git to find it easily
   - What's unclear: best default location — `~/.local/bin/` requires it to be in PATH; an absolute path works regardless
   - Recommendation: Default to `~/.local/bin/secrets-dispatcher-gpg`, print the path so user can verify it's in their PATH. Do NOT add to PATH automatically.

2. **Manager.GetPending() method needed**
   - What we know: `HandleApprove` needs to look up the request type before calling real gpg
   - What's unclear: whether to add a new exported `GetPending(id string) *Request` on Manager, or restructure HandleApprove differently
   - Recommendation: Add `GetPending(id string) *Request` (with read lock). It's a small, clean addition. The planner should assign this as a distinct task.

3. **Multiple parent hashes (merge commits)**
   - What we know: merge commits have multiple `parent` lines in the commit object; ParseCommitObject sees them all
   - What's unclear: should all parent hashes be stored or just the first?
   - Recommendation: Store just the first parent hash in `GPGSignInfo.ParentHash` — it's display-only context, not security-critical. For merge commits with 2+ parents, this is "first parent hash" (conventional).

4. **Worktree behavior for git diff --cached**
   - What we know: STATE.md flagged "Confirm worktree behavior (GIT_DIR + GIT_WORK_TREE) for changed-files collection via git diff --cached in worktree contexts"
   - What's unclear: whether `git diff --cached --name-only` works correctly from inside a worktree
   - Recommendation: Test explicitly during implementation. If it fails, fall back to empty changed-files list and log a debug warning. ChangedFiles is display context, not required for signing.

## Sources

### Primary (HIGH confidence)
- Live test: fake gpg script capturing args/stdin — git invocation format confirmed (`--status-fd=2 -bsau <keyID>`, raw commit object on stdin)
- Live test: multi-word gpg.program — confirmed fails with `cannot exec` error
- Live test: real gpg signing — confirmed stdout=signature, stderr=status lines format
- stdlib `net/http`, `os/exec`, `bufio` — standard Go stdlib, well-documented
- `go env GOMODCACHE` inspection of coder/websocket v1.8.14 — `DialOptions.HTTPClient` supports custom transport
- Existing codebase inspection — auth.Middleware already handles Bearer tokens; HandleWS does NOT

### Secondary (MEDIUM confidence)
- `man git-config` output for `gpg.program` documentation — confirmed format specification
- `os.SameFile` for self-detection — documented stdlib behavior, standard pattern

### Tertiary (LOW confidence)
- Shell wrapper as default location `~/.local/bin/` — reasonable convention but XDG_BIN_HOME could differ

## Metadata

**Confidence breakdown:**
- Standard stack: HIGH — all libraries already in go.mod or stdlib; coder/websocket source inspected directly
- Architecture: HIGH — commit object format verified empirically; auth gaps confirmed by code reading
- Pitfalls: HIGH for pitfalls 1-4 (verified by test or code); MEDIUM for 5-7 (standard patterns)

**Research date:** 2026-02-24
**Valid until:** 2026-05-24 (stable domain; git gpg.program interface is not changing)
