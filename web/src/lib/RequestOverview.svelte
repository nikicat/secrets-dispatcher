<script lang="ts">
  import type { PendingRequest } from "./types";
  import ProcessChain from "./ProcessChain.svelte";
  import { requestAction, requestApplication, requestSecret } from "./requestPresentation";

  interface Props {
    request: PendingRequest;
    itemSummaryHook?: boolean;
  }

  let { request, itemSummaryHook = false }: Props = $props();
</script>

<div class="request-overview">
  <div class="overview-row">
    <span class="overview-label">Application</span>
    <span class="overview-value application-value">{requestApplication(request)}</span>
  </div>
  <div class="overview-row">
    <span class="overview-label">Request</span>
    <span class="overview-value">{requestAction(request)}</span>
  </div>
  <div class="overview-row">
    <span class="overview-label">Secret</span>
    <span class="overview-value secret-value" class:item-summary={itemSummaryHook}>{requestSecret(request)}</span>
  </div>
  <div class="overview-row overview-row--process">
    <span class="overview-label">Process</span>
    <div class="overview-value process-value">
      <ProcessChain
        chain={request.sender_info?.process_chain ?? []}
        fallbackText={request.sender_info?.invoker_name || request.client}
      />
    </div>
  </div>
</div>

<style>
  .request-overview {
    display: grid;
    gap: 4px;
    margin-top: 4px;
  }

  .overview-row {
    display: grid;
    grid-template-columns: 84px minmax(0, 1fr);
    align-items: baseline;
    gap: 10px;
  }

  .overview-row--process {
    align-items: start;
  }

  .overview-label {
    color: var(--color-text-muted);
    font-size: 11px;
    font-weight: 600;
    letter-spacing: 0.03em;
    text-transform: uppercase;
  }

  .overview-value {
    min-width: 0;
    color: var(--color-text);
    font-size: 13px;
  }

  .application-value,
  .secret-value {
    font-weight: 600;
  }

  .secret-value {
    overflow-wrap: anywhere;
  }

  @media (max-width: 560px) {
    .overview-row {
      grid-template-columns: 72px minmax(0, 1fr);
      gap: 6px;
    }
  }
</style>
