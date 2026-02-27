# Phase 6: Desktop Integration - Context

**Gathered:** 2026-02-27
**Status:** Ready for planning

<domain>
## Phase Boundary

Desktop applications transparently use the companion's secret store via a D-Bus proxy on the desktop session bus; git commits trigger GPG signing via a thin client that routes through the companion's session bus; PAM/systemd manage companion lifecycle automatically. Notifications and desktop awareness UX are deferred to a later plan or phase — this phase focuses on the minimal working flow.

</domain>

<decisions>
## Implementation Decisions

### System bus → session bus migration
- Phase 5's system bus interface (RequestSecret, RequestSign) is **deprecated**
- Companion daemon migrates to its own session bus — all user↔companion IPC goes through the companion's session bus
- No system bus involvement for user↔companion communication
- D-Bus socket activation on the companion's session bus starts the daemon on first connection

### Secret Service proxy
- Thin D-Bus forwarder running as desktop user, claims `org.freedesktop.secrets` on desktop session bus
- Forwards Secret Service API calls directly to companion's session bus (not via system bus)
- **Practical subset** of freedesktop Secret Service spec — what libsecret/Chrome/Firefox actually call (OpenSession plain, SearchItems, GetSecrets, CreateItem), not full spec
- **Backend-agnostic** — works with any Secret Service implementation on the companion side, not committed to gopass-secret-service specifically
- VT approval required for secret requests (same as GPG signing)
- Existing pipeline architecture (upstream → filter → downstream) handles both proxy and VT-approval cases via configuration

### Companion session bus access
- Proxy connects to companion user's session bus directly
- Permissions on companion's session bus socket adjusted to allow the corresponding desktop user
- D-Bus socket activation handles first-connection transparently — no retry logic in proxy

### GPG thin client
- Signing-only scope: handles --sign, --detach-sign, --status-fd, --armor, and key selection flags git passes during commit/tag signing
- Connects to companion's session bus (same path as Secret Service proxy)
- Stderr status line while blocking: "Waiting for approval on VT8..."
- Non-zero exit + stderr message on failure (companion not running, approval timeout, denial)
- Does not mimic gpg status-fd format — simple exit code + human-readable error

### PAM lifecycle
- **Start**: Socket activation via systemd — companion starts on first D-Bus connection to its session bus. No explicit PAM start hook needed (linger already enabled in Phase 4)
- **Stop**: systemd-logind dependency — companion service stops automatically when desktop user's last session ends
- D-Bus socket activation eliminates startup race conditions entirely

### Desktop notifications
- **Deferred** — not part of minimal flow
- Silent block for secret requests (desktop app waits until VT approval)
- Pending request awareness (monitoring companion state, notifying desktop user) is a separate concern from the proxy
- Companion-not-running: proxy returns D-Bus error immediately, no notification

### Claude's Discretion
- Companion session bus socket permission mechanism (group-based ACL, bind-mount, etc.)
- Secret Service method mapping details (which D-Bus methods/signals to implement)
- systemd-logind watcher implementation for companion stop
- D-Bus socket activation service file format
- How the pipeline configuration distinguishes proxy mode vs VT-approval mode
- Integration test strategy for cross-user session bus communication

</decisions>

<specifics>
## Specific Ideas

- The pipeline architecture (upstream → filter → downstream) is the unifying pattern: both the proxy and the companion daemon are instances of the same pipeline with different configurations
- Socket activation + logind dependency = fully declarative lifecycle, no custom session-counting logic
- The proxy is deliberately minimal — it's a D-Bus bridge, not a secret manager

</specifics>

<deferred>
## Deferred Ideas

- Desktop notifications for pending VT approval — later plan within Phase 6 or Phase 7
- Notification action button "Review on VT" triggering chvt — tracked as LIFE-02 in future requirements
- Broader gpg shim (verify, encrypt, decrypt) — only if other tools besides git need it
- Full Secret Service spec compliance (encrypted transport, prompts API, locking) — only if practical subset proves insufficient

</deferred>

---

*Phase: 06-desktop-integration*
*Context gathered: 2026-02-27*
