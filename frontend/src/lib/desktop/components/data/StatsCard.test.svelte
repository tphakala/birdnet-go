<script>
  import StatsCard from './StatsCard.svelte';

  let { showIcon = false, loading = false } = $props();
</script>

<StatsCard value="123" label="Test Stats" {loading}>
  {#snippet icon()}
    {#if showIcon}
      <svg
        data-testid="custom-icon"
        class="h-6 w-6"
        fill="none"
        viewBox="0 0 24 24"
        stroke="currentColor"
      >
        <path
          stroke-linecap="round"
          stroke-linejoin="round"
          stroke-width="2"
          d="M13 7h8m0 0v8m0-8l-8 8-4-4-6 6"
        />
      </svg>
    {/if}
  {/snippet}
</StatsCard>
