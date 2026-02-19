<!--
  SortableHeader.svelte

  A reusable sortable table header cell. Renders a <th> with click-to-sort
  behavior and visual sort direction indicators.

  Props:
  - label: string - Column header text
  - field: string - Field identifier for sort state
  - activeField: string - Currently active sort field
  - direction: 'asc' | 'desc' - Current sort direction
  - onSort: (field: string) => void - Sort toggle callback
  - className?: string - Additional CSS classes
  - srOnly?: string - Screen reader text override
-->
<script lang="ts">
  import { ArrowUp, ArrowDown, ArrowUpDown } from '@lucide/svelte';
  import { cn } from '$lib/utils/cn';

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
  class={cn('sortable-header', className)}
  aria-sort={isActive ? (direction === 'asc' ? 'ascending' : 'descending') : 'none'}
>
  <button
    type="button"
    class="sortable-header-btn"
    onclick={() => onSort(field)}
    aria-label={srOnly ?? `Sort by ${label}`}
  >
    <span>{label}</span>
    {#if isActive}
      {#if direction === 'asc'}
        <ArrowUp class="sort-icon sort-icon-active" />
      {:else}
        <ArrowDown class="sort-icon sort-icon-active" />
      {/if}
    {:else}
      <ArrowUpDown class="sort-icon sort-icon-inactive" />
    {/if}
  </button>
</th>

<style>
  .sortable-header {
    user-select: none;
  }

  .sortable-header-btn {
    display: inline-flex;
    align-items: center;
    gap: 0.25rem;
    cursor: pointer;
    background: none;
    border: none;
    padding: 0;
    font: inherit;
    color: inherit;
    white-space: nowrap;
  }

  .sortable-header-btn:hover :global(.sort-icon-inactive) {
    opacity: 0.7;
  }

  .sort-icon {
    width: 0.875rem;
    height: 0.875rem;
    flex-shrink: 0;
  }

  .sort-icon-active {
    opacity: 1;
    color: oklch(var(--p));
  }

  .sort-icon-inactive {
    opacity: 0.35;
    transition: opacity 150ms ease;
  }
</style>
