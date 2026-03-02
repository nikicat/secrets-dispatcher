<script lang="ts">
  import type { HistoryEntry as HistoryEntryType, PendingRequest, AutoApproveRule } from "./types";
  import ProcessChain from "./ProcessChain.svelte";
  import PropsTable from "./PropsTable.svelte";

  interface Props {
    entry: HistoryEntryType;
    autoApproveRules: AutoApproveRule[];
    formatTime: (dateString: string) => string;
    toggleTimeFormat: () => void;
    onAutoApprove: (requestId: string) => void;
  }

  let { entry, autoApproveRules, formatTime, toggleTimeFormat, onAutoApprove }: Props = $props();

  function resolutionClass(resolution: string): string {
    switch (resolution) {
      case "approved":
        return "resolution-approved";
      case "denied":
        return "resolution-denied";
      case "auto_approved":
        return "resolution-auto-approved";
      case "ignored":
        return "resolution-ignored";
      default:
        return "resolution-other";
    }
  }

  function formatSenderInfo(entry: HistoryEntryType): string {
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
    for (const prefix of [
      "/org/freedesktop/secrets/collection/",
      "/org/freedesktop/secrets/aliases/",
    ]) {
      if (itemPath.startsWith(prefix)) {
        const rest = itemPath.slice(prefix.length);
        const slash = rest.indexOf("/");
        return slash >= 0 ? rest.slice(0, slash) : rest;
      }
    }
    return "";
  }

  function historyEntryProps(req: PendingRequest): { process?: string; collection?: string; attributes?: Record<string, string> } {
    const process = req.sender_info?.unit_name || undefined;
    const collection = req.items.length > 0 ? extractCollection(req.items[0].path) || undefined : undefined;
    let attributes: Record<string, string> | undefined;
    if (req.type === "search" && req.search_attributes && Object.keys(req.search_attributes).length > 0) {
      attributes = req.search_attributes;
    } else if (req.type !== "gpg_sign" && req.items.length > 0 && req.items[0].attributes && Object.keys(req.items[0].attributes).length > 0) {
      attributes = req.items[0].attributes;
    }
    return { process, collection, attributes };
  }

  function attributesEqual(a: Record<string, string> | undefined, b: Record<string, string> | undefined): boolean {
    const aa = a ?? {};
    const bb = b ?? {};
    const keysA = Object.keys(aa);
    const keysB = Object.keys(bb);
    if (keysA.length !== keysB.length) return false;
    return keysA.every(k => aa[k] === bb[k]);
  }

  function hasMatchingRule(entry: HistoryEntryType): boolean {
    const req = entry.request;
    const invoker = req.sender_info?.unit_name ?? "";
    const collection = req.items.length > 0 ? extractCollection(req.items[0].path) : "";
    const attrs = req.items.length > 0 ? req.items[0].attributes : undefined;
    return autoApproveRules.some(r =>
      r.invoker_name === invoker &&
      r.request_type === req.type &&
      r.collection === collection &&
      attributesEqual(r.attributes, attrs)
    );
  }
</script>

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
      <span class="history-request-id" title={entry.request.id}>{entry.request.id.slice(0, 8)}</span>
    </div>
    <button class="history-time clickable" onclick={toggleTimeFormat}>{formatTime(entry.resolved_at)}</button>
  </div>
  <div class="history-entry-details">
    <span class="history-items">{historyItemsSummary(entry.request)}</span>
    <ProcessChain chain={entry.request.sender_info?.process_chain ?? []} fallbackText={formatSenderInfo(entry)} />
  </div>
  <PropsTable {...historyEntryProps(entry.request)} />
  {#if entry.resolution === "cancelled"}
    <button class="btn-auto-approve" onclick={() => onAutoApprove(entry.request.id)}>{hasMatchingRule(entry) ? "Reset auto-approve timer" : "Auto-approve similar"}</button>
  {/if}
</li>

<style>
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

  .resolution-ignored {
    color: var(--color-text-muted);
    background-color: var(--color-bg);
  }

  .resolution-other {
    color: var(--color-text-muted);
    background-color: var(--color-bg);
  }

  .history-request-id {
    font-size: 11px;
    font-family: ui-monospace, "SF Mono", Monaco, monospace;
    color: var(--color-text-muted);
    opacity: 0.6;
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
</style>
