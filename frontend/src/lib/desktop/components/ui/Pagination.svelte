<script lang="ts">
  import { cn } from '$lib/utils/cn';
  import type { HTMLAttributes } from 'svelte/elements';

  interface Props extends HTMLAttributes<HTMLElement> {
    currentPage?: number;
    totalPages?: number;
    onPageChange?: (_page: number) => void;
    disabled?: boolean;
    showPageInfo?: boolean;
    maxVisiblePages?: number;
    className?: string;
  }

  let {
    currentPage = 1,
    totalPages = 1,
    onPageChange = () => {},
    disabled = false,
    showPageInfo = true,
    maxVisiblePages = 5,
    className = '',
    ...rest
  }: Props = $props();

  // Calculate visible page numbers
  let visiblePages = $derived(() => {
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
  aria-label="Pagination Navigation"
  {...rest}
>
  <div class="join">
    <!-- Previous button -->
    <button
      class="join-item btn btn-sm"
      onclick={previousPage}
      disabled={disabled || currentPage === 1}
      aria-label="Go to previous page"
    >
      <svg class="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M15 19l-7-7 7-7" />
      </svg>
    </button>

    <!-- Page numbers -->
    {#if showPageInfo && totalPages > 1}
      {#if visiblePages()[0] > 1}
        <button class="join-item btn btn-sm" onclick={() => goToPage(1)} {disabled}> 1 </button>
        {#if visiblePages()[0] > 2}
          <button class="join-item btn btn-sm btn-disabled" disabled> ... </button>
        {/if}
      {/if}

      {#each visiblePages() as page}
        <button
          class={cn('join-item btn btn-sm', { 'btn-active': page === currentPage })}
          onclick={() => goToPage(page)}
          {disabled}
          aria-label={`Go to page ${page}`}
          aria-current={page === currentPage ? 'page' : undefined}
        >
          {page}
        </button>
      {/each}

      {#if visiblePages()[visiblePages().length - 1] < totalPages}
        {#if visiblePages()[visiblePages().length - 1] < totalPages - 1}
          <button class="join-item btn btn-sm btn-disabled" disabled> ... </button>
        {/if}
        <button class="join-item btn btn-sm" onclick={() => goToPage(totalPages)} {disabled}>
          {totalPages}
        </button>
      {/if}
    {:else if showPageInfo}
      <button class="join-item btn btn-sm btn-disabled" disabled>
        Page {currentPage} of {totalPages}
      </button>
    {/if}

    <!-- Next button -->
    <button
      class="join-item btn btn-sm"
      onclick={nextPage}
      disabled={disabled || currentPage === totalPages}
      aria-label="Go to next page"
    >
      <svg class="h-4 w-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
        <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M9 5l7 7-7 7" />
      </svg>
    </button>
  </div>
</nav>
