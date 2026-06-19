<!-- Test fixture: a trivial chart component for ChartCard tests (no D3). -->
<script lang="ts">
  interface Props {
    data?: unknown[];
    /** Optional callback wired by a test's mapProps (e.g. to simulate a brush). */
    trigger?: () => void;
  }
  let { data = [], trigger }: Props = $props();
</script>

<div data-testid="stub-chart" data-count={Array.isArray(data) ? data.length : 0}>stub chart</div>
{#if trigger}
  <button type="button" data-testid="stub-trigger" onclick={() => trigger?.()}>trigger</button>
{/if}
