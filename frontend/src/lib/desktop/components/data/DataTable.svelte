<!--
  DataTable.svelte
  
  A generic, feature-rich data table component for displaying structured data with sorting capabilities.
  Supports TypeScript generics for type-safe column definitions and data handling.
  
  Usage:
  - Detection listings with sortable columns
  - System information displays
  - Any tabular data presentation
  - Admin interfaces requiring data manipulation
  
  Features:
  - Column sorting with visual indicators
  - Loading and error states
  - Customizable styling (striped, hoverable, compact)
  - Empty state handling
  - Responsive design
  - Type-safe with generics
  
  Props:
  - columns: Column<T>[] - Column definitions with sorting config
  - data: T[] - Array of data objects to display
  - loading?: boolean - Shows loading spinner
  - error?: string | null - Error message display
  - emptyMessage?: string - Message when no data
  - Various styling options (striped, hoverable, compact, fullWidth)
-->
<script lang="ts" generics="T">
  import { cn } from '$lib/utils/cn';
  import type { Snippet } from 'svelte';
  import type { HTMLAttributes } from 'svelte/elements';
  import type { Column, SortDirection } from './DataTable.types';

  interface Props<T> extends Omit<HTMLAttributes<HTMLElement>, 'data'> {
    columns: Column<T>[];
    data: T[];
    loading?: boolean;
    error?: string | null;
    emptyMessage?: string;
    striped?: boolean;
    hoverable?: boolean;
    compact?: boolean;
    fullWidth?: boolean;
    className?: string;
    onSort?: (_column: string, _direction: SortDirection) => void;
    sortColumn?: string | null;
    sortDirection?: SortDirection;
    renderCell?: Snippet<[{ column: Column<T>; item: T; index: number }]>;
    renderEmpty?: Snippet;
    renderLoading?: Snippet;
    renderError?: Snippet<[{ error: string }]>;
  }

  let {
    columns,
    data = [],
    loading = false,
    error = null,
    emptyMessage = 'No data available',
    striped = true,
    hoverable = true,
    compact = false,
    fullWidth = true,
    className = '',
    onSort,
    sortColumn = null,
    sortDirection = null,
    renderCell,
    renderEmpty,
    renderLoading,
    renderError,
    ...rest
  }: Props<T> = $props() as Props<T>;

  function handleSort(column: Column<T>) {
    if (!column.sortable || !onSort) return;

    let newDirection: SortDirection = 'asc';
    if (sortColumn === column.key) {
      if (sortDirection === 'asc') newDirection = 'desc';
      else if (sortDirection === 'desc') newDirection = null;
    }

    onSort(column.key, newDirection);
  }

  function getCellValue(column: Column<T>, item: T, index: number): string | number {
    if (column.render) {
      return column.render(item, index);
    }
    if (column.renderHtml) {
      return column.renderHtml(item, index);
    }
    // @ts-expect-error - Dynamic property access
    return item[column.key] ?? '';
  }

  function getAlignClass(align?: string): string {
    switch (align) {
      case 'center':
        return 'text-center';
      case 'right':
        return 'text-right';
      default:
        return 'text-left';
    }
  }

  const tableClasses = cn(
    'table',
    {
      'table-zebra': striped,
      'table-compact': compact,
      'w-full': fullWidth,
    },
    className
  );
</script>

<div class="overflow-x-auto">
  {#if loading}
    {#if renderLoading}
      {@render renderLoading()}
    {:else}
      <div class="flex justify-center py-8">
        <span class="loading loading-spinner loading-lg text-primary"></span>
      </div>
    {/if}
  {:else if error}
    {#if renderError}
      {@render renderError({ error })}
    {:else}
      <div class="alert alert-error">
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="h-6 w-6 shrink-0 stroke-current"
          fill="none"
          viewBox="0 0 24 24"
        >
          <path
            stroke-linecap="round"
            stroke-linejoin="round"
            stroke-width="2"
            d="M10 14l2-2m0 0l2-2m-2 2l-2-2m2 2l2 2m7-2a9 9 0 11-18 0 9 9 0 0118 0z"
          />
        </svg>
        <span>{error}</span>
      </div>
    {/if}
  {:else}
    <table class={tableClasses} {...rest}>
      <thead>
        <tr>
          {#each columns as column}
            <th
              scope="col"
              class={cn(getAlignClass(column.align), column.className)}
              style:width={column.width}
            >
              {#if column.sortable && onSort}
                <button
                  type="button"
                  class="inline-flex items-center gap-1 hover:text-primary transition-colors"
                  onclick={() => handleSort(column)}
                >
                  {column.header}
                  <span class="inline-block w-4">
                    {#if sortColumn === column.key}
                      {#if sortDirection === 'asc'}
                        <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                          <path
                            stroke-linecap="round"
                            stroke-linejoin="round"
                            stroke-width="2"
                            d="M5 15l7-7 7 7"
                          />
                        </svg>
                      {:else if sortDirection === 'desc'}
                        <svg class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                          <path
                            stroke-linecap="round"
                            stroke-linejoin="round"
                            stroke-width="2"
                            d="M19 9l-7 7-7-7"
                          />
                        </svg>
                      {/if}
                    {/if}
                  </span>
                </button>
              {:else}
                {column.header}
              {/if}
            </th>
          {/each}
        </tr>
      </thead>
      <tbody>
        {#if data.length === 0}
          <tr>
            <td colspan={columns.length} class="text-center py-6 text-base-content/70">
              {#if renderEmpty}
                {@render renderEmpty()}
              {:else}
                {emptyMessage}
              {/if}
            </td>
          </tr>
        {:else}
          {#each data as item, index}
            <tr class={hoverable ? 'hover:bg-base-200/50 transition-colors' : ''}>
              {#each columns as column}
                <td class={cn(getAlignClass(column.align), column.className)}>
                  {#if renderCell}
                    {@render renderCell({ column, item, index })}
                  {:else if column.renderHtml}
                    {@html column.renderHtml(item, index)}
                  {:else}
                    {getCellValue(column, item, index)}
                  {/if}
                </td>
              {/each}
            </tr>
          {/each}
        {/if}
      </tbody>
    </table>
  {/if}
</div>
