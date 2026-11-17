<!--
  Test harness: drives ExpandableText's lines prop from its own signal, as a parent would.
  A rerender through the testing library assigns one new props object, so every prop read re-runs,
  `text` included, even when only `lines` changes.
-->
<script lang="ts">
  import ExpandableText from './ExpandableText.svelte';

  let { text }: { text: string } = $props();
  let lines = $state<2 | 3 | 4>(4);
</script>

<ExpandableText {text} {lines} />
<button type="button" onclick={() => (lines = 2)}>narrow</button>
