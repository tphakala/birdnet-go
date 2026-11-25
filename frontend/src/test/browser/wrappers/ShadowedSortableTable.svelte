<!--
  Tests: SortableDataTable header cells with a caller shadow utility in column.className
  Reproduces: the table's own header shadow overriding a utility the caller passes
-->
<script lang="ts">
  import SortableDataTable from '$lib/desktop/components/data/SortableDataTable.svelte';
  import type { Column } from '$lib/desktop/components/data/DataTable.types';

  interface Row {
    name: string;
    score: number;
  }

  const columns: Column<Row>[] = [
    { key: 'name', header: 'Name', sortable: true },
    { key: 'score', header: 'Score', sortable: true, className: 'caller-shadow' },
  ];
  const data: Row[] = [{ name: 'alpha', score: 1 }];
</script>

<SortableDataTable {columns} {data} resizable={false} />

<style>
  /* Same layer order the app stylesheet declares first, with a stand-in for a Tailwind shadow
     utility (the browser harness does not load Tailwind) in the layer utilities live in */
  @layer theme, base, components, utilities;

  @layer utilities {
    :global(.caller-shadow) {
      box-shadow: 0 0 0 3px rgb(255, 0, 0);
    }
  }
</style>
