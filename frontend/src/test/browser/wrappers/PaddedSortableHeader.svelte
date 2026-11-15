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

  /* Same layer order the app stylesheet declares first (tailwind.css imports Tailwind, which
     declares it), so the component rule in the components layer competes with the position
     utilities below as it does in the app. */
  @layer theme, base, components, utilities;

  /* Stand-ins for the Tailwind position utilities, which the browser harness does not load,
     in the layer Tailwind puts utilities in. A variant has its own class name, so
     `md:sticky` is not matched by a rule that lists `.sticky`. */
  @layer utilities {
    :global(.sticky) {
      position: sticky;
      top: 0;
    }

    :global(.md\:sticky) {
      position: sticky;
      top: 0;
    }

    :global(.absolute) {
      position: absolute;
    }
  }
</style>
