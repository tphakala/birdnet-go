<!--
  Tests: {#each items as item (item)} pattern
  Reproduces: SpeciesManager predictions (line 272) and displaySpecies (line 295),
              SubnetInput subnets (line 179)
  Bug: When items array contains duplicate strings, Svelte emits each_key_duplicate warning
-->
<script lang="ts">
  interface Props {
    items: string[];
  }

  let { items }: Props = $props();
</script>

<ul data-testid="string-list">
  {#each items as item (item)}
    <li data-testid="list-item">{item}</li>
  {/each}
</ul>
