<script lang="ts">
  import { onMount } from "svelte";
  import type { PendingRequest, AuthState, ClientInfo, HistoryEntry, AutoApproveRule } from "./lib/types";
  import { exchangeToken, getStatus, createAutoApprove, deleteAutoApproveRule } from "./lib/api";
  import { ApprovalWebSocket } from "./lib/websocket";
  import RequestCard from "./lib/RequestCard.svelte";
  import { requestPermission, showRequestNotification } from "./lib/notifications";

  let authState = $state<AuthState>("checking");
  let requests = $state<PendingRequest[]>([]);
  let clients = $state<ClientInfo[]>([]);
  let history = $state<HistoryEntry[]>([]);
  let autoApproveRules = $state<AutoApproveRule[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let connected = $state(false);
  let sidebarOpen = $state(false);
  let historyOpen = $state(true);
  let version = $state("");
  let useAbsoluteTime = $state(localStorage.getItem('timeFormat') === 'absolute');

  // Single-request mode: when opened from a desktop notification
  let focusRequestId = $state<string | null>(null);
  let focusRequestGone = $state(false); // true when the focused request was cancelled/expired

  // Tick counter for auto-approve rule timer display (increments every second)
  let tick = $state(0);

  function toggleTimeFormat() {
    useAbsoluteTime = !useAbsoluteTime;
    localStorage.setItem('timeFormat', useAbsoluteTime ? 'absolute' : 'relative');
  }

  let ws: ApprovalWebSocket | null = null;

  async function checkAuth(): Promise<boolean> {
    const status = await getStatus();
    if (status !== null) {
      clients = status.clients || [];
      return true;
    }
    return false;
  }

  function startWebSocket() {
    ws = new ApprovalWebSocket({
      onSnapshot: (reqs, cls, hist, ver, rules) => {
        requests = reqs;
        clients = cls;
        history = hist;
        version = ver;
        autoApproveRules = rules;
        loading = false;
        error = null;
        requestPermission();
      },
      onRequestCreated: (req) => {
        requests = [...requests, req];
        showRequestNotification(req);
      },
      onRequestResolved: (id) => {
        if (focusRequestId === id) window.close();
        requests = requests.filter((r) => r.id !== id);
      },
      onRequestExpired: (id) => {
        if (focusRequestId === id) focusRequestGone = true;
        requests = requests.filter((r) => r.id !== id);
      },
      onRequestCancelled: (id) => {
        if (focusRequestId === id) focusRequestGone = true;
        requests = requests.filter((r) => r.id !== id);
      },
      onClientConnected: (client) => {
        // Add client if not already present
        if (!clients.some((c) => c.socket_path === client.socket_path)) {
          clients = [...clients, client];
        }
      },
      onClientDisconnected: (client) => {
        clients = clients.filter((c) => c.socket_path !== client.socket_path);
      },
      onHistoryEntry: (entry) => {
        // Prepend new entry to history (newest first)
        history = [entry, ...history];
      },
      onAutoApproveRuleAdded: (rule) => {
        const idx = autoApproveRules.findIndex(r => r.id === rule.id);
        if (idx >= 0) {
          autoApproveRules[idx] = rule;
          autoApproveRules = autoApproveRules;
        } else {
          autoApproveRules = [...autoApproveRules, rule];
        }
      },
      onAutoApproveRuleRemoved: (id) => {
        autoApproveRules = autoApproveRules.filter(r => r.id !== id);
      },
      onConnectionChange: (isConnected) => {
        connected = isConnected;
        if (!isConnected) {
          error = "Connection lost. Reconnecting...";
        } else {
          error = null;
        }
      },
      onAuthError: () => {
        authState = "unauthenticated";
        ws?.disconnect();
        ws = null;
      },
      onVersionMismatch: () => {
        window.location.reload();
      },
    });
    ws.connect();
  }

  function stopWebSocket() {
    ws?.disconnect();
    ws = null;
  }

  async function handleAuth() {
    const params = new URLSearchParams(window.location.search);
    const token = params.get("token");

    // Check for single-request mode (opened from desktop notification)
    focusRequestId = params.get("request");

    if (token) {
      // Exchange JWT for session cookie
      const success = await exchangeToken(token);
      // Clear token from URL, but preserve other params
      params.delete("token");
      const remaining = params.toString();
      window.history.replaceState({}, "", remaining ? `/?${remaining}` : "/");

      if (success) {
        authState = "authenticated";
        startWebSocket();
      } else {
        authState = "unauthenticated";
        error = "Invalid or expired login link";
        loading = false;
      }
    } else {
      // Check if we have a valid session
      const isAuth = await checkAuth();
      authState = isAuth ? "authenticated" : "unauthenticated";
      if (isAuth) {
        startWebSocket();
      } else {
        loading = false;
      }
    }
  }

  function handleRetry() {
    error = null;
    loading = true;
    if (ws) {
      ws.disconnect();
    }
    startWebSocket();
  }

  function toggleSidebar() {
    sidebarOpen = !sidebarOpen;
  }

  function closeSidebar() {
    sidebarOpen = false;
  }

  function toggleHistory() {
    historyOpen = !historyOpen;
  }

  function formatRelativeTime(dateString: string): string {
    const date = new Date(dateString);
    const now = new Date();
    const diffMs = now.getTime() - date.getTime();
    const diffSec = Math.floor(diffMs / 1000);

    if (diffSec < 60) {
      return "just now";
    }

    const diffMin = Math.floor(diffSec / 60);
    if (diffMin < 60) {
      return `${diffMin} minute${diffMin !== 1 ? "s" : ""} ago`;
    }

    const diffHour = Math.floor(diffMin / 60);
    if (diffHour < 24) {
      return `${diffHour} hour${diffHour !== 1 ? "s" : ""} ago`;
    }

    const diffDay = Math.floor(diffHour / 24);
    return `${diffDay} day${diffDay !== 1 ? "s" : ""} ago`;
  }

  function formatAbsoluteTime(dateString: string): string {
    const date = new Date(dateString);
    const h = String(date.getHours()).padStart(2, "0");
    const m = String(date.getMinutes()).padStart(2, "0");
    const s = String(date.getSeconds()).padStart(2, "0");
    return `${h}:${m}:${s}`;
  }

  function formatTime(dateString: string): string {
    return useAbsoluteTime
      ? formatAbsoluteTime(dateString)
      : formatRelativeTime(dateString);
  }

  function resolutionClass(resolution: string): string {
    switch (resolution) {
      case "approved":
        return "resolution-approved";
      case "denied":
        return "resolution-denied";
      case "auto_approved":
        return "resolution-auto-approved";
      default:
        return "resolution-other";
    }
  }

  function formatSenderInfo(entry: HistoryEntry): string {
    const info = entry.request.sender_info;
    if (!info) {
      return entry.request.client;
    }

    // Prefix with repo for gpg_sign
    const repoPrefix = entry.request.type === "gpg_sign" && entry.request.gpg_sign_info
      ? entry.request.gpg_sign_info.repo_name + " · "
      : "";

    const user = info.user_name || (info.uid ? `UID ${info.uid}` : "");

    // If we have a unit name, show that with user
    if (info.unit_name) {
      return repoPrefix + (user ? `${info.unit_name} (${user})` : info.unit_name);
    }

    // Fall back to user with PID
    if (info.pid && user) {
      return repoPrefix + `${user} (PID ${info.pid})`;
    }

    // Fall back to just PID
    if (info.pid) {
      return repoPrefix + `PID ${info.pid}`;
    }

    // Fall back to client name
    return repoPrefix + entry.request.client;
  }

  function historyItemsSummary(request: PendingRequest): string {
    if (request.type === "gpg_sign" && request.gpg_sign_info) {
      return request.gpg_sign_info.commit_msg.split('\n')[0];
    }
    return request.items.map(i => i.label || i.path).join(", ");
  }

  function extractCollection(itemPath: string): string {
    const prefix = "/org/freedesktop/secrets/collection/";
    if (!itemPath.startsWith(prefix)) return "";
    const rest = itemPath.slice(prefix.length);
    const slash = rest.indexOf("/");
    return slash >= 0 ? rest.slice(0, slash) : rest;
  }

  function hasMatchingRule(entry: HistoryEntry): boolean {
    const req = entry.request;
    const invoker = req.sender_info?.unit_name ?? "";
    const collection = req.items.length > 0 ? extractCollection(req.items[0].path) : "";
    return autoApproveRules.some(r =>
      r.invoker_name === invoker &&
      r.request_type === req.type &&
      r.collection === collection
    );
  }

  async function handleAutoApprove(requestId: string) {
    try {
      await createAutoApprove(requestId);
      // Rule update will arrive via WebSocket
    } catch {
      // Ignore errors silently for now
    }
  }

  async function handleDeleteRule(ruleId: string) {
    try {
      await deleteAutoApproveRule(ruleId);
      autoApproveRules = autoApproveRules.filter(r => r.id !== ruleId);
    } catch {
      // Ignore
    }
  }

  function formatRuleExpiry(expiresAt: string, _tick: number): string {
    const diff = new Date(expiresAt).getTime() - Date.now();
    if (diff <= 0) return "expired";
    const min = Math.floor(diff / 60000);
    const sec = Math.floor((diff % 60000) / 1000);
    if (min > 0) return `${min}m ${sec}s`;
    return `${sec}s`;
  }

  // Called after approve/deny action to refresh (WebSocket will push update, but this ensures UI sync)
  function handleAction() {
    // No-op: WebSocket will push the update
  }

  onMount(() => {
    handleAuth();
    const timer = setInterval(() => {
      tick++;
      // Clean up expired rules
      if (autoApproveRules.length > 0) {
        autoApproveRules = autoApproveRules.filter(
          r => new Date(r.expires_at).getTime() > Date.now()
        );
      }
    }, 1000);
    return () => {
      clearInterval(timer);
      stopWebSocket();
    };
  });
</script>

<div class="app-layout" class:sidebar-open={sidebarOpen}>
  <!-- Sidebar overlay for mobile -->
  {#if sidebarOpen && !focusRequestId}
    <button class="sidebar-overlay" onclick={closeSidebar} aria-label="Close sidebar"></button>
  {/if}

  <!-- Sidebar -->
  {#if authState === "authenticated" && !focusRequestId}
    <aside class="sidebar" class:open={sidebarOpen}>
      <div class="sidebar-header">
        <h3>Connected Clients</h3>
        <button class="sidebar-close" onclick={closeSidebar} aria-label="Close sidebar">
          <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <line x1="18" y1="6" x2="6" y2="18"></line>
            <line x1="6" y1="6" x2="18" y2="18"></line>
          </svg>
        </button>
      </div>
      <div class="sidebar-content">
        {#if clients.length === 0}
          <p class="no-clients">No clients connected</p>
        {:else}
          <ul class="clients-list">
            {#each clients as client}
              <li>
                <span class="client-name">{client.name}</span>
                <span class="client-socket">{client.socket_path}</span>
              </li>
            {/each}
          </ul>
        {/if}
      </div>
      {#if autoApproveRules.length > 0}
        <div class="sidebar-rules">
          <h3>Auto-Approve Rules</h3>
          <ul class="rules-list">
            {#each autoApproveRules as rule (rule.id)}
              <li class="rule-entry">
                <div class="rule-header">
                  <span class="history-type history-type--{rule.request_type}">
                    {#if rule.request_type === "gpg_sign"}GPG Sign{:else if rule.request_type === "search"}Search{:else if rule.request_type === "delete"}Delete{:else if rule.request_type === "write"}Write{:else}Secret{/if}
                  </span>
                  <div class="rule-header-right">
                    <span class="rule-expiry">{formatRuleExpiry(rule.expires_at, tick)}</span>
                    <button class="rule-delete" onclick={() => handleDeleteRule(rule.id)} title="Remove rule">
                      <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="6" x2="6" y2="18"></line><line x1="6" y1="6" x2="18" y2="18"></line></svg>
                    </button>
                  </div>
                </div>
                <table class="rule-props"><tbody>
                  <tr><td class="rule-prop-key">process</td><td>{rule.invoker_name}</td></tr>
                  {#if rule.collection}<tr><td class="rule-prop-key">collection</td><td>{rule.collection}</td></tr>{/if}
                  {#if rule.attributes}
                    {#each Object.entries(rule.attributes) as [key, value]}
                      <tr><td class="rule-prop-key">{key}</td><td>{value}</td></tr>
                    {/each}
                  {/if}
                </tbody></table>
              </li>
            {/each}
          </ul>
        </div>
      {/if}
      {#if version}
        <div class="sidebar-footer">
          <span class="version">{version}</span>
        </div>
      {/if}
    </aside>
  {/if}

  <!-- Main content -->
  <div class="main-wrapper">
    <header>
      <div class="header-title">
        <h1>Secrets Dispatcher</h1>
        <a href="https://github.com/nikicat/secrets-dispatcher" target="_blank" rel="noopener noreferrer" class="github-link" aria-label="View on GitHub">
          <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="currentColor">
            <path d="M12 0c-6.626 0-12 5.373-12 12 0 5.302 3.438 9.8 8.207 11.387.599.111.793-.261.793-.577v-2.234c-3.338.726-4.033-1.416-4.033-1.416-.546-1.387-1.333-1.756-1.333-1.756-1.089-.745.083-.729.083-.729 1.205.084 1.839 1.237 1.839 1.237 1.07 1.834 2.807 1.304 3.492.997.107-.775.418-1.305.762-1.604-2.665-.305-5.467-1.334-5.467-5.931 0-1.311.469-2.381 1.236-3.221-.124-.303-.535-1.524.117-3.176 0 0 1.008-.322 3.301 1.23.957-.266 1.983-.399 3.003-.404 1.02.005 2.047.138 3.006.404 2.291-1.552 3.297-1.23 3.297-1.23.653 1.653.242 2.874.118 3.176.77.84 1.235 1.911 1.235 3.221 0 4.609-2.807 5.624-5.479 5.921.43.372.823 1.102.823 2.222v3.293c0 .319.192.694.801.576 4.765-1.589 8.199-6.086 8.199-11.386 0-6.627-5.373-12-12-12z"/>
          </svg>
        </a>
      </div>
      {#if authState === "authenticated" && !focusRequestId}
        <div class="header-actions">
          <div class="status-indicator">
            <span class="status-dot" class:ok={connected} class:error={!connected}></span>
            <span class="status-text">
              {#if connected}
                {clients.length} client{clients.length !== 1 ? "s" : ""} connected
              {:else}
                Reconnecting...
              {/if}
            </span>
          </div>
          <button class="sidebar-toggle" onclick={toggleSidebar} aria-label="Toggle client list">
            <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <rect x="3" y="3" width="18" height="18" rx="2" ry="2"></rect>
              <line x1="9" y1="3" x2="9" y2="21"></line>
            </svg>
          </button>
        </div>
      {/if}
    </header>

    <main>
      {#if authState === "checking"}
        <div class="center">
          <div class="spinner"></div>
          <p>Checking authentication...</p>
        </div>
      {:else if authState === "unauthenticated"}
        <div class="login-prompt">
          <h2>Authentication Required</h2>
          {#if error}
            <p class="error-message">{error}</p>
          {/if}
          <p>To access the web interface, run:</p>
          <pre><code>secrets-dispatcher login</code></pre>
          <p>Then open the generated URL in your browser.</p>
        </div>
      {:else if loading}
        <div class="center">
          <div class="spinner"></div>
          <p>Loading...</p>
        </div>
      {:else if error && !connected}
        <div class="error-state">
          <p class="error-message">{error}</p>
          <button class="btn-retry" onclick={handleRetry}>Retry</button>
        </div>
      {:else if focusRequestId}
        <!-- Single-request mode: opened from desktop notification -->
        {@const focusedRequest = requests.find(r => r.id === focusRequestId)}
        {#if focusedRequest}
          <RequestCard request={focusedRequest} onAction={handleAction} />
        {:else if focusRequestGone}
          <div class="empty-state">
            <p>Request is no longer pending</p>
          </div>
        {:else}
          <div class="empty-state">
            <p>Request not found</p>
          </div>
        {/if}
      {:else if requests.length === 0}
        <div class="empty-state">
          <p>No pending requests</p>
        </div>
        <!-- Show history section below empty state -->
        {#if history.length > 0}
          <section class="history-section">
            <button class="history-toggle" onclick={toggleHistory}>
              <h2>Recent Activity ({history.length})</h2>
              <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class:rotated={!historyOpen}>
                <polyline points="6 9 12 15 18 9"></polyline>
              </svg>
            </button>
            {#if historyOpen}
              <ul class="history-list">
                {#each history as entry (entry.request.id + entry.resolved_at)}
                  <li class="history-entry">
                    <div class="history-entry-header">
                      <div class="history-entry-badges">
                        <span class="history-type history-type--{entry.request.type}">
                          {#if entry.request.type === "gpg_sign"}
                            GPG Sign
                          {:else if entry.request.type === "search"}
                            Search
                          {:else if entry.request.type === "delete"}
                            Delete
                          {:else if entry.request.type === "write"}
                            Write
                          {:else}
                            Secret
                          {/if}
                        </span>
                        <span class="history-resolution {resolutionClass(entry.resolution)}">{entry.resolution}</span>
                      </div>
                      <button class="history-time clickable" onclick={toggleTimeFormat}>{formatTime(entry.resolved_at)}</button>
                    </div>
                    <div class="history-entry-details">
                      <span class="history-items">{historyItemsSummary(entry.request)}</span>
                      <span class="history-sender">{formatSenderInfo(entry)}</span>
                    </div>
                    {#if entry.resolution === "cancelled"}
                      <button class="btn-auto-approve" onclick={() => handleAutoApprove(entry.request.id)}>{hasMatchingRule(entry) ? "Reset auto-approve timer" : "Auto-approve similar"}</button>
                    {/if}
                  </li>
                {/each}
              </ul>
            {/if}
          </section>
        {/if}
      {:else}
        <section>
          <h2>Pending Requests ({requests.length})</h2>
          {#each [...requests].sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime()) as request (request.id)}
            <RequestCard {request} onAction={handleAction} />
          {/each}
        </section>
        <!-- Show history section below pending requests -->
        {#if history.length > 0}
          <section class="history-section">
            <button class="history-toggle" onclick={toggleHistory}>
              <h2>Recent Activity ({history.length})</h2>
              <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class:rotated={!historyOpen}>
                <polyline points="6 9 12 15 18 9"></polyline>
              </svg>
            </button>
            {#if historyOpen}
              <ul class="history-list">
                {#each history as entry (entry.request.id + entry.resolved_at)}
                  <li class="history-entry">
                    <div class="history-entry-header">
                      <div class="history-entry-badges">
                        <span class="history-type history-type--{entry.request.type}">
                          {#if entry.request.type === "gpg_sign"}
                            GPG Sign
                          {:else if entry.request.type === "search"}
                            Search
                          {:else if entry.request.type === "delete"}
                            Delete
                          {:else if entry.request.type === "write"}
                            Write
                          {:else}
                            Secret
                          {/if}
                        </span>
                        <span class="history-resolution {resolutionClass(entry.resolution)}">{entry.resolution}</span>
                      </div>
                      <button class="history-time clickable" onclick={toggleTimeFormat}>{formatTime(entry.resolved_at)}</button>
                    </div>
                    <div class="history-entry-details">
                      <span class="history-items">{historyItemsSummary(entry.request)}</span>
                      <span class="history-sender">{formatSenderInfo(entry)}</span>
                    </div>
                    {#if entry.resolution === "cancelled"}
                      <button class="btn-auto-approve" onclick={() => handleAutoApprove(entry.request.id)}>{hasMatchingRule(entry) ? "Reset auto-approve timer" : "Auto-approve similar"}</button>
                    {/if}
                  </li>
                {/each}
              </ul>
            {/if}
          </section>
        {/if}
      {/if}
    </main>
  </div>
</div>

<style>
  .app-layout {
    display: flex;
    min-height: 100vh;
  }

  .main-wrapper {
    flex: 1;
    max-width: 640px;
    margin: 0 auto;
    padding: 24px 16px;
    width: 100%;
  }

  /* Sidebar */
  .sidebar {
    position: fixed;
    top: 0;
    right: 0;
    width: 280px;
    height: 100vh;
    background-color: var(--color-surface);
    border-left: 1px solid var(--color-border);
    z-index: 100;
    transform: translateX(100%);
    transition: transform 0.2s ease;
    display: flex;
    flex-direction: column;
  }

  .sidebar.open {
    transform: translateX(0);
  }

  .sidebar-overlay {
    display: none;
    position: fixed;
    top: 0;
    left: 0;
    right: 0;
    bottom: 0;
    background-color: rgba(0, 0, 0, 0.5);
    z-index: 99;
    border: none;
    cursor: pointer;
  }

  .sidebar-open .sidebar-overlay {
    display: block;
  }

  .sidebar-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 16px;
    border-bottom: 1px solid var(--color-border);
  }

  .sidebar-header h3 {
    font-size: 14px;
    font-weight: 600;
    color: var(--color-text);
    margin: 0;
  }

  .sidebar-close {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 32px;
    height: 32px;
    padding: 0;
    background: transparent;
    border: 1px solid var(--color-border);
    border-radius: var(--radius-sm);
    color: var(--color-text-muted);
    cursor: pointer;
  }

  .sidebar-close:hover {
    background-color: var(--color-surface-hover);
    color: var(--color-text);
  }

  .sidebar-content {
    flex: 1;
    overflow-y: auto;
    padding: 16px;
  }

  .no-clients {
    color: var(--color-text-muted);
    font-size: 14px;
    text-align: center;
    padding: 24px 0;
  }

  .clients-list {
    list-style: none;
    padding: 0;
    margin: 0;
  }

  .clients-list li {
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: 12px;
    background-color: var(--color-bg);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-sm);
    margin-bottom: 8px;
  }

  .clients-list li:last-child {
    margin-bottom: 0;
  }

  .client-name {
    font-weight: 500;
    color: var(--color-text);
    font-size: 14px;
  }

  .client-socket {
    font-size: 12px;
    font-family: ui-monospace, "SF Mono", Monaco, monospace;
    color: var(--color-text-muted);
    word-break: break-all;
  }

  .sidebar-footer {
    padding: 12px 16px;
    border-top: 1px solid var(--color-border);
  }

  .version {
    font-size: 11px;
    font-family: ui-monospace, "SF Mono", Monaco, monospace;
    color: var(--color-text-muted);
    opacity: 0.6;
  }

  /* Header */
  header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding-bottom: 16px;
    margin-bottom: 24px;
    border-bottom: 1px solid var(--color-border);
  }

  h1 {
    font-size: 20px;
    font-weight: 600;
  }

  .header-title {
    display: flex;
    align-items: center;
    gap: 10px;
  }

  .github-link {
    color: var(--color-text-muted);
    display: flex;
    align-items: center;
    transition: color 0.2s;
  }

  .github-link:hover {
    color: var(--color-text);
  }

  h2 {
    font-size: 16px;
    font-weight: 500;
    margin-bottom: 16px;
    color: var(--color-text-muted);
  }

  .header-actions {
    display: flex;
    align-items: center;
    gap: 12px;
  }

  .status-text {
    display: none;
  }

  .sidebar-toggle {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 36px;
    height: 36px;
    padding: 0;
    background: transparent;
    border: 1px solid var(--color-border);
    border-radius: var(--radius-sm);
    color: var(--color-text-muted);
    cursor: pointer;
  }

  .sidebar-toggle:hover {
    background-color: var(--color-surface);
    color: var(--color-text);
  }

  /* Main content */
  .center {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 16px;
    padding: 48px 0;
    color: var(--color-text-muted);
  }

  .login-prompt {
    background-color: var(--color-surface);
    border: 1px solid var(--color-border);
    border-radius: var(--radius);
    padding: 24px;
    text-align: center;
  }

  .login-prompt h2 {
    color: var(--color-text);
    margin-bottom: 16px;
  }

  .login-prompt p {
    color: var(--color-text-muted);
    margin-bottom: 12px;
  }

  .login-prompt pre {
    background-color: var(--color-bg);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-sm);
    padding: 12px 16px;
    margin: 16px 0;
    font-family: ui-monospace, "SF Mono", Monaco, monospace;
    font-size: 14px;
  }

  .login-prompt code {
    color: var(--color-primary);
  }

  .error-message {
    color: var(--color-danger);
    background-color: rgba(239, 68, 68, 0.1);
    border: 1px solid var(--color-danger);
    border-radius: var(--radius-sm);
    padding: 8px 12px;
    margin-bottom: 16px;
  }

  .error-state {
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 16px;
    padding: 24px;
  }

  .empty-state {
    text-align: center;
    padding: 48px 0;
    color: var(--color-text-muted);
  }

  section {
    margin-top: 8px;
  }

  /* History section */
  .history-section {
    margin-top: 32px;
  }

  .history-toggle {
    display: flex;
    align-items: center;
    gap: 8px;
    width: 100%;
    background: transparent;
    border: none;
    padding: 0;
    cursor: pointer;
    text-align: left;
  }

  .history-toggle h2 {
    margin: 0;
  }

  .history-toggle svg {
    color: var(--color-text-muted);
    transition: transform 0.2s ease;
  }

  .history-toggle svg.rotated {
    transform: rotate(-90deg);
  }

  .history-list {
    list-style: none;
    padding: 0;
    margin: 16px 0 0 0;
  }

  .history-entry {
    display: flex;
    flex-direction: column;
    gap: 4px;
    padding: 12px;
    background-color: var(--color-surface);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-sm);
    margin-bottom: 8px;
  }

  .history-entry:last-child {
    margin-bottom: 0;
  }

  .history-entry-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
  }

  .history-entry-badges {
    display: flex;
    gap: 6px;
    align-items: center;
  }

  .history-resolution {
    font-size: 12px;
    font-weight: 600;
    text-transform: uppercase;
    padding: 2px 8px;
    border-radius: var(--radius-sm);
  }

  .history-type {
    font-size: 11px;
    font-weight: 500;
    text-transform: uppercase;
    padding: 2px 6px;
    border-radius: var(--radius-sm);
    color: var(--color-text-muted);
    background-color: var(--color-bg);
    border: 1px solid var(--color-border);
  }

  .history-type--gpg_sign {
    color: var(--color-gpg-sign);
    border-color: var(--color-gpg-sign);
    background-color: var(--color-gpg-sign-bg);
  }

  .history-type--delete,
  .history-type--write {
    color: var(--color-danger);
    border-color: var(--color-danger);
    background-color: color-mix(in srgb, var(--color-danger) 10%, transparent);
  }

  .resolution-approved {
    color: var(--color-success);
    background-color: rgba(34, 197, 94, 0.1);
  }

  .resolution-denied {
    color: var(--color-danger);
    background-color: rgba(239, 68, 68, 0.1);
  }

  .resolution-auto-approved {
    color: var(--color-primary);
    background-color: rgba(59, 130, 246, 0.1);
  }

  .resolution-other {
    color: var(--color-text-muted);
    background-color: var(--color-bg);
  }

  .history-time {
    font-size: 12px;
    color: var(--color-text-muted);
  }

  .history-time.clickable {
    background: none;
    border: none;
    padding: 0;
    font: inherit;
    cursor: pointer;
  }

  .history-time.clickable:hover {
    color: var(--color-text);
    text-decoration: underline;
  }

  .history-entry-details {
    display: flex;
    flex-direction: column;
    gap: 2px;
  }

  .history-items {
    font-size: 14px;
    font-weight: 500;
    color: var(--color-text);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .history-sender {
    font-size: 12px;
    color: var(--color-text-muted);
  }

  .btn-auto-approve {
    margin-top: 6px;
    padding: 4px 10px;
    font-size: 12px;
    font-weight: 500;
    color: var(--color-primary);
    background: transparent;
    border: 1px solid var(--color-primary);
    border-radius: var(--radius-sm);
    cursor: pointer;
  }

  .btn-auto-approve:hover {
    background-color: rgba(59, 130, 246, 0.1);
  }

  /* Sidebar auto-approve rules */
  .sidebar-rules {
    padding: 16px;
    border-top: 1px solid var(--color-border);
  }

  .sidebar-rules h3 {
    font-size: 14px;
    font-weight: 600;
    color: var(--color-text);
    margin: 0 0 8px 0;
  }

  .rules-list {
    list-style: none;
    padding: 0;
    margin: 0;
  }

  .rule-entry {
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: 8px;
    background-color: var(--color-bg);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-sm);
    margin-bottom: 6px;
  }

  .rule-entry:last-child {
    margin-bottom: 0;
  }

  .rule-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
  }

  .rule-header-right {
    display: flex;
    align-items: center;
    gap: 6px;
  }

  .rule-delete {
    display: flex;
    padding: 2px;
    background: transparent;
    border: none;
    color: var(--color-text-muted);
    cursor: pointer;
  }

  .rule-delete:hover {
    color: var(--color-danger);
  }

  .rule-props {
    width: 100%;
    font-size: 12px;
    border-collapse: collapse;
  }

  .rule-props td {
    padding: 1px 0;
    vertical-align: top;
    color: var(--color-text);
    word-break: break-all;
  }

  .rule-prop-key {
    color: var(--color-text-muted);
    white-space: nowrap;
    padding-right: 8px !important;
    width: 1%;
  }

  .rule-expiry {
    font-size: 11px;
    color: var(--color-warning);
  }

  /* Desktop: show sidebar by default, hide toggle */
  @media (min-width: 768px) {
    .sidebar {
      position: fixed;
      transform: translateX(0);
    }

    .sidebar-open .sidebar-overlay {
      display: none;
    }

    .sidebar-close {
      display: none;
    }

    .main-wrapper {
      margin-right: 280px;
    }

    .status-text {
      display: inline;
    }

    .sidebar-toggle {
      display: none;
    }
  }

  /* Large screens: center the main content better */
  @media (min-width: 1024px) {
    .main-wrapper {
      margin-left: auto;
      margin-right: calc(280px + ((100% - 640px - 280px) / 2));
    }
  }
</style>
