<script lang="ts">
  import { cn } from '$lib/utils/cn';

  interface Props {
    className?: string;
    placeholder?: string;
    value?: string;
    onSearch?: (_query: string) => void;
    onNavigate?: (_url: string) => void;
    size?: 'sm' | 'md' | 'lg';
    showOnPages?: string[];
    currentPage?: string;
  }

  let {
    className = '',
    placeholder = 'Search detections',
    value = '',
    onSearch,
    onNavigate,
    size = 'sm',
    showOnPages = ['dashboard'],
    currentPage = 'dashboard',
  }: Props = $props();

  let searchQuery = $state(value);
  let searchTimeout: ReturnType<typeof setTimeout> | null = null;
  let isSearching = $state(false);
  let inputRef = $state<HTMLInputElement>();

  // Check if search should be visible on current page
  const isVisible = $derived(showOnPages.includes(currentPage.toLowerCase()));

  // Get size classes
  const sizeClasses = $derived(() => {
    switch (size) {
      case 'lg':
        return {
          input: 'input-lg',
          icon: 'w-6 h-6',
          padding: 'pl-4 pr-12',
        };
      case 'md':
        return {
          input: 'input-md',
          icon: 'w-5 h-5',
          padding: 'pl-3 pr-10',
        };
      default:
        return {
          input: 'input-sm sm:input-md',
          icon: 'w-4 h-4 sm:w-6 sm:h-6',
          padding: 'pl-3 sm:pl-4 pr-10 sm:pr-12',
        };
    }
  });

  // Handle input change with debounce
  function handleInput(event: Event) {
    const target = event.target as HTMLInputElement;
    searchQuery = target.value;

    // Clear existing timeout
    if (searchTimeout) {
      globalThis.clearTimeout(searchTimeout);
    }

    // Don't search for empty queries
    if (!searchQuery.trim()) {
      isSearching = false;
      return;
    }

    // Set searching state
    isSearching = true;

    // Debounce search (200ms delay like the original)
    searchTimeout = setTimeout(() => {
      performSearch();
    }, 200);
  }

  // Perform the search
  async function performSearch() {
    if (!searchQuery.trim()) {
      isSearching = false;
      return;
    }

    // TODO: Implement search API call when v2 endpoint is available
    // Expected endpoint: /api/v2/detections/search or similar
    // Parameters: { query: searchQuery, limit: 20 }

    // TODO: Search API not implemented for query: ${searchQuery}

    // For now, just call the onSearch callback if provided
    if (onSearch) {
      onSearch(searchQuery);
    }

    // In the future, this would make an API call like:
    /*
    try {
      const response = await fetch(`/api/v2/detections/search?query=${encodeURIComponent(searchQuery)}`);
      if (response.ok) {
        const results = await response.json();
        // Handle results
      }
    } catch (error) {
      console.error('Search failed:', error);
    }
    */

    isSearching = false;

    // TODO: Navigate to search results page when API is ready
    // For now, just log or call navigation callback
    if (onNavigate) {
      onNavigate(`/search?query=${encodeURIComponent(searchQuery)}`);
    }
  }

  // Handle form submission
  function handleSubmit(event: Event) {
    event.preventDefault();
    if (searchTimeout) {
      globalThis.clearTimeout(searchTimeout);
    }
    performSearch();
  }

  // Handle keyboard shortcuts
  function handleKeyDown(event: KeyboardEvent) {
    // Clear search on Escape
    if (event.key === 'Escape') {
      searchQuery = '';
      inputRef?.blur();
    }
  }

  // Focus search input with keyboard shortcut (Cmd/Ctrl + K)
  function handleGlobalKeyDown(event: KeyboardEvent) {
    if ((event.metaKey || event.ctrlKey) && event.key === 'k') {
      event.preventDefault();
      inputRef?.focus();
    }
  }

  $effect(() => {
    // Add global keyboard listener
    if (typeof globalThis.window !== 'undefined') {
      globalThis.window.addEventListener('keydown', handleGlobalKeyDown);
      return () => {
        globalThis.window.removeEventListener('keydown', handleGlobalKeyDown);
      };
    }
  });
</script>

{#if isVisible}
  <div class={cn('flex-grow flex justify-center relative', className)} role="search">
    <form
      onsubmit={handleSubmit}
      class="relative w-full md:w-3/4 lg:w-4/5 xl:w-5/6 max-w-4xl mx-auto"
    >
      <input
        bind:this={inputRef}
        type="text"
        name="search"
        value={searchQuery}
        oninput={handleInput}
        onkeydown={handleKeyDown}
        aria-label={placeholder}
        {placeholder}
        class={cn(
          'input rounded-full focus:outline-none w-full font-normal transition-all',
          sizeClasses().input,
          sizeClasses().padding,
          isSearching && 'opacity-75'
        )}
      />

      <!-- Search icon or loading spinner -->
      <div
        class="absolute inset-y-0 right-0 flex items-center pr-2 sm:pr-3 pointer-events-none"
        aria-hidden="true"
      >
        {#if isSearching}
          <span class="loading loading-spinner loading-sm"></span>
        {:else}
          <svg
            xmlns="http://www.w3.org/2000/svg"
            fill="none"
            viewBox="0 0 24 24"
            stroke-width="1.5"
            stroke="currentColor"
            class={sizeClasses().icon}
          >
            <path
              stroke-linecap="round"
              stroke-linejoin="round"
              d="m21 21-5.197-5.197m0 0A7.5 7.5 0 1 0 5.196 5.196a7.5 7.5 0 0 0 10.607 10.607Z"
            />
          </svg>
        {/if}
      </div>

      <!-- Keyboard shortcut hint -->
      <div class="absolute inset-y-0 left-0 hidden lg:flex items-center pl-3 pointer-events-none">
        <kbd class="kbd kbd-xs text-base-content/40">⌘K</kbd>
      </div>
    </form>
  </div>
{/if}

<style>
  /* Smooth transitions for search states */
  input {
    transition: opacity 0.2s ease-in-out;
  }

  /* Optional: Add focus styles */
  input:focus {
    box-shadow: 0 0 0 2px var(--p);
  }
</style>
