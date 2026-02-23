<!--
  Fixed version: {#each items as item (item)} using item value as key
  This correctly tracks which DOM element belongs to which item,
  so removing from the middle works correctly.
-->
<script lang="ts">
  interface Props {
    items: string[];
    onremove?: (_index: number) => void;
  }

  let { items, onremove }: Props = $props();
</script>

<div data-testid="index-key-list">
  {#each items as item, index (item)}
    <div data-testid="index-item" class="item-row">
      <input type="text" value={item} data-testid="item-input" readonly />
      <button type="button" data-testid="remove-btn" onclick={() => onremove?.(index)}>
        Remove
      </button>
    </div>
  {/each}
</div>
