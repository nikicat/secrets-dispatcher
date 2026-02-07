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

  <div class="items">
    <div class="items-label">Items:</div>
    <ul>
      {#each request.items as item}
        <li>{item}</li>
      {/each}
    </ul>
  </div>

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

  .items {
    margin-bottom: 16px;
  }

  .items-label {
    font-size: 13px;
    color: var(--color-text-muted);
    margin-bottom: 4px;
  }

  ul {
    list-style: none;
    padding-left: 8px;
  }

  li {
    font-size: 13px;
    color: var(--color-text);
    padding: 2px 0;
  }

  li::before {
    content: "\2022";
    color: var(--color-text-muted);
    margin-right: 8px;
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
