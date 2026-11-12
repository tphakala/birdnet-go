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
  - LoadingSpinner.svelte  for the loading state

  Body modes: the default is a resizable box (ResizableContainer). With
  `resizable={false}` the table takes its natural height, and `bodyMaxHeight`
  caps it so a long list scrolls under a sticky header while a short list
  stays compact. A body without a sortable column is a tab stop so the arrow
  keys can scroll it, and a region named by `title`. The `notice` snippet
  renders between the header bar and the body in every state (an error above
  rows that are still shown), and the `empty` snippet replaces the default empty state.
  While `loading` is true the body shows a spinner and a visible label
  (`loadingLabel`, default "Loading"), announced through a status region that
  stays in the document.

  This component sorts and filters internally. Consumers declare columns with a
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
  import { textAlignClass, type Column } from './DataTable.types';
  import SortableHeader from '$lib/desktop/components/ui/SortableHeader.svelte';
  import ResizableContainer from '$lib/desktop/components/ui/ResizableContainer.svelte';
  import EmptyState from '$lib/desktop/components/ui/EmptyState.svelte';
  import LoadingSpinner from '$lib/desktop/components/ui/LoadingSpinner.svelte';

  interface Props {
    columns: Column<T>[];
    data: T[];
    loading?: boolean;
    /** Text shown, and announced, while `loading` is true; defaults to the generic loading label. */
    loadingLabel?: string;

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
    /** Rendered between the header bar and the body in every state, e.g. an ErrorAlert. */
    notice?: Snippet;
    /** Replaces the default EmptyState when `data` is empty; may render nothing. */
    empty?: Snippet;
    emptyTitle?: string;
    emptyDescription?: string;
    emptyIcon?: Snippet;
    noResultsMessage?: string;

    // Body
    resizable?: boolean;
    defaultHeight?: number;
    minHeight?: number;
    maxHeight?: number;
    /**
     * CSS length that caps the body height when `resizable` is false, e.g.
     * `min(70vh, 40rem)`. The body then scrolls under the sticky header.
     */
    bodyMaxHeight?: string;

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
    loadingLabel,
    title,
    description,
    count,
    icon,
    headerActions,
    searchable = false,
    searchPlaceholder,
    searchAccessor,
    defaultSortKey,
    notice,
    empty,
    emptyTitle,
    emptyDescription,
    emptyIcon,
    noResultsMessage,
    resizable = true,
    defaultHeight = 512,
    minHeight = 200,
    maxHeight = 800,
    bodyMaxHeight,
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
    if (!col) return filteredData;
    // A column without a sortValue sorts by its raw field value, so a sortable
    // header sorts by the field it is keyed on.
    const sortValue = col.sortValue ?? ((item: T) => sortKey(fieldValue(item, col)));
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

  // The body scrolls sideways whenever the table is wider than the card, and vertically when
  // capped, so without a sort button inside it to hold focus it needs a tab stop of its own
  let bodyNeedsFocus = $derived(!columns.some(c => c.sortable));
  // A focusable body is announced as a region named by the table title
  let scrollRegionLabel = $derived(bodyNeedsFocus && title ? title : undefined);

  // Fall back to the item reference (not the array index) so that re-sorting or
  // filtering reorders existing rows by identity instead of recreating DOM nodes.
  function rowKey(item: T, index: number): string | number | T {
    return keyFn ? keyFn(item, index) : item;
  }

  /** The item's own field for a column, or undefined when the item is not an object. */
  function fieldValue(item: T, column: Column<T>): unknown {
    return typeof item === 'object' && item !== null ? Reflect.get(item, column.key) : undefined;
  }

  /** Display form of a field: numbers kept, null shown as ''. */
  function displayValue(value: unknown): string | number {
    if (typeof value === 'number') return value;
    return value == null ? '' : String(value);
  }

  /** Sort form of a field: dates by time, an invalid date or NaN as '', the rest as displayed. */
  function sortKey(value: unknown): string | number {
    if (value instanceof Date) {
      const time = value.getTime();
      return Number.isNaN(time) ? '' : time;
    }
    const shown = displayValue(value);
    return typeof shown === 'number' && Number.isNaN(shown) ? '' : shown;
  }

  function cellValue(column: Column<T>, item: T, index: number): string | number {
    return column.render ? column.render(item, index) : displayValue(fieldValue(item, column));
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
        class="badge-status-neutral inline-flex items-center px-2 py-0.5 rounded-full text-[0.6875rem] font-medium tabular-nums shrink-0"
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
            class="w-48 pl-8 pr-3 py-1.5 text-xs rounded-lg border border-[var(--border-100)] bg-[var(--surface-100)] focus:outline-none focus:ring-2 focus:ring-[var(--color-primary)]"
          />
        </div>
      {/if}
      {#if headerActions}
        {@render headerActions()}
      {/if}
    </div>
  </div>

  {#if notice}
    <div class="px-4 has-[*]:py-3">
      {@render notice()}
    </div>
  {/if}

  <!-- Body -->
  <!-- The status region stays in the document so a screen reader announces the label when
       loading starts; only its content appears and goes. -->
  <div
    role="status"
    class={cn(loading && 'flex items-center justify-center gap-3 py-12 text-sm text-muted')}
  >
    {#if loading}
      <LoadingSpinner size="md" aria-hidden="true" />
      <span>{loadingLabel ?? t('common.loading')}</span>
    {/if}
  </div>
  {#if loading}
    <!-- The loading state above replaces the rows -->
  {:else if data.length === 0}
    {#if empty}
      {@render empty()}
    {:else}
      <EmptyState title={emptyTitle} description={emptyDescription} icon={emptyIcon} />
    {/if}
  {:else if sortedData.length === 0}
    <div class="text-center py-8 text-muted">
      <p class="text-sm">{noResultsMessage ?? t('dataDisplay.table.noData')}</p>
    </div>
  {:else}
    {#snippet tableMarkup()}
      <table class="w-full text-sm">
        <thead class="sticky top-0 bg-[var(--surface-100)] z-10">
          <tr>
            {#each columns as column (column.key)}
              {#if column.sortable}
                <SortableHeader
                  label={column.header}
                  field={column.key}
                  activeField={sortColumn}
                  direction={sortDirection}
                  onSort={handleSort}
                  align={column.align}
                  className={cn(headerCellClass, column.className)}
                  width={column.width}
                />
              {:else}
                <th
                  scope="col"
                  class={cn(headerCellClass, textAlignClass(column.align), column.className)}
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
                'border-b last:border-b-0 border-[var(--border-100)]/50 transition-colors hover:bg-[var(--hover-overlay)]',
                rowClass?.(item)
              )}
            >
              {#each columns as column (column.key)}
                <td
                  class={cn(
                    'py-2 px-3',
                    textAlignClass(column.align),
                    column.align === 'right' && 'tabular-nums',
                    column.className
                  )}
                >
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
      <ResizableContainer
        {defaultHeight}
        {minHeight}
        {maxHeight}
        scrollTabStop={bodyNeedsFocus}
        scrollLabel={scrollRegionLabel}
      >
        {@render tableMarkup()}
      </ResizableContainer>
    {:else}
      <!-- The body can scroll, so it needs a tab stop for the arrow keys unless a sort
           button inside it can already hold focus. -->
      <!-- svelte-ignore a11y_no_noninteractive_tabindex -->
      <div
        class={cn(
          'overflow-x-auto',
          bodyMaxHeight &&
            'max-h-(--table-body-max-height) overflow-y-auto rounded-b-xl scroll-shadow-y'
        )}
        style:--table-body-max-height={bodyMaxHeight}
        tabindex={bodyNeedsFocus ? 0 : undefined}
        role={scrollRegionLabel ? 'region' : undefined}
        aria-label={scrollRegionLabel}
      >
        {@render tableMarkup()}
      </div>
    {/if}
  {/if}
</div>

<style>
  /* A collapsed table border belongs to the table, not to the sticky header
     cells, so it scrolls away with the rows. An inset shadow on each header cell
     travels with the cell instead. :global reaches the th elements that
     SortableHeader renders, which a scoped th rule would miss. */
  thead :global(th) {
    box-shadow: inset 0 -1px 0 var(--border-100);
  }
</style>
