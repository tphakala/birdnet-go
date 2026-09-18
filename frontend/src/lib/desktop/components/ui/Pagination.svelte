<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import type { HTMLAttributes } from 'svelte/elements';
  import { ChevronLeft, ChevronRight } from '@lucide/svelte';
  import { t } from '$lib/i18n';

  interface Props extends HTMLAttributes<HTMLElement> {
    currentPage?: number;
    totalPages?: number;
    onPageChange?: (_page: number) => void;
    disabled?: boolean;
    /**
     * Render numbered page buttons. Turning these off leaves only the previous
     * and next arrows, which makes a long list navigable one page at a time and
     * nothing else -- so keep them unless the surrounding UI offers another way
     * to jump to a page.
     */
    showPageNumbers?: boolean;
    /**
     * Render a "Page 3 of 9" label in place of the numbers. Off when the caller
     * states the position itself, as the detections list does with its
     * "Showing 51 to 75 of 214" line.
     */
    showPageInfo?: boolean;
    maxVisiblePages?: number;
    className?: string;
  }

  let {
    currentPage = 1,
    totalPages = 1,
    onPageChange = () => {},
    disabled = false,
    showPageNumbers = true,
    showPageInfo = true,
    maxVisiblePages = 5,
    className = '',
    ...rest
  }: Props = $props();

  // Calculate visible page numbers
  let visiblePages = $derived.by(() => {
    const pages: number[] = [];
    let start = Math.max(1, currentPage - Math.floor(maxVisiblePages / 2));
    let end = Math.min(totalPages, start + maxVisiblePages - 1);

    // Adjust start if we're near the end
    if (end - start < maxVisiblePages - 1) {
      start = Math.max(1, end - maxVisiblePages + 1);
    }

    for (let i = start; i <= end; i++) {
      pages.push(i);
    }

    return pages;
  });

  function goToPage(page: number) {
    if (page >= 1 && page <= totalPages && page !== currentPage && !disabled) {
      onPageChange(page);
    }
  }

  function previousPage() {
    goToPage(currentPage - 1);
  }

  function nextPage() {
    goToPage(currentPage + 1);
  }
</script>

<nav
  class={cn('flex items-center justify-center gap-2', className)}
  aria-label={t('dataDisplay.pagination.ariaLabel')}
  {...rest}
>
  <div class="join">
    <!-- Previous button -->
    <button
      class="join-item btn btn-sm"
      onclick={previousPage}
      disabled={disabled || currentPage === 1}
      aria-label={t('dataDisplay.pagination.goToPreviousPage')}
    >
      <ChevronLeft class="size-4" />
    </button>

    <!-- Page numbers -->
    {#if showPageNumbers && totalPages > 1}
      {@const pages = visiblePages}
      {@const firstPage = pages[0]}
      {@const lastPage = pages[pages.length - 1]}
      {#if firstPage > 1}
        <button class="join-item btn btn-sm" onclick={() => goToPage(1)} {disabled}> 1 </button>
        {#if firstPage > 2}
          <button class="join-item btn btn-sm btn-disabled" disabled> ... </button>
        {/if}
      {/if}

      {#each pages as page (page)}
        <button
          class={cn('join-item btn btn-sm', { 'btn-active': page === currentPage })}
          onclick={() => goToPage(page)}
          {disabled}
          aria-label={t('dataDisplay.pagination.goToPage', { page })}
          aria-current={page === currentPage ? 'page' : undefined}
        >
          {page}
        </button>
      {/each}

      {#if lastPage < totalPages}
        {#if lastPage < totalPages - 1}
          <button class="join-item btn btn-sm btn-disabled" disabled> ... </button>
        {/if}
        <button class="join-item btn btn-sm" onclick={() => goToPage(totalPages)} {disabled}>
          {totalPages}
        </button>
      {/if}
    {:else if showPageInfo && totalPages > 0}
      <button class="join-item btn btn-sm btn-disabled" disabled>
        {t('dataDisplay.pagination.page', { current: currentPage, total: totalPages })}
      </button>
    {/if}

    <!-- Next button -->
    <button
      class="join-item btn btn-sm"
      onclick={nextPage}
      disabled={disabled || currentPage === totalPages}
      aria-label={t('dataDisplay.pagination.goToNextPage')}
    >
      <ChevronRight class="size-4" />
    </button>
  </div>
</nav>
