<script lang="ts">
  import { onMount } from "svelte";
  import type { PendingRequest, AuthState } from "./lib/types";
  import { exchangeToken, getStatus, getPending, ApiError } from "./lib/api";
  import RequestCard from "./lib/RequestCard.svelte";

  let authState = $state<AuthState>("checking");
  let requests = $state<PendingRequest[]>([]);
  let loading = $state(true);
  let error = $state<string | null>(null);
  let connected = $state(false);

  let pollInterval: ReturnType<typeof setInterval> | null = null;

  async function checkAuth(): Promise<boolean> {
    const status = await getStatus();
    if (status !== null) {
      connected = true;
      return true;
    }
    return false;
  }

  async function fetchPending() {
    try {
      const result = await getPending();
      requests = result.requests;
      error = null;
      connected = true;
    } catch (e) {
      if (e instanceof ApiError) {
        if (e.status === 401) {
          authState = "unauthenticated";
          stopPolling();
          return;
        }
        error = e.message;
      } else {
        error = "Connection failed";
      }
      connected = false;
    } finally {
      loading = false;
    }
  }

  function startPolling() {
    if (pollInterval) return;
    fetchPending();
    pollInterval = setInterval(fetchPending, 2000);
  }

  function stopPolling() {
    if (pollInterval) {
      clearInterval(pollInterval);
      pollInterval = null;
    }
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
        startPolling();
      } else {
        authState = "unauthenticated";
        error = "Invalid or expired login link";
      }
    } else {
      // Check if we have a valid session
      const isAuth = await checkAuth();
      authState = isAuth ? "authenticated" : "unauthenticated";
      if (isAuth) {
        startPolling();
      }
    }
  }

  function handleRetry() {
    error = null;
    loading = true;
    fetchPending();
  }

  onMount(() => {
    handleAuth();
    return () => stopPolling();
  });
</script>

<header>
  <h1>Secrets Dispatcher</h1>
  {#if authState === "authenticated"}
    <div class="status-indicator">
      <span class="status-dot" class:ok={connected} class:error={!connected}
      ></span>
      <span>{connected ? "Connected" : "Disconnected"}</span>
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
  {:else if error}
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
        <RequestCard {request} onAction={fetchPending} />
      {/each}
    </section>
  {/if}
</main>

<style>
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
</style>
