<script lang="ts">
  interface Props {
    process?: string;
    client?: string;
    collection?: string;
    attributes?: Record<string, string>;
  }

  let { process, client, collection, attributes }: Props = $props();

  const rows = $derived([
    ...(
      [
        ["process", process],
        ["client", client],
        ["collection", collection],
      ] satisfies [string, string | undefined][]
    ).filter((row): row is [string, string] => !!row[1]),
    ...Object.entries(attributes ?? {}),
  ]);
</script>

{#if rows.length > 0}
  <table class="props-table">
    <tbody>
      {#each rows as [key, value]}
        <tr><td class="prop-key">{key}</td><td>{value}</td></tr>
      {/each}
    </tbody>
  </table>
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
