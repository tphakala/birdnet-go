<script lang="ts">
  import UseShortcutsHarness from './UseShortcutsHarness.test.svelte';

  // Test host that remounts the owner, as stepping between detections does
  let { generation = 0, mounted = true }: { generation?: number; mounted?: boolean } = $props();
</script>

{#if mounted}
  {#key generation}
    <UseShortcutsHarness />
  {/key}
{/if}
