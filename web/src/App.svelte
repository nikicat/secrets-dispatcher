<script lang="ts">
  import { onMount } from "svelte";
  import type { PendingRequest, AuthState, ClientInfo } from "./lib/types";
  import { exchangeToken, getStatus } from "./lib/api";
  import { ApprovalWebSocket } from "./lib/websocket";
  import RequestCard from "./lib/RequestCard.svelte";

  let authState = $state<AuthState>("checking");
  let requests = $state<PendingRequest[]>([]);
  let clients = $state<ClientInfo[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let connected = $state(false);
  let sidebarOpen = $state(false);

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
      onSnapshot: (reqs, cls) => {
        requests = reqs;
        clients = cls;
        loading = false;
        error = null;
      },
      onRequestCreated: (req) => {
        requests = [...requests, req];
      },
      onRequestResolved: (id) => {
        requests = requests.filter((r) => r.id !== id);
      },
      onRequestExpired: (id) => {
        requests = requests.filter((r) => r.id !== id);
      },
      onRequestCancelled: (id) => {
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

    if (token) {
      // Exchange JWT for session cookie
      const success = await exchangeToken(token);
      // Clear token from URL
      window.history.replaceState({}, "", "/");

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

  // Called after approve/deny action to refresh (WebSocket will push update, but this ensures UI sync)
  function handleAction() {
    // No-op: WebSocket will push the update
  }

  onMount(() => {
    handleAuth();
    return () => stopWebSocket();
  });
</script>

<div class="app-layout" class:sidebar-open={sidebarOpen}>
  <!-- Sidebar overlay for mobile -->
  {#if sidebarOpen}
    <button class="sidebar-overlay" onclick={closeSidebar} aria-label="Close sidebar"></button>
  {/if}

  <!-- Sidebar -->
  {#if authState === "authenticated"}
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
    </aside>
  {/if}

  <!-- Main content -->
  <div class="main-wrapper">
    <header>
      <h1>Secrets Dispatcher</h1>
      {#if authState === "authenticated"}
        <div class="header-actions">
          <div class="status-indicator">
            <span class="status-dot" class:ok={connected} class:error={!connected}></span>
            <span class="status-text">
              {#if connected}
                {clients.length} client{clients.length !== 1 ? "s" : ""}
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
      {:else if requests.length === 0}
        <div class="empty-state">
          <p>No pending requests</p>
        </div>
      {:else}
        <section>
          <h2>Pending Requests ({requests.length})</h2>
          {#each requests as request (request.id)}
            <RequestCard {request} onAction={handleAction} />
          {/each}
        </section>
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
