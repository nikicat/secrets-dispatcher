# Technology Stack: GPG Signing Proxy Milestone

**Project:** secrets-dispatcher — GPG commit signing approval
**Researched:** 2026-02-24
**Scope:** New dependencies and tooling for the GPG milestone only. Existing stack (Go stdlib, coder/websocket, godbus/dbus, google/uuid, fsnotify, tint, yaml.v3) is already in place and not re-documented here.

---

## Summary Recommendation

**No new Go dependencies are needed.** The GPG signing feature is implemented entirely with the Go standard library plus the already-present dependencies. The only external tool is the system `gpg` binary, which is an existing runtime requirement (users must already have GPG for commit signing). The thin client uses existing HTTP + auth patterns; commit object parsing is simple text parsing; gpg invocation uses `os/exec`.

---

## Core Framework

The existing stack handles all needs:

| Component | Existing Dependency | Handles |
|-----------|---------------------|---------|
| HTTP client (gpg-sign) | `net/http` (stdlib) | POST to daemon |
| HTTP server (daemon endpoint) | `net/http` (stdlib) | Handle `/api/v1/gpg-sign/request` |
| Auth (gpg-sign client) | `internal/api` auth package | Load token from state dir |
| GPG invocation | `os/exec` (stdlib) | Exec real gpg with args + stdin |
| Commit object parsing | stdlib `bufio`, `bytes`, `strings` | Parse raw text format from stdin |

---

## The git `gpg.program` Interface

**Confidence: HIGH — verified against git source code (gpg-interface.c)**

When git calls `gpg.program` to sign a commit, the exact invocation is:

```
<gpg.program> --status-fd=2 -bsau <key-id>
```

Where the flags decompose as:
- `-b` — detached signature (not inline)
- `-s` — sign
- `-a` — ASCII armor
- `-u <key-id>` — key to use

**stdin:** Raw commit object bytes (the payload to be signed). This is the exact byte sequence git would have stored as the commit object, without the `gpgsig` header.

**stdout:** The ASCII-armored PGP detached signature. Git reads this and embeds it as the `gpgsig` header in the final commit object.

**stderr:** GPG status lines (because of `--status-fd=2`). Git scans stderr for the literal string `[GNUPG:] SIG_CREATED ` to verify the signing succeeded. The `gpg-sign` client must forward real gpg's stderr to its own stderr so git gets these status lines.

**Success detection:** git searches stderr for `[GNUPG:] SIG_CREATED `. If absent, git reports "gpg failed to sign the data" and aborts the commit.

### Implication for the thin client

The `gpg-sign` subcommand receives these exact args from git. It does NOT invoke gpg itself. It:
1. Reads the args to extract `<key-id>`
2. Reads commit bytes from stdin
3. Parses context from the commit bytes
4. POSTs to the daemon, blocks for approval
5. On success: writes signature to stdout, status lines to stderr, exits 0
6. On denial/timeout: exits non-zero (git sees gpg failure, aborts commit)

### Implication for the daemon

The daemon invokes real gpg with the same args it received from the client:

```go
cmd := exec.CommandContext(ctx, "gpg", "--status-fd=2", "-bsau", keyID)
cmd.Stdin = bytes.NewReader(commitObject)
var sigBuf, statusBuf bytes.Buffer
cmd.Stdout = &sigBuf   // ASCII-armored signature
cmd.Stderr = &statusBuf // [GNUPG:] SIG_CREATED etc.
if err := cmd.Run(); err != nil {
    // return HTTP 500
}
// return sigBuf.Bytes() + statusBuf.String() in response
```

The daemon must NOT add `--batch` or `--no-tty`, which would suppress pinentry. The daemon runs as the same user with the same `GPG_AGENT_INFO` / `GNUPGHOME` / `GPG_TTY` environment, so gpg-agent and pinentry work normally.

---

## Commit Object Parsing

**Confidence: HIGH — verified against git documentation (gitformat-signature) and go-git source**

### Do NOT use go-git for this

go-git v5 (v5.16.5, Feb 2026) has excellent commit parsing but requires wrapping raw bytes in a `plumbing.EncodedObject` interface. The raw bytes from stdin do not come from a git repository object store, making go-git's `Commit.Decode()` awkward to use without implementing the storer interface. More importantly, go-git is a large dependency (imports 57 packages) for parsing 20 lines of text.

**Use stdlib text parsing instead.** The git commit object format is simple and stable.

### Raw commit object format

```
tree <sha1-hex>\n
parent <sha1-hex>\n          (zero or more)
author <name> <email> <unix-ts> <tz-offset>\n
committer <name> <email> <unix-ts> <tz-offset>\n
\n
<commit message>
```

Example (what git passes on stdin to gpg.program):

```
tree eebfed94e75e7760540d1485c740902590a00332
parent 04b871796dc0420f8e7561a895b52484b701d51a
author A U Thor <author@example.com> 1465981137 +0000
committer C O Mitter <committer@example.com> 1465981137 +0000

feat: add GPG signing proxy

More detail here.
```

Note: this is the commit object WITHOUT the `gpgsig` header. Git sends the pre-signature payload to gpg.program, not the final committed object.

### Parsing approach

```go
// internal/gpgsign/parse.go
func ParseCommitObject(data []byte) (*CommitInfo, error) {
    // Use bufio.Scanner line-by-line
    // Headers end at first blank line
    // Remainder is commit message
    // Each header: "key value\n", where key is "tree", "parent", "author", "committer"
    // author/committer format: "Name <email> unixtimestamp tzoffset"
}
```

This is ~50 lines of stdlib code. No external dependency needed.

---

## Changed Files Collection

**Confidence: HIGH — exec approach; MEDIUM — timing constraint**

The `gpg-sign` thin client runs inside `git commit -S`, after git has staged changes but before it writes the commit object. The correct command to get staged files at this moment:

```go
cmd := exec.Command("git", "-C", repoRoot, "diff", "--cached", "--name-only")
```

This is the equivalent of `git diff --cached --name-only` run from the repo root. It lists all files staged for the commit. This is the correct timing: git invokes gpg.program after computing the commit object (which includes the tree hash) but before storing it. The index is fully staged at this point.

**Alternative — diff-tree against parent:** `git diff-tree --no-commit-id -r --name-only <tree-hash>`. This avoids a subprocess by using the tree hash from the parsed commit object. However, it requires git to have stored the tree object, which may or may not be done at the point gpg.program is invoked. The `--cached` approach is safer.

**Repo root detection:** Walk up from `os.Getwd()` looking for `.git/` directory, or parse `git rev-parse --show-toplevel`. The git command approach is one line and reliable:

```go
cmd := exec.Command("git", "rev-parse", "--show-toplevel")
```

**Repo name:** Use the directory basename of the repo root, or parse `git remote get-url origin` and extract the repo name from the URL. Basename is simpler and always works even without a remote.

---

## GPG Invocation in the Daemon

**Confidence: HIGH — stdlib os/exec**

No PGP library is needed in the daemon. The daemon passes through to real gpg. This is intentional:

- gpg-agent handles key caching and passphrase
- pinentry handles passphrase UI if not cached
- The daemon does not need access to the private key material
- Full gpg compatibility is guaranteed (whatever gpg supports, the proxy supports)

Using a Go PGP library (ProtonMail/go-crypto or golang.org/x/crypto/openpgp) would require the daemon to hold the private key in memory, bypass gpg-agent, and implement passphrase handling. This contradicts the project design (see PROJECT.md "Out of Scope").

**The daemon calls real gpg and relays the result.** That is the entire signing implementation on the daemon side, beyond the approval gate.

---

## What NOT to Use

### golang.org/x/crypto/openpgp — DO NOT USE

- Marked as deprecated by the Go team (redirects to ProtonMail/go-crypto)
- Would require private key in memory — bypasses gpg-agent
- Not needed: daemon shells out to real gpg

### ProtonMail/go-crypto (github.com/ProtonMail/go-crypto) — DO NOT USE for signing

- v1.3.0 (May 2025) is current and well-maintained
- Excellent for OpenPGP operations when you control the keys
- Wrong tool for this use case: requires the private key in memory, does not interoperate with gpg-agent/pinentry
- Not needed: daemon shells out to real gpg
- Would be useful if the project ever needed native Go signing without gpg-agent — flag this for future consideration

### github.com/go-git/go-git/v5 — DO NOT ADD as a dependency

- v5.16.5 (Feb 2026) is current and production-quality
- Well-suited for commit object parsing IF you need a full git library
- Overkill for this use case: commit object is plain text; stdlib parsing is 50 lines
- Adds significant dependency weight (57 imported packages)
- Would be justified if the project needed to open git repos, traverse history, etc.
- The gpg-sign use case only needs to parse one commit header block from stdin

### Assuan / gpg-agent protocol libraries — DO NOT USE

- Intercepting at the gpg-agent Assuan protocol level (instead of at the gpg.program level) is significantly more complex
- Requires implementing the Assuan socket protocol
- The gpg.program approach gives full access to the commit object on stdin, which is exactly what is needed
- PROJECT.md already validated this decision

---

## Supporting Libraries (already present, new usage)

These existing dependencies gain new usage patterns in this milestone:

| Library | Existing Use | New Use |
|---------|-------------|---------|
| `os/exec` (stdlib) | None in current code | `gpg-sign`: `git rev-parse`, `git diff --cached`; daemon: real gpg invocation |
| `encoding/base64` (stdlib) | None currently | Encode commit bytes in JSON request body |
| `bytes`, `bufio`, `strings` (stdlib) | Scattered use | Commit object parsing in gpg-sign client |
| `github.com/coder/websocket` | Web UI real-time updates | No new usage — design uses synchronous HTTP instead of WebSocket for signature delivery |

---

## Configuration

One new git configuration per user or per repository:

```ini
# ~/.gitconfig  or per-repo .git/config
[gpg]
    program = /path/to/secrets-dispatcher

[commit]
    gpgsign = true
```

No new daemon configuration is needed. The approval timeout (`serve.timeout`, default 5 min) covers GPG signing requests. If a user wants a shorter timeout for commits specifically, that is a future enhancement.

**Environment variables the daemon must inherit:**

| Variable | Purpose |
|----------|---------|
| `GPG_AGENT_INFO` | gpg-agent socket path (older gpg versions) |
| `GNUPGHOME` | Alternate GPG home directory |
| `GPG_TTY` | Terminal for pinentry |

These are inherited automatically since the daemon runs in the user's session. No action required.

---

## Installation

No new packages to install. The feature is implemented with existing dependencies.

The build produces the same single binary. The user configures git to use it as `gpg.program`.

```bash
# User-side git configuration (not part of the build)
git config --global gpg.program "$(which secrets-dispatcher) gpg-sign"
git config --global commit.gpgsign true
```

Wait — this is not quite right. `gpg.program` must be a path to an executable that git calls directly with the GPG args appended. It cannot be `secrets-dispatcher gpg-sign` (a command with args) because git exec's the path literally. The correct approach is one of:

1. A shell wrapper: `#!/bin/sh\nexec secrets-dispatcher gpg-sign "$@"` installed at e.g. `~/.local/bin/sd-gpg`
2. The binary detects when `$0` basename is not `secrets-dispatcher` (symlink support)
3. A separate installed binary `secrets-dispatcher-gpg` that calls into the same main

**Recommended:** Option 1 (shell wrapper). Simplest, no binary changes. User installs a one-line wrapper script. Document in README.

Alternative: Option 2 — if the binary is symlinked as `sd-gpg`, `filepath.Base(os.Args[0])` equals `sd-gpg`, and main dispatches to gpg-sign mode automatically. This is the cleanest user experience but adds a symlink install step.

**Flag for roadmap:** The install UX for gpg.program needs a decision before Phase 1 implementation.

---

## Sources

- git source: [gpg-interface.c](https://github.com/git/git/blob/master/gpg-interface.c) — exact args and success detection string (HIGH confidence)
- git docs: [gitformat-signature](https://git-scm.com/docs/gitformat-signature) — commit object format before/after signing (HIGH confidence)
- [go-git v5 object package](https://pkg.go.dev/github.com/go-git/go-git/v5/plumbing/object) — Commit struct, Decode method, EncodeWithoutSignature (HIGH confidence)
- [ProtonMail/go-crypto openpgp package](https://pkg.go.dev/github.com/ProtonMail/go-crypto/openpgp) — ArmoredDetachSign signature, private-key-in-memory requirement confirmed (HIGH confidence)
- [ProtonMail/gopenpgp v3](https://pkg.go.dev/github.com/ProtonMail/gopenpgp/v3) — high-level PGP API, same key-in-memory limitation (MEDIUM confidence)
- Existing codebase: `internal/api/`, `internal/approval/`, `internal/cli/`, `main.go` — auth patterns, existing API conventions (HIGH confidence — direct analysis)
