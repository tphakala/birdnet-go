<!--
  SortableHeader.svelte

  A reusable sortable table header cell. Renders a <th> with click-to-sort
  behavior and visual sort direction indicators.

  ChevronUp/ChevronDown icons only when actively sorted, empty w-4 spacer when
  inactive.

  The unsorted indicator is drawn at 80% of the header text color: header text
  at 4.5:1 or more leaves it at 3:1 or more, so it stays visible without
  competing with the active column's chevron.

  Props:
  - label: string - Column header text
  - field: string - Field identifier for sort state
  - activeField: string - Currently active sort field
  - direction: 'asc' | 'desc' - Current sort direction
  - onSort: (field: string) => void - Sort toggle callback
  - align?: 'left' | 'center' | 'right' - Text alignment of the cell; omit it to
    leave alignment to className or the table's own styles. Right-aligned
    headers draw the indicator before the label, so the label's right edge
    lines up with right-aligned cell values.
  - className?: string - Additional CSS classes on <th>
  - srOnly?: string - Screen reader text override
  - width?: string - Optional fixed column width (applied as inline style on <th>)
-->
<script lang="ts" module>
  export type ColumnAlign = 'left' | 'center' | 'right';

  /** Tailwind text-align class for a column alignment; unset means left. */
  export function textAlignClass(align?: ColumnAlign): string {
    if (align === 'right') return 'text-right';
    if (align === 'center') return 'text-center';
    return 'text-left';
  }
</script>

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
    align?: ColumnAlign;
    className?: string;
    srOnly?: string;
    width?: string;
  }

  let {
    label,
    field,
    activeField,
    direction,
    onSort,
    align,
    className = '',
    srOnly,
    width,
  }: Props = $props();

  const alignClass = $derived(align && textAlignClass(align));

  const isActive = $derived(field === activeField);
</script>

<th
  scope="col"
  class={cn(alignClass, className)}
  style:width
  aria-sort={isActive ? (direction === 'asc' ? 'ascending' : 'descending') : 'none'}
>
  <button
    type="button"
    class="inline-flex items-center gap-1 [text-align:inherit] hover:text-[var(--color-primary)] transition-colors"
    onclick={() => onSort(field)}
    aria-label={srOnly ?? t('dataDisplay.table.sortBy', { column: label })}
    data-testid={`sort-${field}`}
  >
    {#if align === 'right'}
      {@render indicator()}
    {/if}
    <!-- Baseline-aligned so the button keeps the label baseline even when the
         indicator comes first. -->
    <span class="self-baseline" data-sort-label>{label}</span>
    {#if align !== 'right'}
      {@render indicator()}
    {/if}
  </button>
</th>

{#snippet indicator()}
  <span class="inline-flex w-4 justify-center" aria-hidden="true">
    {#if isActive}
      {#if direction === 'asc'}
        <ChevronUp class="size-4" />
      {:else}
        <ChevronDown class="size-4" />
      {/if}
    {:else}
      <ChevronsUpDown class="size-3 opacity-80" />
    {/if}
  </span>
{/snippet}
