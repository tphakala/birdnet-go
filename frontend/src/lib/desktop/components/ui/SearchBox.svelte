<script lang="ts">
  import { onDestroy } from 'svelte';
  import { cn } from '$lib/utils/cn';
  import {
    parseSearchQuery,
    formatFiltersForAPI,
    getFilterSuggestions,
    formatFilterForDisplay,
    type SearchFilter,
    type ParsedSearch,
  } from '$lib/utils/searchParser';
  import { navigationIcons, actionIcons } from '$lib/utils/icons'; // Centralized icons - see icons.ts

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
    showOnPages = ['dashboard', 'detections'],
    currentPage = 'dashboard',
  }: Props = $props();

  let searchQuery = $state(value);
  let isSearching = $state(false);
  let inputRef = $state<HTMLInputElement>();
  let showDropdown = $state(false);
  let selectedIndex = $state(-1);
  let searchHistory = $state<string[]>([]);
  let suggestions = $state<string[]>([]);

  // Memory leak prevention
  let blurTimeout: ReturnType<typeof setTimeout> | undefined;

  // Advanced search parsing
  let parsedSearch = $state<ParsedSearch>({ textQuery: '', filters: [], errors: [] });
  let showFilterChips = $state(false);
  let showSyntaxHelp = $state(false);

  // Load search history from localStorage
  $effect(() => {
    if (typeof globalThis.window !== 'undefined') {
      const saved = globalThis.localStorage.getItem('birdnet-search-history');
      if (saved) {
        try {
          searchHistory = JSON.parse(saved).slice(0, 10); // Keep last 10 searches
        } catch (e) {
          searchHistory = [];
        }
      }
    }
  });

  // Initialize search query from URL if on detections page
  $effect(() => {
    if (typeof globalThis.window !== 'undefined' && currentPage === 'detections') {
      const params = new URLSearchParams(globalThis.window.location.search);
      const searchParam = params.get('search');
      if (searchParam) {
        searchQuery = searchParam;
      }
    }
  });

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

  // Save search to history
  function saveToHistory(query: string) {
    if (!query.trim()) return;

    // Remove if already exists
    const filtered = searchHistory.filter(item => item !== query);
    // Add to beginning
    const newHistory = [query, ...filtered].slice(0, 10);
    searchHistory = newHistory;

    // Save to localStorage
    if (typeof globalThis.window !== 'undefined') {
      globalThis.localStorage.setItem('birdnet-search-history', JSON.stringify(newHistory));
    }
  }

  // Remove item from search history
  function removeFromHistory(query: string) {
    const newHistory = searchHistory.filter(item => item !== query);
    searchHistory = newHistory;

    // Update suggestions to remove the deleted item
    suggestions = suggestions.filter(item => item !== query);

    // Save to localStorage
    if (typeof globalThis.window !== 'undefined') {
      globalThis.localStorage.setItem('birdnet-search-history', JSON.stringify(newHistory));
    }

    // Hide dropdown if no suggestions left
    if (suggestions.length === 0) {
      showDropdown = false;
    }
  }

  // Handle input change
  function handleInput() {
    selectedIndex = -1;

    // Parse the search query for filters
    parsedSearch = parseSearchQuery(searchQuery);
    showFilterChips = parsedSearch.filters.length > 0 || parsedSearch.errors.length > 0;

    // Get suggestions - prefer filter suggestions over history when typing filters
    let newSuggestions: string[] = [];

    if (searchQuery.length > 0) {
      // Check if user is typing a filter
      const filterSuggestions = getFilterSuggestions(searchQuery);
      if (filterSuggestions.length > 0) {
        newSuggestions = filterSuggestions;
        showSyntaxHelp = true;
      } else {
        // Filter search history based on current input
        newSuggestions = searchHistory.filter(item =>
          item.toLowerCase().includes(searchQuery.toLowerCase())
        );
        showSyntaxHelp = false;
      }
      showDropdown = true;
    } else if (searchHistory.length > 0) {
      // Show all recent searches when input is cleared
      newSuggestions = searchHistory.slice(0, 8);
      showDropdown = true;
      showSyntaxHelp = false;
    } else {
      showDropdown = false;
      showSyntaxHelp = false;
    }

    suggestions = newSuggestions;
  }

  // Remove a filter chip
  function removeFilter(filterIndex: number) {
    const updatedFilters = parsedSearch.filters.filter((_, index) => index !== filterIndex);

    // Reconstruct the search query without the removed filter
    const filtersText = updatedFilters.map(f => f.raw).join(' ');
    const newQuery = `${parsedSearch.textQuery} ${filtersText}`.trim();

    searchQuery = newQuery;
    parsedSearch = parseSearchQuery(newQuery);
    showFilterChips = parsedSearch.filters.length > 0 || parsedSearch.errors.length > 0;
  }

  // Perform the search
  async function performSearch() {
    const query = searchQuery.trim();
    if (!query) {
      return;
    }

    showDropdown = false;
    isSearching = true;

    // Parse the query for advanced search
    const parsed = parseSearchQuery(query);

    // Save to search history
    saveToHistory(query);

    // Call the onSearch callback if provided
    if (onSearch) {
      onSearch(query);
    }

    // Build URL with both text search and filters
    const searchParams = new URLSearchParams();

    // Add basic search if there's text content
    if (parsed.textQuery || parsed.filters.length === 0) {
      searchParams.set('search', parsed.textQuery || query);
    }

    // Add parsed filters as URL parameters
    const filterParams = formatFiltersForAPI(parsed.filters);
    Object.entries(filterParams).forEach(([key, value]) => {
      searchParams.set(key, value);
    });

    // If we're already on detections page, just update the URL without full navigation
    if (currentPage === 'detections') {
      const url = new URL(globalThis.window.location.href);

      // Clear existing search parameters
      url.search = '';

      // Add new parameters
      searchParams.forEach((value, key) => {
        url.searchParams.set(key, value);
      });

      globalThis.window.history.replaceState({}, '', url.toString());

      // Trigger a custom event to notify the detections page of the search change
      globalThis.window.dispatchEvent(
        new CustomEvent('searchUpdate', {
          detail: { search: parsed.textQuery || query, filters: parsed.filters },
        })
      );
    } else {
      // Navigate to detections page with search query and filters
      if (onNavigate) {
        onNavigate(`/ui/detections?${searchParams.toString()}`);
      }
    }

    isSearching = false;
  }

  // Handle form submission
  function handleSubmit(event: Event) {
    event.preventDefault();
    performSearch();
  }

  // Handle suggestion click
  function handleSuggestionClick(suggestion: string) {
    searchQuery = suggestion;
    showDropdown = false;
    performSearch();
  }

  // Handle focus events
  function handleFocus() {
    if (searchQuery.length > 0) {
      // Show recent searches when focused, even if input is empty
      suggestions = searchHistory.filter(item =>
        item.toLowerCase().includes(searchQuery.toLowerCase())
      );
      if (suggestions.length > 0) {
        showDropdown = true;
      }
    } else if (searchHistory.length > 0) {
      // Show recent searches when focused on empty input
      suggestions = searchHistory.slice(0, 8); // Show up to 8 recent searches
      showDropdown = true;
    }
  }

  function handleBlur(_event: FocusEvent) {
    // Clear any existing timeout to prevent duplicate executions
    if (blurTimeout) {
      clearTimeout(blurTimeout);
    }
    
    // Delay hiding dropdown to allow for clicks
    blurTimeout = setTimeout(() => {
      showDropdown = false;
      selectedIndex = -1;
      blurTimeout = undefined;
    }, 150);
  }

  // Clear search
  function clearSearch() {
    searchQuery = '';
    showDropdown = false;
    selectedIndex = -1;
    inputRef?.focus();

    // If on detections page, clear search and refresh
    if (currentPage === 'detections') {
      const url = new URL(globalThis.window.location.href);
      url.searchParams.delete('search');
      globalThis.window.history.replaceState({}, '', url.toString());

      globalThis.window.dispatchEvent(
        new CustomEvent('searchUpdate', {
          detail: { search: '' },
        })
      );
    }
  }

  // Handle keyboard navigation and search
  function handleKeyDown(event: KeyboardEvent) {
    if (!showDropdown) {
      // Just handle Escape and Enter when dropdown is closed
      if (event.key === 'Escape') {
        searchQuery = '';
        inputRef?.blur();
      } else if (event.key === 'Enter') {
        event.preventDefault();
        performSearch();
      }
      return;
    }

    switch (event.key) {
      case 'ArrowDown':
        event.preventDefault();
        selectedIndex = selectedIndex < suggestions.length - 1 ? selectedIndex + 1 : -1;
        break;
      case 'ArrowUp':
        event.preventDefault();
        selectedIndex = selectedIndex > -1 ? selectedIndex - 1 : suggestions.length - 1;
        break;
      case 'Enter':
        event.preventDefault();
        if (selectedIndex >= 0 && suggestions[selectedIndex]) {
          searchQuery = suggestions[selectedIndex];
        }
        showDropdown = false;
        performSearch();
        break;
      case 'Escape':
        showDropdown = false;
        selectedIndex = -1;
        if (!searchQuery) {
          inputRef?.blur();
        }
        break;
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

  // Memory leak prevention - cleanup timeout on component destroy
  onDestroy(() => {
    if (blurTimeout) {
      clearTimeout(blurTimeout);
      blurTimeout = undefined;
    }
  });
</script>

{#if isVisible}
  <div class={cn('flex-grow flex justify-center relative', className)} role="search">
    <form
      onsubmit={handleSubmit}
      class="relative w-full md:w-3/4 lg:w-4/5 xl:w-5/6 max-w-4xl mx-auto"
    >
      <div class="relative">
        <input
          bind:this={inputRef}
          bind:value={searchQuery}
          type="text"
          name="search"
          oninput={handleInput}
          onkeydown={handleKeyDown}
          onfocus={handleFocus}
          onblur={handleBlur}
          aria-label={placeholder}
          {placeholder}
          autocomplete="off"
          class={cn(
            'input rounded-full focus:outline-none w-full font-normal transition-all',
            sizeClasses().input,
            sizeClasses().padding,
            isSearching && 'opacity-75',
            showDropdown && 'rounded-b-none'
          )}
        />

        <!-- Clear button (X) -->
        {#if searchQuery.length > 0}
          <button
            type="button"
            onclick={clearSearch}
            class="absolute inset-y-0 right-8 sm:right-10 flex items-center pr-2 hover:bg-base-200 rounded-full"
            aria-label="Clear search"
          >
            <div class="text-base-content/60">
              {@html navigationIcons.close}
            </div>
          </button>
        {/if}

        <!-- Search icon or loading spinner -->
        <div
          class="absolute inset-y-0 right-0 flex items-center pr-2 sm:pr-3 pointer-events-none"
          aria-hidden="true"
        >
          {#if isSearching}
            <span class="loading loading-spinner loading-sm"></span>
          {:else}
            <div class={sizeClasses().icon}>
              {@html actionIcons.search}
            </div>
          {/if}
        </div>

        <!-- Filter chips -->
        {#if showFilterChips}
          <div
            class="absolute top-full left-0 right-0 bg-base-100 border border-base-300 border-t-0 rounded-b-lg shadow-sm z-40 p-3"
          >
            <!-- Active filters -->
            {#if parsedSearch.filters.length > 0}
              <div class="flex flex-wrap gap-2 mb-2">
                {#each parsedSearch.filters as filter, index}
                  <div class="badge badge-primary gap-2">
                    <span class="text-xs">{formatFilterForDisplay(filter)}</span>
                    <button
                      type="button"
                      onclick={() => removeFilter(index)}
                      class="btn btn-ghost btn-xs btn-circle"
                      aria-label="Remove filter"
                    >
                      <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                        <path
                          stroke-linecap="round"
                          stroke-linejoin="round"
                          stroke-width="2"
                          d="M6 18L18 6M6 6l12 12"
                        />
                      </svg>
                    </button>
                  </div>
                {/each}
              </div>
            {/if}

            <!-- Errors -->
            {#if parsedSearch.errors.length > 0}
              <div class="space-y-1 mb-2">
                {#each parsedSearch.errors as error}
                  <div class="alert alert-error alert-sm">
                    <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                      <path
                        stroke-linecap="round"
                        stroke-linejoin="round"
                        stroke-width="2"
                        d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-2.5L13.732 4c-.77-.833-1.964-.833-2.732 0L4.082 15.5c-.77.833.192 2.5 1.732 2.5z"
                      />
                    </svg>
                    <span class="text-xs">{error}</span>
                  </div>
                {/each}
              </div>
            {/if}

            <!-- Syntax help -->
            {#if showSyntaxHelp}
              <div class="text-xs text-base-content/60 border-t border-base-300 pt-2">
                <p class="font-medium mb-1">Filter Syntax:</p>
                <p>confidence:>85, time:dawn, date:today, verified:true</p>
              </div>
            {/if}
          </div>
        {/if}

        <!-- Search suggestions dropdown -->
        {#if showDropdown && suggestions.length > 0}
          <div
            class="absolute top-full left-0 right-0 bg-base-100 border border-base-300 border-t-0 rounded-b-lg shadow-lg z-50 max-h-80 overflow-y-auto"
          >
            {#each suggestions as suggestion, index}
              <div
                class={cn(
                  'w-full flex items-center gap-3 border-b border-base-200 last:border-b-0 group hover:bg-base-200',
                  selectedIndex === index && 'bg-base-200'
                )}
              >
                <!-- Main suggestion button -->
                <button
                  type="button"
                  onclick={() => handleSuggestionClick(suggestion)}
                  class="flex-grow flex items-center gap-3 px-4 py-2 text-left"
                >
                  <!-- Icon - Filter or History -->
                  {#if showSyntaxHelp}
                    <!-- Filter icon for syntax suggestions -->
                    <svg
                      class="w-4 h-4 text-primary/80 flex-shrink-0"
                      fill="none"
                      viewBox="0 0 24 24"
                      stroke="currentColor"
                    >
                      <path
                        stroke-linecap="round"
                        stroke-linejoin="round"
                        stroke-width="2"
                        d="M3 4a1 1 0 011-1h16a1 1 0 011 1v2.586a1 1 0 01-.293.707l-6.414 6.414a1 1 0 00-.293.707V17l-4 4v-6.586a1 1 0 00-.293-.707L3.293 7.293A1 1 0 013 6.586V4z"
                      />
                    </svg>
                  {:else}
                    <!-- History icon for search history -->
                    <svg
                      class="w-4 h-4 text-base-content/60 flex-shrink-0"
                      fill="none"
                      viewBox="0 0 24 24"
                      stroke="currentColor"
                    >
                      <path
                        stroke-linecap="round"
                        stroke-linejoin="round"
                        stroke-width="2"
                        d="M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z"
                      />
                    </svg>
                  {/if}
                  <span class="flex-grow text-sm">{suggestion}</span>
                </button>

                <!-- Remove from history button (only for history, not filter suggestions) -->
                {#if !showSyntaxHelp}
                  <button
                    type="button"
                    onclick={e => {
                      e.stopPropagation();
                      removeFromHistory(suggestion);
                    }}
                    class="opacity-0 group-hover:opacity-100 hover:opacity-100 p-2 mr-2 hover:bg-base-300 rounded"
                    aria-label="Remove from history"
                  >
                    <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                      <path
                        stroke-linecap="round"
                        stroke-linejoin="round"
                        stroke-width="2"
                        d="M6 18L18 6M6 6l12 12"
                      />
                    </svg>
                  </button>
                {/if}
              </div>
            {/each}
          </div>
        {/if}
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
