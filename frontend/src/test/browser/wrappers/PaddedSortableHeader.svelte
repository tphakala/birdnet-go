<!--
  Tests: SortableHeader inside a table whose header cell carries padding
  Reproduces: a sort button that is smaller than its cell, so a click in the padding does nothing
-->
<script lang="ts">
  import SortableHeader from '$lib/desktop/components/ui/SortableHeader.svelte';

  interface Props {
    onSort: (_field: string) => void;
  }

  let { onSort }: Props = $props();
</script>

<table>
  <thead>
    <tr>
      <SortableHeader
        label="Score"
        field="score"
        activeField=""
        direction="asc"
        {onSort}
        className="padded-header"
      />
    </tr>
  </thead>
</table>

<style>
  :global(.padded-header) {
    padding: 16px 24px;
  }
</style>
