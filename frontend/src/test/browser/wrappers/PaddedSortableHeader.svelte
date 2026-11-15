<!--
  Tests: SortableHeader inside a table whose header cell carries padding
  Reproduces: a sort button that is smaller than its cell, so a click in the padding does nothing
-->
<script lang="ts">
  import SortableHeader from '$lib/desktop/components/ui/SortableHeader.svelte';

  interface Props {
    onSort: (_field: string) => void;
    /** Extra classes on the header cell, as a table passes through className. */
    extraClass?: string;
  }

  let { onSort, extraClass = '' }: Props = $props();
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
        className="padded-header {extraClass}"
      />
    </tr>
  </thead>
</table>

<style>
  :global(.padded-header) {
    padding: 16px 24px;
  }

  /* Stands in for the Tailwind sticky utility, which the browser harness does not load */
  :global(.sticky) {
    position: sticky;
    top: 0;
  }
</style>
