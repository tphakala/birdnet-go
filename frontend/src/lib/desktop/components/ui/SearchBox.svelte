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

    // Show dropdown when typing
    if (searchQuery.length > 0) {
      showDropdown = true;
      // Filter search history based on current input
      suggestions = searchHistory.filter(item =>
        item.toLowerCase().includes(searchQuery.toLowerCase())
      );
    } else if (searchHistory.length > 0) {
      // Show all recent searches when input is cleared
      suggestions = searchHistory.slice(0, 8);
      showDropdown = true;
    } else {
      showDropdown = false;
      suggestions = [];
    }
  }

  // Perform the search
  async function performSearch() {
    const query = searchQuery.trim();
    if (!query) {
      return;
    }

    showDropdown = false;
    isSearching = true;

    // Save to search history
    saveToHistory(query);

    // Call the onSearch callback if provided
    if (onSearch) {
      onSearch(query);
    }

    // If we're already on detections page, just update the URL without full navigation
    if (currentPage === 'detections') {
      const url = new URL(globalThis.window.location.href);
      url.searchParams.set('search', query);
      globalThis.window.history.replaceState({}, '', url.toString());

      // Trigger a custom event to notify the detections page of the search change
      globalThis.window.dispatchEvent(
        new CustomEvent('searchUpdate', {
          detail: { search: query },
        })
      );
    } else {
      // Navigate to detections page with search query
      if (onNavigate) {
        onNavigate(`/ui/detections?search=${encodeURIComponent(query)}`);
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

  function handleBlur(event: FocusEvent) {
    // Delay hiding dropdown to allow for clicks
    setTimeout(() => {
      showDropdown = false;
      selectedIndex = -1;
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
            <svg
              class="w-4 h-4 text-base-content/60"
              fill="none"
              viewBox="0 0 24 24"
              stroke="currentColor"
            >
              <path
                stroke-linecap="round"
                stroke-linejoin="round"
                stroke-width="2"
                d="M6 18L18 6M6 6l12 12"
              />
            </svg>
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
                  <!-- History icon -->
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
                  <span class="flex-grow text-sm">{suggestion}</span>
                </button>

                <!-- Remove from history button -->
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
