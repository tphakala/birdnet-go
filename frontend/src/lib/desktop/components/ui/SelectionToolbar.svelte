<!--
  SelectionToolbar.svelte

  A reusable sticky toolbar for multiselect list views. Shows selection count,
  a "select all matching" banner, and an action slot for caller-provided buttons.

  Props:
  - selectedCount: number - Number of currently selected items
  - totalCount: number - Total matching items across all pages
  - allSelected: boolean - Whether all matching items are selected
  - allOnPageSelected: boolean - Whether all items on the current page are selected
  - pageSize: number - Items per page
  - onSelectAll: () => void - Callback to select all matching items
  - onClear: () => void - Callback to clear selection
  - actions?: Snippet - Slot for action buttons
  - className?: string - Additional CSS classes
-->
<script lang="ts">
  import { X } from '@lucide/svelte';
  import { cn } from '$lib/utils/cn';
  import { t } from '$lib/i18n';
  import type { Snippet } from 'svelte';

  interface Props {
    selectedCount: number;
    totalCount: number;
    allSelected: boolean;
    allOnPageSelected: boolean;
    pageSize: number;
    onSelectAll: () => void;
    onClear: () => void;
    actions?: Snippet;
    className?: string;
  }

  let {
    selectedCount,
    totalCount,
    allSelected,
    allOnPageSelected,
    pageSize,
    onSelectAll,
    onClear,
    actions,
    className = '',
  }: Props = $props();

  const showSelectAllBanner = $derived(allOnPageSelected && !allSelected && totalCount > pageSize);
</script>

<div
  class={cn(
    'sticky top-0 z-20 flex flex-wrap items-center gap-3 px-4 py-2',
    'bg-[var(--color-primary)]/10 border-b border-[var(--color-primary)]/20',
    'transition-transform duration-200 ease-out',
    className
  )}
  role="toolbar"
  aria-label={t('detections.selection.toolbarLabel')}
>
  <div class="flex items-center gap-2">
    <span class="text-sm font-medium text-[var(--color-base-content)]" aria-live="polite">
      {#if allSelected}
        {t('detections.selection.allSelected', { count: totalCount })}
      {:else}
        {t('detections.selection.nSelected', { count: selectedCount })}
      {/if}
    </span>
    <button
      type="button"
      class="inline-flex items-center justify-center w-6 h-6 rounded-full
             text-[var(--color-base-content)]/60 hover:text-[var(--color-base-content)]
             hover:bg-[var(--color-base-300)] transition-colors"
      onclick={onClear}
      aria-label={t('detections.selection.clear')}
    >
      <X class="size-3.5" />
    </button>
  </div>

  {#if showSelectAllBanner}
    <div class="text-sm text-[var(--color-base-content)]/70">
      <button
        type="button"
        class="underline hover:text-[var(--color-primary)] transition-colors cursor-pointer"
        onclick={onSelectAll}
        aria-label={t('detections.selection.selectAllMatching', { count: totalCount })}
      >
        {t('detections.selection.selectAllMatching', { count: totalCount })}
      </button>
    </div>
  {/if}

  {#if actions}
    <div class="ml-auto flex items-center gap-1">
      {@render actions()}
    </div>
  {/if}
</div>
