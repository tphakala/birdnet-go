<!--
  Tests: {#each items as item (item)} pattern (a bare string value used as the key)
  Reproduces: any keyed list that uses a bare string value as its {#each} key
  Bug: When the items array contains duplicate strings, Svelte throws an each_key_duplicate error
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
