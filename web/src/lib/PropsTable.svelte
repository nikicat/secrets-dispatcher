<script lang="ts">
  interface Props {
    process?: string;
    collection?: string;
    attributes?: Record<string, string>;
  }

  let { process, collection, attributes }: Props = $props();
</script>

{#if process || collection || (attributes && Object.keys(attributes).length > 0)}
  <table class="props-table"><tbody>
    {#if process}<tr><td class="prop-key">process</td><td>{process}</td></tr>{/if}
    {#if collection}<tr><td class="prop-key">collection</td><td>{collection}</td></tr>{/if}
    {#if attributes}
      {#each Object.entries(attributes) as [key, value]}
        <tr><td class="prop-key">{key}</td><td>{value}</td></tr>
      {/each}
    {/if}
  </tbody></table>
{/if}

<style>
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
</style>
