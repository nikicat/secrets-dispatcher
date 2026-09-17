<script lang="ts">
  import type { ProcessMatcher, SecretMatcher } from "./types";

  interface Props {
    requestTypes: string[];
    process: ProcessMatcher;
    secret?: SecretMatcher;
    searchAttributes?: Record<string, string>;
    enabled?: boolean;
    allItems?: boolean;
  }

  let {
    requestTypes,
    process,
    secret,
    searchAttributes,
    enabled = true,
    allItems = false,
  }: Props = $props();
</script>

<div class="scope-preview">
  <strong>Resulting scope</strong>
  <dl>
    <dt>Status</dt><dd>{enabled ? "Enabled" : "Disabled (will not approve until enabled)"}</dd>
    <dt>Request types</dt><dd><code>{requestTypes.join(", ")}</code></dd>
    {#if process.direct}<dt>Process mode</dt><dd>Direct request process only</dd>{/if}
    {#if process.exe}<dt>Executable</dt><dd><code>{process.exe}</code></dd>{/if}
    {#if process.name}<dt>Process name</dt><dd><code>{process.name}</code></dd>{/if}
    {#if process.unit}<dt>Systemd unit</dt><dd><code>{process.unit}</code></dd>{/if}
    {#if process.args}<dt>Argument</dt><dd><code>{process.args}</code></dd>{/if}
    {#if process.cwd}<dt>Working directory</dt><dd><code>{process.cwd}</code></dd>{/if}
    {#if secret?.collection}<dt>Collection</dt><dd><code>{secret.collection}</code></dd>{/if}
    {#if secret?.label}<dt>Label</dt><dd><code>{secret.label}</code></dd>{/if}
    {#each Object.entries(secret?.attributes ?? {}) as [key, value] (key)}
      <dt>Secret: {key}</dt><dd><code>{value}</code></dd>
    {/each}
    {#each Object.entries(searchAttributes ?? {}) as [key, value] (key)}
      <dt>Search: {key}</dt><dd><code>{value}</code></dd>
    {/each}
  </dl>
  {#if allItems && secret}
    <p>Every item in a request must match this secret scope.</p>
  {/if}
  {#if !secret && !searchAttributes}
    <p>No secret matcher is set; this process and request-type scope applies to any item.</p>
  {/if}
  {#if process.direct}
    <p>All displayed process fields must match the direct request process.</p>
  {:else}
    <p>All process fields are required, but non-direct fields may match across the process chain. Glob patterns are supported.</p>
  {/if}
</div>

<style>
  .scope-preview {
    margin: 10px 0;
    padding: 10px;
    color: var(--color-text-muted);
    background: var(--color-bg);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-sm);
    font-size: 12px;
  }

  strong {
    color: var(--color-text);
  }

  dl {
    display: grid;
    grid-template-columns: minmax(90px, auto) minmax(0, 1fr);
    gap: 4px 10px;
    margin: 8px 0 0;
  }

  dt {
    color: var(--color-text-muted);
  }

  dd {
    min-width: 0;
    margin: 0;
    overflow-wrap: anywhere;
    color: var(--color-text);
  }

  p {
    margin: 8px 0 0;
  }
</style>
