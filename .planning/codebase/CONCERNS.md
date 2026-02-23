# Codebase Concerns

**Analysis Date:** 2026-02-23

## Tech Debt

**Main function complexity:**
- Issue: `runServe()` in `main.go` contains 220 lines of config loading, flag parsing, and initialization logic that should be extracted into separate modules for maintainability.
- Files: `main.go` (lines 242-460)
- Impact: Difficult to test individual components; hard to understand initialization flow; difficult to extend with new configuration options.
- Fix approach: Create a `config` initialization package that handles all the setup orchestration. Extract flag parsing into a dedicated module.

**History list memory footprint:**
- Issue: History entries are stored in memory indefinitely (capped at `historyLimit`, default 100). Each history entry contains full request metadata, items, and sender info. With 100 entries × typical size, this could consume several MB over time.
- Files: `internal/approval/manager.go` (lines 184-194)
- Impact: Long-running processes may accumulate unnecessary memory; no persistence mechanism to archive old history.
- Fix approach: Implement optional persistence layer (e.g., SQLite) for history. Add optional periodic cleanup/archival. Add metrics for history size monitoring.

**WebSocket message buffer management:**
- Issue: WebSocket send channel has a buffer of 256 messages (line 110 in `websocket.go`). Slow clients cause messages to be silently dropped with a warning, but there's no mechanism to disconnect persistently slow clients.
- Files: `internal/api/websocket.go` (lines 110, 198-203)
- Impact: Clients can become out-of-sync with server state; no way to know if critical updates were dropped; no backpressure mechanism.
- Fix approach: Implement connection timeout for clients that cannot keep up. Add metrics tracking dropped messages. Consider per-client flow control or priority queues.

## Known Bugs

**Cookie expiration not set:**
- Symptoms: Session cookies created in `auth.go` do not have an expiration time or MaxAge set. Browser will keep the cookie until shutdown.
- Files: `internal/api/auth.go` (lines 150-157)
- Trigger: Login via JWT, cookie is set without `Expires` or `MaxAge`.
- Workaround: Browser restart clears the session; restart the service to invalidate all tokens.
- Fix approach: Add `Expires` and `MaxAge` to cookie (recommend 24-hour expiry). Add cookie refresh logic to extend expiry on activity.

**Goroutines in approval.notify() not waited for:**
- Symptoms: Observer notification sends events asynchronously in goroutines (line 153 in `manager.go`), but observers may log or do other operations that could fail silently. No guarantee that events are delivered.
- Files: `internal/approval/manager.go` (line 153)
- Trigger: When a request is resolved (approved, denied, expired, cancelled), observers are notified in separate goroutines.
- Workaround: Critical events (approvals) also wait for client response, but history/UI updates may race.
- Fix approach: Switch to synchronous notification or ensure all critical observers run synchronously. Add error handling and telemetry for observer failures.

**Missing read timeout in WebSocket:**
- Symptoms: `readPump()` in `websocket.go` reads from WebSocket without timeout. Stuck client connection won't be detected until OS-level TCP timeout (could be minutes).
- Files: `internal/api/websocket.go` (lines 332)
- Trigger: Network partition or client crash leaves connection hanging.
- Workaround: OS TCP keepalive (OS-dependent, not guaranteed).
- Fix approach: Set read deadline on WebSocket connection. Implement idle timeout (e.g., 5-minute).

## Security Considerations

**Session cookie lacks Secure flag:**
- Risk: Cookie is set with `HttpOnly` and `SameSite: Strict` but missing `Secure` flag. In production over HTTPS, this is acceptable but not enforced.
- Files: `internal/api/auth.go` (lines 150-157)
- Current mitigation: Assumes HTTPS in production (app binds to localhost:8484 by default, but user can expose it).
- Recommendations: Add `Secure: true` to SetSessionCookie to enforce HTTPS. Document that this app must not be exposed over HTTP in production. Add startup warning if listening on non-localhost address.

**Bearer token in URL (login URL generation):**
- Risk: `GenerateLoginURL()` embeds JWT in query parameter (`?token=...`). Query parameters can be logged in browser history, server logs, and forwarded to proxies.
- Files: `internal/api/auth.go` (line 136)
- Current mitigation: Login URL used once to set session cookie; token is short-lived (5 minutes).
- Recommendations: Switch to POST-only authentication endpoint. Use form body instead of query parameter. Document token expiry in help text. Consider one-time-use tokens.

**File permissions for cookie file:**
- Risk: Cookie file created with mode `0600` (line 57 in `auth.go`), which is correct, but only on Unix. No validation on Windows.
- Files: `internal/api/auth.go` (line 57)
- Current mitigation: Linux/Unix only in practice; tested on Linux systems.
- Recommendations: Add validation that cookie file permissions are secure at startup. Warn if permissions are too permissive.

**Approval request details exposed to WebSocket:**
- Risk: Full request metadata (sender PID, UID, command name) sent to all WebSocket clients. If attacker gains WebSocket access, they can see all pending approval requests including who is requesting and what they're accessing.
- Files: `internal/api/websocket.go` (lines 244-249, 358-384)
- Current mitigation: WebSocket requires session cookie/Bearer token (same as other API endpoints).
- Recommendations: Add role-based filtering (e.g., only show requests for user's own client). Add audit logging for WebSocket access. Consider masking sensitive fields.

**No rate limiting on API endpoints:**
- Risk: API endpoints (approve, deny, list) have no rate limiting. Attacker with valid token could brute-force request IDs or DOS the service.
- Files: `internal/api/handlers.go`, `internal/api/server.go`
- Current mitigation: Request IDs are UUIDs (not sequential); unlikely to brute-force. Default listen address is localhost.
- Recommendations: Add rate limiting per token/IP. Add request ID validation to reject obviously invalid UUIDs early. Document that app expects to run on localhost only.

## Performance Bottlenecks

**History list prepend pattern causes O(n) allocations:**
- Problem: History entries are prepended using `append([]HistoryEntry{entry}, m.history...)` (line 188 in `manager.go`), which allocates a new slice and copies all existing entries on every append. With 100 history entries, this is ~10,000 copy operations per approval decision.
- Files: `internal/approval/manager.go` (lines 188-189, 208-209)
- Cause: Go slice prepend requires reallocation and copy; using append backwards is inefficient.
- Improvement path: Use a circular buffer or deque data structure. Alternatively, append to end and reverse when reading. Measure actual impact with benchmarks.

**WebSocket broadcast to all clients on every event:**
- Problem: `broadcast()` in `websocket.go` marshals message to JSON and sends to all connected clients even if client is slow/disconnected. No prioritization or batching.
- Files: `internal/api/websocket.go` (lines 413-430)
- Cause: Event-driven architecture broadcasts immediately to all clients; no buffering or message coalescing.
- Improvement path: Batch updates into 100ms windows before sending. Add per-client priority (critical vs. informational events). Measure actual impact on a system with many clients.

**No connection pooling for D-Bus:**
- Problem: Each proxy creates new D-Bus connections per request. With many secrets accessed, this could create connection overhead.
- Files: `internal/proxy/proxy.go` (lines 72-82)
- Cause: One-to-one mapping between proxy and D-Bus connection; no connection reuse across requests.
- Improvement path: This is likely acceptable for typical usage (1-2 proxies, ~5-10 secrets per session), but could be optimized with connection pooling if profiling shows bottleneck.

## Fragile Areas

**Proxy connection error recovery:**
- Files: `internal/proxy/manager.go` (lines 210-239)
- Why fragile: If a socket file exists but D-Bus connection fails, the proxy is not retried. Failed connection is logged but not retried on subsequent socket events. If the remote service is temporarily down, operator must manually delete/recreate the socket file.
- Safe modification: Add retry mechanism with exponential backoff. Store failed sockets and retry periodically rather than just once.
- Test coverage: Unit tests exist for happy path (proxy connects, runs); no tests for connection failures or retry scenarios.

**Approval request resolution race condition:**
- Files: `internal/approval/manager.go` (lines 289-303, 306-320)
- Why fragile: `Approve()` and `Deny()` both write `req.result` and close `req.done` without synchronization. If called twice rapidly (via network race), the second call will panic when trying to close an already-closed channel.
- Safe modification: Add a check for already-resolved state before closing channel. Use sync.Once or a resolved flag.
- Test coverage: `internal/approval/manager_test.go` has tests but doesn't test double-resolution race condition.

**WebSocket client cleanup during Unsubscribe:**
- Files: `internal/api/websocket.go` (lines 342-355), `internal/proxy/manager.go` (lines 127-135)
- Why fragile: Both WebSocket and proxy manager implement Unsubscribe by iterating and removing from slice. If an observer is added multiple times or removed during iteration, index-based removal could skip or remove wrong observer.
- Safe modification: Use a map-based registration instead of slice. Add tests that register the same observer multiple times.
- Test coverage: No tests verify Unsubscribe behavior with duplicate registrations.

**Session initialization timing:**
- Files: `main.go` (lines 342-373), `internal/api/server.go` (lines 38-90)
- Why fragile: Server.Start() launches HTTP server in background goroutine (line 94). If Start() is called but server fails to bind to address, the error is only logged asynchronously and program continues. Caller has no way to detect startup failure.
- Safe modification: Change Start() to return error. Ensure address binding succeeds before returning. Add startup health check.
- Test coverage: Unit tests don't verify startup failure scenarios.

## Scaling Limits

**In-memory request storage:**
- Current capacity: ~100 pending requests (default historyLimit); limited by available memory.
- Limit: On heavily-loaded systems with many concurrent approval requests, could hit memory ceiling or garbage collection pauses.
- Scaling path: Implement persistent storage (SQLite/PostgreSQL) for requests and history. Add request archival/cleanup policy. Add metrics for request queue depth.

**Single approval manager instance:**
- Current capacity: One manager per process; all requests serialized through single mutex.
- Limit: Heavy lock contention if many requests arrive simultaneously. Single mutex on ~200 lines of code in manager.go could become bottleneck.
- Scaling path: Partition requests by client or hash. Use sharded mutexes or lock-free data structures. For horizontal scaling, introduce Redis-backed queue.

**WebSocket connection buffer:**
- Current capacity: 256-message buffer per client; ~1MB per slow client in worst case.
- Limit: Large number of slow clients (100+) could consume significant memory; dropping messages silently.
- Scaling path: Implement per-client flow control, prioritization, or tiered message delivery. Add metrics/alerts for dropped messages.

## Dependencies at Risk

**godbus/dbus/v5:**
- Risk: Core D-Bus communication library. If bugs found, could affect all proxy functionality. Limited alternative libraries in Go.
- Impact: D-Bus connection failures would block all secret operations.
- Migration plan: Library is actively maintained (last commit recent). No migration path without major rewrite.

**coder/websocket:**
- Risk: Non-standard WebSocket library (not gorilla/websocket). Less widely used, potential compatibility issues.
- Impact: WebSocket connection issues could break real-time UI updates.
- Migration plan: Gorilla websocket is standard alternative; API is similar, could migrate if needed.

**fsnotify:**
- Risk: File system watcher; performance/correctness varies by OS (FSEvents on macOS, inotify on Linux, etc.).
- Impact: Missing socket creation events in multi-socket mode could orphan clients.
- Migration plan: Well-maintained library; no migration needed. Document OS-specific limitations.

## Missing Critical Features

**No audit logging:**
- Problem: Approval decisions are not logged with timestamp, approver, request details. Operator has no permanent record of who approved what.
- Blocks: Compliance, incident investigation, debugging user disputes.
- Recommendation: Add audit log (file or syslog) with: timestamp, approver (cookie/token), request ID, decision, client, items accessed.

**No metrics/observability:**
- Problem: No way to monitor request volume, approval rate, timeouts, errors. Operator is blind to system health.
- Blocks: Capacity planning, performance analysis, alerting.
- Recommendation: Export Prometheus metrics: pending request count, approval/denial/timeout rates, WebSocket connection count, request latency.

**No rules/policy engine:**
- Problem: All requests require manual approval. No way to auto-approve/deny based on rules (e.g., "auto-approve requests from root for less than 5 items").
- Blocks: Usability for high-volume access patterns.
- Recommendation: Add YAML rules file with pattern matching (client name, sender UID, item path). Implement as approval.Decider interface.

## Test Coverage Gaps

**WebSocket disconnect race conditions:**
- What's not tested: Close connection while writePump/readPump are active; multiple simultaneous connects/disconnects.
- Files: `internal/api/websocket.go`, `internal/api/websocket_test.go`
- Risk: Connection leaks, panic on closed channel, or resource exhaustion.
- Priority: High

**Approval request double-resolution:**
- What's not tested: Calling Approve() and Deny() on same request concurrently; Approve() twice.
- Files: `internal/approval/manager.go`, `internal/approval/manager_test.go`
- Risk: Channel close panic, data corruption in pending map.
- Priority: High

**Proxy reconnection after socket disappear/reappear:**
- What's not tested: Socket file deleted while proxy running; new socket created with same name; fsnotify event ordering.
- Files: `internal/proxy/manager.go`, `internal/proxy/manager_test.go`
- Risk: Orphaned goroutines, connection leaks, requests hanging indefinitely.
- Priority: High

**History trimming correctness:**
- What's not tested: Exact order of entries when trimmed; concurrent reads while trimming happens; historyMax=0 edge case.
- Files: `internal/approval/manager.go`, `internal/approval/manager_test.go`
- Risk: Out-of-order history, stale pointers, panic on empty slice.
- Priority: Medium

**Auth cookie expiration and refresh:**
- What's not tested: Cookie expiry handling in middleware; JWT token expiry; session cookie MaxAge behavior.
- Files: `internal/api/auth.go`, `internal/api/auth_test.go`
- Risk: Expired cookies not caught; sessions persist indefinitely.
- Priority: Medium

---

*Concerns audit: 2026-02-23*
