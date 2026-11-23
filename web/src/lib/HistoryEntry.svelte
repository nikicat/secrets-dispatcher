<script lang="ts">
  import type { ComponentProps } from "svelte";
  import type {
    HistoryEntry as HistoryEntryType,
    RequestRecord,
    AutoApproveRule,
    AutoApproveScope,
  } from "./types";
  import ProcessChain from "./ProcessChain.svelte";
  import PropsTable from "./PropsTable.svelte";
  import { senderDisplayName } from "./sender";

  interface Props {
    entry: HistoryEntryType;
    count?: number;
    tick: number;
    autoApproveRules: AutoApproveRule[];
    formatTime: (dateString: string) => string;
    toggleTimeFormat: () => void;
    onAutoApprove: (requestId: string) => void;
  }

  let {
    entry,
    count = 1,
    tick,
    autoApproveRules,
    formatTime,
    toggleTimeFormat,
    onAutoApprove,
  }: Props = $props();

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
    const repoPrefix =
      entry.request.type === "gpg_sign" && entry.request.gpg_sign_info
        ? entry.request.gpg_sign_info.repo_name + " · "
        : "";

    const user = info.uid !== undefined ? `UID ${info.uid}` : "";

    // If we have a process or unit name, show that with user
    const name = senderDisplayName(info);
    if (name) {
      return repoPrefix + (user ? `${name} (${user})` : name);
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

  function historyItemsSummary(request: RequestRecord): string {
    if (request.type === "gpg_sign" && request.gpg_sign_info) {
      return request.gpg_sign_info.commit_msg.split("\n")[0];
    }
    return request.items.map((i) => i.label || i.path).join(", ");
  }

  type Attributes = Record<string, string>;

  const request = $derived(entry.request);
  const firstItem = $derived(request.items.at(0));

  function nonEmpty(attrs: Attributes | undefined): Attributes | undefined {
    return attrs && Object.keys(attrs).length > 0 ? attrs : undefined;
  }

  const tableProps: ComponentProps<typeof PropsTable> = $derived({
    process:
      (request.sender_info && senderDisplayName(request.sender_info)) ||
      undefined,
    collection: firstItem?.collection,
    attributes:
      nonEmpty(request.search_attributes) ?? nonEmpty(firstItem?.attributes),
  });

  function attributesEqual(
    a: Attributes | undefined,
    b: Attributes | undefined,
  ): boolean {
    const aa = a ?? {};
    const bb = b ?? {};
    const keysA = Object.keys(aa);
    const keysB = Object.keys(bb);
    if (keysA.length !== keysB.length) return false;
    return keysA.every((k) => aa[k] === bb[k]);
  }

  // Mirrors Go AutoApproveScope.Equal, which decides whether approving the
  // request resets a rule's timer or adds a rule.
  function scopeEqual(a: AutoApproveScope, b: AutoApproveScope): boolean {
    return (
      a.invoker_exe === b.invoker_exe &&
      a.request_type === b.request_type &&
      a.collection === b.collection &&
      attributesEqual(a.attributes, b.attributes)
    );
  }

  const hasMatchingRule = $derived.by(() => {
    const scope = request.auto_approve_scope;
    return !!scope && autoApproveRules.some((r) => scopeEqual(r, scope));
  });
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
        {:else if entry.request.type === "unlock"}
          Unlock
        {:else if entry.request.type === "ssh_sign"}
          SSH Sign
        {:else}
          Secret
        {/if}
      </span>
      <span class="history-resolution {resolutionClass(entry.resolution)}"
        >{entry.resolution}</span
      >
      {#if count > 1}
        <span class="history-count">&times;{count}</span>
      {/if}
      <span class="history-request-id" title={entry.request.id}
        >{entry.request.id.slice(0, 8)}</span
      >
    </div>
    <button class="history-time clickable" onclick={toggleTimeFormat}
      >{(void tick, formatTime(entry.resolved_at))}</button
    >
  </div>
  <div class="history-entry-details">
    <span class="history-items">{historyItemsSummary(entry.request)}</span>
    <ProcessChain
      chain={entry.request.sender_info?.process_chain ?? []}
      fallbackText={formatSenderInfo(entry)}
    />
  </div>
  <PropsTable {...tableProps} />
  {#if entry.resolution === "cancelled" && request.auto_approve_scope}
    <button
      class="btn-auto-approve"
      onclick={() => onAutoApprove(entry.request.id)}
      >{hasMatchingRule
        ? "Reset auto-approve timer"
        : "Auto-approve similar"}</button
    >
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

  .history-count {
    font-size: 12px;
    font-weight: 600;
    color: var(--color-primary);
    background-color: rgba(59, 130, 246, 0.1);
    padding: 2px 6px;
    border-radius: var(--radius-sm);
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
