<script lang="ts">
  import type { HistoryEntry as HistoryEntryType, PendingRequest, AutoApproveRule, ManagedTrustRule, ProcessInfo } from "./types";
  import ProcessChain from "./ProcessChain.svelte";
  import PropsTable from "./PropsTable.svelte";

  interface Props {
    entry: HistoryEntryType;
    count?: number;
    tick: number;
    autoApproveRules: AutoApproveRule[];
    approvalRules: ManagedTrustRule[];
    formatTime: (dateString: string) => string;
    toggleTimeFormat: () => void;
    onAutoApprove: (requestId: string) => void;
    onSaveApproval: (requestId: string) => Promise<void>;
  }

  let { entry, count = 1, tick, autoApproveRules, approvalRules, formatTime, toggleTimeFormat, onAutoApprove, onSaveApproval }: Props = $props();
  let confirmingApproval = $state(false);
  let savingApproval = $state(false);
  let saveApprovalError = $state<string | null>(null);

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
    if (info.invoker_name) {
      return repoPrefix + (user ? `${info.invoker_name} (${user})` : info.invoker_name);
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
    const process = req.sender_info?.invoker_name || undefined;
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
    const invoker = req.sender_info?.invoker_name ?? "";
    const collection = req.items.length > 0 ? extractCollection(req.items[0].path) : "";
    const attrs = req.items.length > 0 ? req.items[0].attributes : undefined;
    return autoApproveRules.some(r =>
      r.invoker_name === invoker &&
      r.request_type === req.type &&
      r.collection === collection &&
      attributesEqual(r.attributes, attrs)
    );
  }

  interface ApprovalScope {
    process: ProcessInfo;
    collection: string;
    attributes: Record<string, string>;
    script?: string;
    cwd?: string;
  }

  function isInterpreter(exe: string): boolean {
    const base = exe.slice(exe.lastIndexOf("/") + 1).toLowerCase();
    return base.startsWith("python") || [
      "sh", "bash", "dash", "zsh", "fish", "ksh", "csh", "tcsh",
      "node", "nodejs", "ruby", "perl",
    ].includes(base);
  }

  function approvalScope(entry: HistoryEntryType): ApprovalScope | null {
    const req = entry.request;
    if (entry.resolution !== "approved" || req.type !== "get_secret" || req.items.length !== 1) {
      return null;
    }
    const item = req.items[0];
    const collection = extractCollection(item.path);
    const process = req.sender_info?.process_chain?.[0];
    if (!collection || Object.keys(item.attributes ?? {}).length === 0 || !process?.exe?.startsWith("/")) {
      return null;
    }

    if (!isInterpreter(process.exe)) {
      return { process, collection, attributes: item.attributes };
    }

    const args = process.args ?? [];
    const script = args[1] === "--" ? args[2] : args[1];
    if (!script || script.startsWith("-")) {
      return null;
    }
    if (!script.startsWith("/") && !process.cwd?.startsWith("/")) {
      return null;
    }
    return {
      process,
      collection,
      attributes: item.attributes,
      script,
      cwd: script.startsWith("/") ? undefined : process.cwd,
    };
  }

  function globQuote(value: string): string {
    return [...value].map(char => ["\\", "*", "?", "["].includes(char) ? `\\${char}` : char).join("");
  }

  function hasSavedApproval(scope: ApprovalScope | null): boolean {
    if (!scope) return false;
    const quotedAttributes = Object.fromEntries(
      Object.entries(scope.attributes).map(([key, value]) => [key, globQuote(value)]),
    );
    return approvalRules.some(rule =>
      rule.action === "approve" &&
      rule.request_types?.length === 1 &&
      rule.request_types[0] === "get_secret" &&
      rule.process?.direct === true &&
      rule.process.exe === globQuote(scope.process.exe ?? "") &&
      (rule.process.args ?? "") === (scope.script ? globQuote(scope.script) : "") &&
      (rule.process.cwd ?? "") === (scope.cwd ? globQuote(scope.cwd) : "") &&
      rule.secret?.collection === globQuote(scope.collection) &&
      attributesEqual(rule.secret.attributes, quotedAttributes)
    );
  }

  async function saveApproval() {
    savingApproval = true;
    saveApprovalError = null;
    try {
      await onSaveApproval(entry.request.id);
      confirmingApproval = false;
    } catch (err) {
      saveApprovalError = err instanceof Error ? err.message : "Could not save approval";
    } finally {
      savingApproval = false;
    }
  }

  let savedApprovalScope = $derived(approvalScope(entry));
  let approvalAlreadySaved = $derived(hasSavedApproval(savedApprovalScope));
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
      <span class="history-resolution {resolutionClass(entry.resolution)}">{entry.resolution}</span>
      {#if count > 1}
        <span class="history-count">&times;{count}</span>
      {/if}
      <span class="history-request-id" title={entry.request.id}>{entry.request.id.slice(0, 8)}</span>
    </div>
    <button class="history-time clickable" onclick={toggleTimeFormat}>{void tick, formatTime(entry.resolved_at)}</button>
  </div>
  <div class="history-entry-details">
    <span class="history-items">{historyItemsSummary(entry.request)}</span>
    <ProcessChain chain={entry.request.sender_info?.process_chain ?? []} fallbackText={formatSenderInfo(entry)} />
  </div>
  <PropsTable {...historyEntryProps(entry.request)} />
  {#if entry.request.attribution?.source === "managed_rule"}
    <div class="managed-attribution">
      Saved approval: {entry.request.attribution.rule_name ?? entry.request.attribution.rule_id ?? "managed rule"}
    </div>
  {/if}
  {#if entry.resolution === "cancelled"}
    <button class="btn-auto-approve" onclick={() => onAutoApprove(entry.request.id)}>{hasMatchingRule(entry) ? "Reset auto-approve timer" : "Auto-approve similar"}</button>
  {/if}
  {#if savedApprovalScope}
    {#if confirmingApproval}
      <div class="approval-confirmation">
        <strong>Always approve this exact access?</strong>
        <p>Future requests must match every value below.</p>
        <dl>
          <dt>Direct executable</dt>
          <dd><code>{savedApprovalScope.process.exe}</code></dd>
          {#if savedApprovalScope.script}
            <dt>Script argument</dt>
            <dd><code>{savedApprovalScope.script}</code></dd>
          {/if}
          {#if savedApprovalScope.cwd}
            <dt>Working directory</dt>
            <dd><code>{savedApprovalScope.cwd}</code></dd>
          {/if}
          <dt>Collection</dt>
          <dd><code>{savedApprovalScope.collection}</code></dd>
          {#each Object.entries(savedApprovalScope.attributes) as [key, value] (key)}
            <dt>{key}</dt>
            <dd><code>{value}</code></dd>
          {/each}
        </dl>
        {#if savedApprovalScope.script}
          <p class="argv-advisory">The script path is matched from argv. argv is advisory and can be rewritten by the process; the executable match remains authoritative.</p>
        {/if}
        {#if saveApprovalError}
          <p class="inline-error" role="alert">{saveApprovalError}</p>
        {/if}
        <div class="confirmation-actions">
          <button class="btn-confirm-approval" onclick={saveApproval} disabled={savingApproval}>{savingApproval ? "Saving..." : "Save exact approval"}</button>
          <button class="btn-cancel-approval" onclick={() => { confirmingApproval = false; saveApprovalError = null; }} disabled={savingApproval}>Cancel</button>
        </div>
      </div>
    {:else}
      <button class="btn-save-approval" onclick={() => confirmingApproval = true} disabled={approvalAlreadySaved}>
        {approvalAlreadySaved ? "Exact approval saved" : "Always approve exact access"}
      </button>
    {/if}
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

  .managed-attribution {
    margin-top: 4px;
    padding: 5px 8px;
    border-left: 2px solid var(--color-primary);
    color: var(--color-text-muted);
    background-color: rgba(59, 130, 246, 0.06);
    font-size: 12px;
  }

  .btn-save-approval,
  .btn-confirm-approval,
  .btn-cancel-approval {
    padding: 5px 10px;
    border-radius: var(--radius-sm);
    font-size: 12px;
    font-weight: 500;
    cursor: pointer;
  }

  .btn-save-approval {
    margin-top: 6px;
    color: var(--color-success);
    background: transparent;
    border: 1px solid var(--color-success);
  }

  .btn-save-approval:hover:not(:disabled) {
    background-color: rgba(34, 197, 94, 0.1);
  }

  .btn-save-approval:disabled,
  .confirmation-actions button:disabled {
    cursor: default;
    opacity: 0.6;
  }

  .approval-confirmation {
    margin-top: 8px;
    padding: 10px;
    border: 1px solid var(--color-success);
    border-radius: var(--radius-sm);
    background-color: color-mix(in srgb, var(--color-success) 5%, var(--color-bg));
  }

  .approval-confirmation strong {
    color: var(--color-text);
    font-size: 13px;
  }

  .approval-confirmation > p {
    margin: 3px 0 8px;
    color: var(--color-text-muted);
    font-size: 12px;
  }

  .approval-confirmation dl {
    display: grid;
    grid-template-columns: minmax(90px, auto) minmax(0, 1fr);
    gap: 4px 10px;
    margin: 0;
    font-size: 11px;
  }

  .approval-confirmation dt {
    color: var(--color-text-muted);
  }

  .approval-confirmation dd {
    min-width: 0;
    margin: 0;
    overflow-wrap: anywhere;
    color: var(--color-text);
  }

  .approval-confirmation .argv-advisory {
    margin-top: 8px;
    color: var(--color-warning);
  }

  .approval-confirmation .inline-error {
    margin-top: 8px;
    color: var(--color-danger);
  }

  .confirmation-actions {
    display: flex;
    gap: 8px;
    margin-top: 10px;
  }

  .btn-confirm-approval {
    color: white;
    background-color: var(--color-success);
    border: 1px solid var(--color-success);
  }

  .btn-cancel-approval {
    color: var(--color-text-muted);
    background: transparent;
    border: 1px solid var(--color-border);
  }
</style>
