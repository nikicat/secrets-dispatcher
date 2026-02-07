<script lang="ts">
  import type { PendingRequest } from "./types";
  import { approve, deny, ApiError } from "./api";

  interface Props {
    request: PendingRequest;
    onAction: () => void;
  }

  let { request, onAction }: Props = $props();

  let loading = $state<"approve" | "deny" | null>(null);
  let error = $state<string | null>(null);
  let timeLeft = $state("");
  let copiedPath = $state<string | null>(null);

  async function copyToClipboard(path: string) {
    await navigator.clipboard.writeText(path);
    copiedPath = path;
    setTimeout(() => {
      copiedPath = null;
    }, 2000);
  }

  function updateTimeLeft() {
    const now = Date.now();
    const expires = new Date(request.expires_at).getTime();
    const diff = expires - now;

    if (diff <= 0) {
      timeLeft = "Expired";
      return;
    }

    const minutes = Math.floor(diff / 60000);
    const seconds = Math.floor((diff % 60000) / 1000);
    timeLeft = `${minutes}m ${seconds.toString().padStart(2, "0")}s`;
  }

  $effect(() => {
    updateTimeLeft();
    const interval = setInterval(updateTimeLeft, 1000);
    return () => clearInterval(interval);
  });

  async function handleApprove() {
    loading = "approve";
    error = null;
    try {
      await approve(request.id);
      onAction();
    } catch (e) {
      if (e instanceof ApiError) {
        error = e.message;
      } else {
        error = "Failed to approve";
      }
    } finally {
      loading = null;
    }
  }

  async function handleDeny() {
    loading = "deny";
    error = null;
    try {
      await deny(request.id);
      onAction();
    } catch (e) {
      if (e instanceof ApiError) {
        error = e.message;
      } else {
        error = "Failed to deny";
      }
    } finally {
      loading = null;
    }
  }
</script>

<div class="card">
  <div class="card-header">
    <span class="client">Client: {request.client}</span>
    <span class="expires">Expires: {timeLeft}</span>
  </div>

  {#if request.type === "search"}
    <div class="search-criteria">
      <h4>Search Criteria</h4>
      {#if request.search_attributes && Object.keys(request.search_attributes).length > 0}
        <table class="attributes-table">
          <tbody>
            {#each Object.entries(request.search_attributes) as [key, value]}
              <tr>
                <td class="attr-key">{key}</td>
                <td class="attr-value">{value}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      {:else}
        <p class="no-criteria">No search criteria specified</p>
      {/if}
    </div>
    <div class="items">
      <h4>Matching Items ({request.items.length})</h4>
      {#if request.items.length === 0}
        <p class="no-items">No matching items found</p>
      {:else}
        {#each request.items as item}
          <div class="item-card">
            <div class="item-header">
              <span class="item-label">{item.label || "Unnamed"}</span>
              <button
                class="copy-btn"
                onclick={() => copyToClipboard(item.path)}
                title="Copy item path"
              >
                {#if copiedPath === item.path}
                  <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <polyline points="20 6 9 17 4 12"></polyline>
                  </svg>
                {:else}
                  <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
                    <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>
                  </svg>
                {/if}
              </button>
            </div>
            {#if item.attributes && Object.keys(item.attributes).length > 0}
              <table class="attributes-table">
                <tbody>
                  {#each Object.entries(item.attributes) as [key, value]}
                    <tr>
                      <td class="attr-key">{key}</td>
                      <td class="attr-value">{value}</td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            {/if}
          </div>
        {/each}
      {/if}
    </div>
  {:else}
    <div class="items">
      <h4>Requested Secrets</h4>
      {#each request.items as item}
        <div class="item-card">
          <div class="item-header">
            <span class="item-label">{item.label || "Unnamed"}</span>
            <button
              class="copy-btn"
              onclick={() => copyToClipboard(item.path)}
              title="Copy item path"
            >
              {#if copiedPath === item.path}
                <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <polyline points="20 6 9 17 4 12"></polyline>
                </svg>
              {:else}
                <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                  <rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect>
                  <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path>
                </svg>
              {/if}
            </button>
          </div>
          {#if item.attributes && Object.keys(item.attributes).length > 0}
            <table class="attributes-table">
              <tbody>
                {#each Object.entries(item.attributes) as [key, value]}
                  <tr>
                    <td class="attr-key">{key}</td>
                    <td class="attr-value">{value}</td>
                  </tr>
                {/each}
              </tbody>
            </table>
          {/if}
        </div>
      {/each}
    </div>
  {/if}

  {#if error}
    <div class="error">{error}</div>
  {/if}

  <div class="actions">
    <button
      class="btn-approve"
      onclick={handleApprove}
      disabled={loading !== null}
    >
      {#if loading === "approve"}
        Approving...
      {:else}
        Approve
      {/if}
    </button>
    <button class="btn-deny" onclick={handleDeny} disabled={loading !== null}>
      {#if loading === "deny"}
        Denying...
      {:else}
        Deny
      {/if}
    </button>
  </div>
</div>

<style>
  .card {
    background-color: var(--color-surface);
    border: 1px solid var(--color-border);
    border-radius: var(--radius);
    padding: 16px;
    margin-bottom: 16px;
  }

  .card-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 12px;
  }

  .client {
    font-weight: 600;
    color: var(--color-text);
  }

  .expires {
    font-size: 13px;
    color: var(--color-warning);
  }

  .search-criteria {
    margin-bottom: 16px;
    padding-bottom: 16px;
    border-bottom: 1px solid var(--color-border);
  }

  .search-criteria h4 {
    font-size: 13px;
    font-weight: 600;
    color: var(--color-text-muted);
    margin: 0 0 8px 0;
  }

  .no-criteria,
  .no-items {
    font-size: 13px;
    color: var(--color-text-muted);
    font-style: italic;
    margin: 0;
  }

  .items {
    margin-bottom: 16px;
  }

  .items h4 {
    font-size: 13px;
    font-weight: 600;
    color: var(--color-text-muted);
    margin: 0 0 8px 0;
  }

  .item-card {
    background-color: var(--color-bg);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-sm);
    padding: 10px 12px;
    margin-bottom: 8px;
  }

  .item-card:last-child {
    margin-bottom: 0;
  }

  .item-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 6px;
  }

  .item-label {
    font-weight: 500;
    font-size: 14px;
    color: var(--color-text);
  }

  .copy-btn {
    display: flex;
    align-items: center;
    justify-content: center;
    padding: 4px;
    background: transparent;
    border: 1px solid var(--color-border);
    border-radius: var(--radius-sm);
    color: var(--color-text-muted);
    cursor: pointer;
    transition: all 0.15s ease;
  }

  .copy-btn:hover {
    background-color: var(--color-surface);
    color: var(--color-text);
    border-color: var(--color-text-muted);
  }

  .attributes-table {
    width: 100%;
    font-size: 12px;
    border-collapse: collapse;
  }

  .attributes-table tr {
    border-top: 1px solid var(--color-border);
  }

  .attributes-table td {
    padding: 4px 0;
  }

  .attr-key {
    color: var(--color-text-muted);
    width: 40%;
    padding-right: 8px;
  }

  .attr-value {
    color: var(--color-text);
    word-break: break-all;
  }

  .error {
    background-color: rgba(239, 68, 68, 0.1);
    border: 1px solid var(--color-danger);
    border-radius: var(--radius-sm);
    padding: 8px 12px;
    margin-bottom: 12px;
    font-size: 13px;
    color: var(--color-danger);
  }

  .actions {
    display: flex;
    gap: 12px;
  }
</style>
