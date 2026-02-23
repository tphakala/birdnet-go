<!--
  Tests: {#each groups as group (group.category)} pattern
  Reproduces: SpeciesSelector filteredSpecies (line 413, 528)
  Bug: When groups have duplicate category names, Svelte emits each_key_duplicate warning
-->
<script lang="ts">
  interface GroupItem {
    id: string;
    name: string;
  }

  interface Group {
    category: string;
    items: GroupItem[];
  }

  interface Props {
    groups: Group[];
  }

  let { groups }: Props = $props();
</script>

<div data-testid="group-list">
  {#each groups as group (group.category)}
    <div data-testid="group">
      <h3>{group.category}</h3>
      {#each group.items as item (item.id)}
        <span data-testid="group-item">{item.name}</span>
      {/each}
    </div>
  {/each}
</div>
