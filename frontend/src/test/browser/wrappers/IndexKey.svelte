<!--
  Tests: {#each items as item, index (index)} pattern
  Reproduces: RTSPUrlInput urls (line 116)
  Issue: Using index as key causes incorrect DOM recycling when items are
  removed from the middle of the list. The last item's DOM is always removed
  instead of the correct one, leading to stale input values.
-->
<script lang="ts">
  interface Props {
    items: string[];
    onremove?: (_index: number) => void;
  }

  let { items, onremove }: Props = $props();
</script>

<div data-testid="index-key-list">
  {#each items as item, index (index)}
    <div data-testid="index-item" class="item-row">
      <input type="text" value={item} data-testid="item-input" readonly />
      <button type="button" data-testid="remove-btn" onclick={() => onremove?.(index)}>
        Remove
      </button>
    </div>
  {/each}
</div>
