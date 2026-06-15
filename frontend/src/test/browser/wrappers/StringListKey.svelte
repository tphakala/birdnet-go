<!--
  Tests: {#each items as item (item)} pattern
  Reproduces: SubnetInput subnets (line 179) and similar string-keyed lists
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
