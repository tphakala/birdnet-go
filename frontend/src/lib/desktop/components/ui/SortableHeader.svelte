<!--
  SortableHeader.svelte

  A reusable sortable table header cell. Renders a <th> with click-to-sort
  behavior and visual sort direction indicators.

  Matches the sort UI pattern from DataTable.svelte: ChevronUp/ChevronDown
  icons only when actively sorted, empty w-4 spacer when inactive.

  Props:
  - label: string - Column header text
  - field: string - Field identifier for sort state
  - activeField: string - Currently active sort field
  - direction: 'asc' | 'desc' - Current sort direction
  - onSort: (field: string) => void - Sort toggle callback
  - className?: string - Additional CSS classes on <th>
  - srOnly?: string - Screen reader text override
-->
<script lang="ts">
  import { ChevronUp, ChevronDown, ChevronsUpDown } from '@lucide/svelte';
  import { cn } from '$lib/utils/cn';
  import { t } from '$lib/i18n';

  interface Props {
    label: string;
    field: string;
    activeField: string;
    direction: 'asc' | 'desc';
    onSort: (_field: string) => void;
    className?: string;
    srOnly?: string;
  }

  let { label, field, activeField, direction, onSort, className = '', srOnly }: Props = $props();

  const isActive = $derived(field === activeField);
</script>

<th
  scope="col"
  class={cn(className)}
  aria-sort={isActive ? (direction === 'asc' ? 'ascending' : 'descending') : 'none'}
>
  <button
    type="button"
    class="inline-flex items-center gap-1 hover:text-primary transition-colors"
    onclick={() => onSort(field)}
    aria-label={srOnly ?? t('dataDisplay.table.sortBy', { column: label })}
    data-testid={`sort-${field}`}
  >
    {label}
    <span class="inline-block w-4" aria-hidden="true">
      {#if isActive}
        {#if direction === 'asc'}
          <ChevronUp class="size-4" />
        {:else}
          <ChevronDown class="size-4" />
        {/if}
      {:else}
        <ChevronsUpDown class="size-3 opacity-30" />
      {/if}
    </span>
  </button>
</th>
