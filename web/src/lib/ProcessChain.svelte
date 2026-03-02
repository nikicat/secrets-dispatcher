<script lang="ts">
  import type { ProcessInfo } from "./types";

  interface Props {
    chain: ProcessInfo[];
    fallbackText?: string;
  }

  let { chain, fallbackText = "" }: Props = $props();
</script>

{#if chain.length > 0}
  <details class="process-chain-details">
    <summary class="process-chain">
      {#each chain as proc}
        <span class="chain-entry">{proc.name}</span>
      {/each}
    </summary>
    <div class="chain-detail-list">
      {#each chain as proc}
        <div class="chain-detail-entry">
          <span class="chain-detail-name">{proc.name}</span>
          <table class="props-table"><tbody>
            <tr><td class="prop-key">pid</td><td>{proc.pid}</td></tr>
            {#if proc.exe}
              <tr><td class="prop-key">exe</td><td>{proc.exe}</td></tr>
            {/if}
            {#if proc.args && proc.args.length > 1}
              <tr><td class="prop-key">args</td><td>{proc.args.slice(1).join(' ')}</td></tr>
            {/if}
            {#if proc.cwd}
              <tr><td class="prop-key">cwd</td><td>{proc.cwd}</td></tr>
            {/if}
          </tbody></table>
        </div>
      {/each}
    </div>
  </details>
{:else if fallbackText}
  <span class="sender-info">{fallbackText}</span>
{/if}

<style>
  .process-chain-details summary {
    cursor: pointer;
    list-style: none;
  }

  .process-chain-details summary::-webkit-details-marker {
    display: none;
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

  .chain-detail-list {
    margin-top: 8px;
    display: flex;
    flex-direction: column;
    gap: 6px;
  }

  .chain-detail-entry {
    padding: 4px 8px;
    background-color: var(--color-bg);
    border: 1px solid var(--color-border);
    border-radius: var(--radius-sm);
  }

  .chain-detail-name {
    font-size: 12px;
    font-weight: 500;
    color: var(--color-text);
  }

  .props-table {
    width: 100%;
    font-size: 12px;
    border-collapse: collapse;
  }

  .props-table td {
    padding: 1px 0;
    vertical-align: top;
    color: var(--color-text);
    word-break: break-all;
  }

  .prop-key {
    color: var(--color-text-muted);
    opacity: 0.7;
    white-space: nowrap;
    padding-right: 8px !important;
    width: 1%;
    text-align: right;
  }

  .sender-info {
    font-size: 12px;
    color: var(--color-text-muted);
  }
</style>
