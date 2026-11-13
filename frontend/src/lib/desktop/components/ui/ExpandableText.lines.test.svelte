<!--
  Test harness: drives ExpandableText's lines prop from its own signal, as a parent would.
  A rerender through the testing library replaces all props together, which also changes `text`.
-->
<script lang="ts">
  import ExpandableText from './ExpandableText.svelte';

  let { text }: { text: string } = $props();
  let lines = $state<2 | 3 | 4>(4);
</script>

<ExpandableText {text} {lines} />
<button type="button" onclick={() => (lines = 2)}>narrow</button>
