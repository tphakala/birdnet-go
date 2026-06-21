<!--
  SortableDataTable.svelte

  A generic, card-wrapped data table with built-in client-side sorting and
  search. Owns the chrome that settings tables on the same page would otherwise
  duplicate: a header bar (icon + title + count + search + trailing actions), a
  sticky-header scroll body, and loading / empty / no-results states.

  Composes existing primitives:
  - SortableHeader.svelte  for sortable column headers
  - ResizableContainer.svelte for the sticky-header scroll body
  - EmptyState.svelte      for the empty state

  Unlike the controlled DataTable.svelte (which delegates sorting to the parent),
  this component sorts and filters internally. Consumers declare columns with a
  `sortValue` accessor, a `searchAccessor`, and a `renderCell` snippet for custom
  cell content (bars, badges, buttons).

  Uses the system-page surface tokens (var(--surface-100), var(--border-100),
  text-muted) so it matches SpeciesTable's design language.

  @component
-->
<script lang="ts" generics="T">
  import type { Snippet } from 'svelte';
  import { Search } from '@lucide/svelte';
  import { t } from '$lib/i18n';
  import { cn } from '$lib/utils/cn';
  import type { Column } from './DataTable.types';
  import SortableHeader from '$lib/desktop/components/ui/SortableHeader.svelte';
  import ResizableContainer from '$lib/desktop/components/ui/ResizableContainer.svelte';
  import EmptyState from '$lib/desktop/components/ui/EmptyState.svelte';

  interface Props {
    columns: Column<T>[];
    data: T[];
    loading?: boolean;

    // Header bar
    title?: string;
    description?: string;
    /** Count badge value; defaults to the filtered row count. */
    count?: number;
    icon?: Snippet;
    headerActions?: Snippet;

    // Search
    searchable?: boolean;
    searchPlaceholder?: string;
    /** Returns the searchable text for an item; omit to disable search. */
    searchAccessor?: (_item: T) => string;

    // Sort
    /** Column key sorted on first render. */
    defaultSortKey?: string;

    // States
    emptyTitle?: string;
    emptyDescription?: string;
    emptyIcon?: Snippet;
    noResultsMessage?: string;

    // Body
    resizable?: boolean;
    defaultHeight?: number;
    minHeight?: number;
    maxHeight?: number;

    // Rows
    keyFn?: (_item: T, _index: number) => string | number;
    /** Extra classes applied per row, e.g. for an edit highlight. */
    rowClass?: (_item: T) => string;
    renderCell?: Snippet<[{ column: Column<T>; item: T; index: number }]>;
  }

  let {
    columns = [],
    data = [],
    loading = false,
    title,
    description,
    count,
    icon,
    headerActions,
    searchable = false,
    searchPlaceholder,
    searchAccessor,
    defaultSortKey,
    emptyTitle,
    emptyDescription,
    emptyIcon,
    noResultsMessage,
    resizable = true,
    defaultHeight = 512,
    minHeight = 200,
    maxHeight = 800,
    keyFn,
    rowClass,
    renderCell,
  }: Props = $props();

  function initialDirection(key: string | undefined): 'asc' | 'desc' {
    const col = columns.find(c => c.key === key);
    return col?.defaultDirection ?? 'asc';
  }

  // svelte-ignore state_referenced_locally
  let sortColumn = $state<string>(defaultSortKey ?? '');
  // svelte-ignore state_referenced_locally
  let sortDirection = $state<'asc' | 'desc'>(initialDirection(defaultSortKey));
  let searchQuery = $state('');

  function handleSort(field: string): void {
    if (sortColumn === field) {
      sortDirection = sortDirection === 'asc' ? 'desc' : 'asc';
    } else {
      sortColumn = field;
      sortDirection = initialDirection(field);
    }
  }

  let filteredData = $derived.by(() => {
    if (!searchable || !searchAccessor) return data;
    const query = searchQuery.trim().toLowerCase();
    if (!query) return data;
    const accessor = searchAccessor;
    return data.filter(item => accessor(item).toLowerCase().includes(query));
  });

  let sortedData = $derived.by(() => {
    const col = columns.find(c => c.key === sortColumn);
    if (!col?.sortValue) return filteredData;
    const sortValue = col.sortValue;
    const direction = sortDirection === 'asc' ? 1 : -1;
    return [...filteredData].sort((a, b) => {
      const av = sortValue(a);
      const bv = sortValue(b);
      let cmp: number;
      if (typeof av === 'number' && typeof bv === 'number') {
        cmp = av - bv;
      } else {
        cmp = String(av).localeCompare(String(bv));
      }
      return cmp * direction;
    });
  });

  let displayCount = $derived(count ?? filteredData.length);

  // Fall back to the item reference (not the array index) so that re-sorting or
  // filtering reorders existing rows by identity instead of recreating DOM nodes.
  function rowKey(item: T, index: number): string | number | T {
    return keyFn ? keyFn(item, index) : item;
  }

  function cellValue(column: Column<T>, item: T, index: number): string | number {
    if (column.render) return column.render(item, index);
    const value = (item as Record<string, unknown>)[column.key];
    return value == null ? '' : String(value);
  }

  function alignClass(align?: string): string {
    switch (align) {
      case 'center':
        return 'text-center';
      case 'right':
        return 'text-right';
      default:
        return 'text-left';
    }
  }

  const headerCellClass = 'py-2 px-3 text-xs font-medium text-muted';
</script>

<div class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl shadow-sm">
  <!-- Header bar -->
  <div
    class="flex items-center justify-between gap-3 px-4 py-3 border-b border-[var(--border-100)]"
  >
    <div class="flex items-center gap-3 min-w-0">
      {#if icon || title}
        <div class="flex items-center gap-2 min-w-0">
          {#if icon}
            {@render icon()}
          {/if}
          {#if title}
            <div class="min-w-0">
              <h3 class="text-xs font-semibold uppercase tracking-wider text-muted truncate">
                {title}
              </h3>
              {#if description}
                <p class="text-xs text-muted mt-0.5 truncate">{description}</p>
              {/if}
            </div>
          {/if}
        </div>
      {/if}
      <span
        class="inline-flex items-center px-2 py-0.5 rounded-full text-[10px] font-medium bg-slate-500/10 text-muted shrink-0"
      >
        {displayCount}
      </span>
    </div>

    <div class="flex items-center gap-2 shrink-0">
      {#if searchable && searchAccessor}
        <div class="relative">
          <Search
            class="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-muted"
            aria-hidden="true"
          />
          <input
            type="text"
            bind:value={searchQuery}
            placeholder={searchPlaceholder ?? t('common.search')}
            aria-label={searchPlaceholder ?? t('common.search')}
            autocomplete="off"
            data-1p-ignore
            data-lpignore="true"
            data-form-type="other"
            class="w-48 pl-8 pr-3 py-1.5 text-xs rounded-lg border border-[var(--border-100)] bg-[var(--surface-100)] focus:outline-none focus:ring-2 focus:ring-blue-500/40"
          />
        </div>
      {/if}
      {#if headerActions}
        {@render headerActions()}
      {/if}
    </div>
  </div>

  <!-- Body -->
  {#if loading}
    <div class="flex items-center justify-center py-12">
      <div
        class="inline-block w-8 h-8 border-4 border-[var(--surface-300)] border-t-[var(--color-primary)] rounded-full animate-spin"
        role="status"
        aria-label={t('common.loading')}
      ></div>
    </div>
  {:else if data.length === 0}
    <EmptyState title={emptyTitle} description={emptyDescription} icon={emptyIcon} />
  {:else if sortedData.length === 0}
    <div class="text-center py-8 text-muted">
      <p class="text-sm">{noResultsMessage ?? t('dataDisplay.table.noData')}</p>
    </div>
  {:else}
    {#snippet tableMarkup()}
      <table class="w-full text-sm">
        <thead class="sticky top-0 bg-[var(--surface-100)] z-10">
          <tr class="border-b border-[var(--border-100)]">
            {#each columns as column (column.key)}
              {#if column.sortable}
                <SortableHeader
                  label={column.header}
                  field={column.key}
                  activeField={sortColumn}
                  direction={sortDirection}
                  onSort={handleSort}
                  className={cn(headerCellClass, alignClass(column.align), column.className)}
                  width={column.width}
                />
              {:else}
                <th
                  scope="col"
                  class={cn(headerCellClass, alignClass(column.align), column.className)}
                  style:width={column.width}
                >
                  {column.header}
                </th>
              {/if}
            {/each}
          </tr>
        </thead>
        <tbody>
          {#each sortedData as item, index (rowKey(item, index))}
            <tr
              class={cn(
                'border-b last:border-b-0 border-[var(--border-100)]/50 transition-colors hover:bg-black/[0.02] dark:hover:bg-white/[0.02]',
                rowClass?.(item)
              )}
            >
              {#each columns as column (column.key)}
                <td class={cn('py-2 px-3', alignClass(column.align), column.className)}>
                  {#if renderCell}
                    {@render renderCell({ column, item, index })}
                  {:else}
                    {cellValue(column, item, index)}
                  {/if}
                </td>
              {/each}
            </tr>
          {/each}
        </tbody>
      </table>
    {/snippet}

    {#if resizable}
      <ResizableContainer {defaultHeight} {minHeight} {maxHeight}>
        {@render tableMarkup()}
      </ResizableContainer>
    {:else}
      <div class="overflow-x-auto">
        {@render tableMarkup()}
      </div>
    {/if}
  {/if}
</div>
