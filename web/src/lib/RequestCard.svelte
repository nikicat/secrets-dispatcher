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

  // Format sender info for display
  function formatSenderInfo(): string {
    const info = request.sender_info;
    if (!info) {
      return request.client;
    }

    const user = info.user_name || (info.uid ? `UID ${info.uid}` : "");

    // If we have a unit name, show that with username
    if (info.unit_name) {
      return user ? `${info.unit_name} (${user})` : info.unit_name;
    }

    // Fall back to username with PID
    if (info.pid && user) {
      return `${user} (PID ${info.pid})`;
    }

    // Fall back to just PID
    if (info.pid) {
      return `PID ${info.pid}`;
    }

    // Fall back to client name
    return request.client;
  }

  function commitSubject(msg: string): string {
    return msg.split('\n')[0];
  }

  function commitBody(msg: string): string {
    const lines = msg.split('\n');
    if (lines.length <= 1) return '';
    const body = lines.slice(1).join('\n').replace(/^\n/, '');
    return body.trimEnd();
  }

  function typeBadgeLabel(type: string): string {
    switch (type) {
      case "gpg_sign": return "GPG Sign";
      case "search": return "Search";
      case "delete": return "Delete";
      default: return "Secret";
    }
  }

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

<div class="card card--{request.type}">
  <div class="card-header">
    <div class="card-title">
      <div class="card-identity">
        <span class="type-badge type-badge--{request.type}">
          {typeBadgeLabel(request.type)}
        </span>
        <span class="session-id">
          {#if request.type === "gpg_sign" && request.gpg_sign_info}
            PID {request.sender_info.pid} · {request.gpg_sign_info.repo_name}
          {:else if request.sender_info?.pid}
            PID {request.sender_info.pid}
          {/if}
        </span>
      </div>
      <span class="item-summary">
        {#if request.type === "gpg_sign" && request.gpg_sign_info}
          {commitSubject(request.gpg_sign_info.commit_msg)}
        {:else}
          {request.items.map(i => i.label || i.path).join(", ") || "Secret request"}
        {/if}
      </span>
      {#if hasProcessChain()}
        <div class="process-chain">
          {#each request.sender_info.process_chain! as proc}
            <span class="chain-entry">{proc.name}[{proc.pid}]</span>
          {/each}
        </div>
      {:else}
        <span class="sender-info" title="Client: {request.client}">{formatSenderInfo()}</span>
      {/if}
    </div>
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
  {:else if request.type === "gpg_sign" && request.gpg_sign_info}
    {@const info = request.gpg_sign_info}
    <div class="gpg-sign-content">
      <div class="commit-meta">
        <div class="meta-row">
          <span class="meta-label">Author</span>
          <span class="meta-value">{info.author}</span>
        </div>
        <div class="meta-row">
          <span class="meta-label">Key</span>
          <span class="meta-value mono">{info.key_id}</span>
        </div>
      </div>

      {#if commitBody(info.commit_msg)}
        <details class="commit-body-toggle">
          <summary>Show full message</summary>
          <pre class="commit-body">{commitBody(info.commit_msg)}</pre>
        </details>
      {/if}

      {#if info.changed_files?.length}
        <div class="changed-files">
          <span class="section-label">Changed files ({info.changed_files.length})</span>
          {#each info.changed_files.slice(0, 5) as file}
            <div class="file-path mono">{file}</div>
          {/each}
          {#if info.changed_files.length > 5}
            <details>
              <summary>{info.changed_files.length - 5} more files</summary>
              {#each info.changed_files.slice(5) as file}
                <div class="file-path mono">{file}</div>
              {/each}
            </details>
          {/if}
        </div>
      {/if}

      <details class="secondary-meta">
        <summary>More details</summary>
        {#if info.committer && info.committer !== info.author}
          <div>Committer: {info.committer}</div>
        {/if}
        {#if info.parent_hash}
          <div class="mono">Parent: {info.parent_hash}</div>
        {/if}
      </details>
    </div>
  {:else}
    <div class="items">
      <h4>{request.type === "delete" ? "Items to Delete" : "Requested Secrets"}</h4>
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

  .card.card--gpg_sign {
    border-left: 3px solid var(--color-gpg-sign-border);
  }

  .card.card--delete {
    border-left: 3px solid var(--color-danger);
  }

  .card-header {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    margin-bottom: 12px;
    gap: 12px;
  }

  .card-title {
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
  }

  .card-identity {
    display: flex;
    align-items: center;
    gap: 8px;
    margin-bottom: 2px;
  }

  .type-badge {
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
    padding: 1px 6px;
    border-radius: var(--radius-sm);
    color: var(--color-text-muted);
    background-color: var(--color-bg);
    border: 1px solid var(--color-border);
  }

  .type-badge--gpg_sign {
    color: var(--color-gpg-sign);
    background-color: var(--color-gpg-sign-bg);
    border-color: var(--color-gpg-sign);
  }

  .type-badge--delete {
    color: var(--color-danger);
    background-color: color-mix(in srgb, var(--color-danger) 10%, transparent);
    border-color: var(--color-danger);
  }

  .session-id {
    font-size: 11px;
    color: var(--color-text-muted);
    font-family: ui-monospace, "SF Mono", Monaco, monospace;
  }

  .item-summary {
    font-weight: 600;
    font-size: 15px;
    color: var(--color-text);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .sender-info {
    font-size: 12px;
    color: var(--color-text-muted);
  }

  .process-chain {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
    align-items: center;
  }

  .chain-entry {
    font-size: 11px;
    font-family: ui-monospace, "SF Mono", Monaco, monospace;
    color: var(--color-text-muted);
    background-color: var(--color-bg);
    padding: 1px 5px;
    border-radius: var(--radius-sm);
    border: 1px solid var(--color-border);
  }

  .expires {
    font-size: 12px;
    color: var(--color-warning);
    white-space: nowrap;
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

  /* GPG sign card styles */
  .gpg-sign-content {
    margin-bottom: 16px;
  }

  .commit-meta {
    margin-bottom: 12px;
  }

  .meta-row {
    display: flex;
    gap: 8px;
    font-size: 13px;
    padding: 2px 0;
  }

  .meta-label {
    color: var(--color-text-muted);
    min-width: 50px;
  }

  .meta-value {
    color: var(--color-text);
  }

  .mono {
    font-family: ui-monospace, "SF Mono", Monaco, monospace;
    font-size: 12px;
  }

  .commit-body-toggle {
    margin-bottom: 12px;
  }

  .commit-body-toggle summary {
    font-size: 12px;
    color: var(--color-primary);
    cursor: pointer;
  }

  .commit-body {
    margin-top: 8px;
    padding: 8px 12px;
    background-color: var(--color-bg);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-sm);
    font-size: 12px;
    font-family: ui-monospace, "SF Mono", Monaco, monospace;
    white-space: pre-wrap;
    color: var(--color-text-muted);
  }

  .changed-files {
    margin-bottom: 12px;
  }

  .section-label {
    display: block;
    font-size: 13px;
    font-weight: 600;
    color: var(--color-text-muted);
    margin-bottom: 4px;
  }

  .file-path {
    font-size: 12px;
    color: var(--color-text);
    padding: 2px 0;
  }

  .changed-files details summary {
    font-size: 12px;
    color: var(--color-primary);
    cursor: pointer;
    padding-top: 4px;
  }

  .secondary-meta {
    font-size: 12px;
    color: var(--color-text-muted);
  }

  .secondary-meta summary {
    color: var(--color-primary);
    cursor: pointer;
  }

  .secondary-meta div {
    padding: 2px 0;
  }
</style>
