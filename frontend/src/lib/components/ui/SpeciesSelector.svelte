<!-- Species Selector - Modern UX Component -->
<script lang="ts">
  import type { Snippet } from 'svelte';
  import { createEventDispatcher, onDestroy } from 'svelte';
  import { cn } from '$lib/utils/cn.js';
  import { navigationIcons, actionIcons } from '$lib/utils/icons';
  import type { Species } from '$lib/types/species';

  interface Props {
    species: Species[];
    selected?: string[];
    placeholder?: string;
    size?: 'xs' | 'sm' | 'md' | 'lg' | 'xl';
    variant?: 'chip' | 'list' | 'compact';
    maxSelections?: number;
    searchable?: boolean;
    categorized?: boolean;
    showFrequency?: boolean;
    className?: string;
    disabled?: boolean;
    loading?: boolean;
    emptyText?: string;
    // Snippet for custom species display
    speciesDisplay?: Snippet<[Species]>;
  }

  let {
    species = [],
    selected = [],
    placeholder = 'Select species...',
    size = 'md',
    variant = 'chip',
    maxSelections,
    searchable = true,
    categorized = false,
    showFrequency = true,
    className = '',
    disabled = false,
    loading = false,
    emptyText = 'No species found',
    speciesDisplay,
  }: Props = $props();

  const dispatch = createEventDispatcher<{
    change: { selected: string[] };
    add: { species: Species };
    remove: { species: Species };
  }>();

  // Component state
  let searchQuery = $state('');
  let isExpanded = $state(false);
  let dropdownRef = $state<HTMLDivElement>();
  let inputRef = $state<HTMLInputElement>();
  let shouldShowDropdown = $state(false);
  let blurTimeoutId: number | undefined;
  // Generate unique ID for dropdown (client-only to avoid SSR hydration mismatch)
  let dropdownId = $state('');

  // Size configurations
  const sizeConfig = {
    xs: {
      container: 'h-6 text-xs',
      chip: 'h-5 px-2 text-xs',
      button: 'h-6 w-6 text-xs',
      input: 'input-xs',
      list: 'text-xs py-1',
    },
    sm: {
      container: 'h-8 text-sm',
      chip: 'h-6 px-3 text-sm',
      button: 'h-8 w-8 text-sm',
      input: 'input-sm',
      list: 'text-sm py-2',
    },
    md: {
      container: 'h-10 text-base',
      chip: 'h-7 px-3 text-sm',
      button: 'h-10 w-10',
      input: 'input-md',
      list: 'text-sm py-2',
    },
    lg: {
      container: 'h-12 text-base',
      chip: 'h-8 px-4',
      button: 'h-12 w-12',
      input: 'input-lg',
      list: 'py-3',
    },
    xl: {
      container: 'h-14 text-lg',
      chip: 'h-9 px-4 text-base',
      button: 'h-14 w-14 text-lg',
      input: 'input-lg',
      list: 'py-3 text-base',
    },
  };

  // Frequency styling
  const frequencyConfig = {
    'very-common': { color: 'badge-success', label: 'Very Common' },
    common: { color: 'badge-warning', label: 'Common' },
    uncommon: { color: 'badge-error', label: 'Uncommon' },
    rare: { color: 'badge-error badge-outline', label: 'Rare' },
  };

  // Filtered and categorized species
  const filteredSpecies = $derived(() => {
    // Cache trimmed and lowercased query once to avoid repeated string transforms
    const trimmedLowerQuery = searchQuery.trim().toLowerCase();
    let filtered = species.filter(
      s =>
        s.commonName.toLowerCase().includes(trimmedLowerQuery) ||
        (s.scientificName?.toLowerCase().includes(trimmedLowerQuery) ?? false)
    );

    if (categorized) {
      // Group by category or frequency
      const groups = new Map<string, Species[]>();
      filtered.forEach(s => {
        const key = s.category ?? s.frequency ?? 'other';
        let group = groups.get(key);
        if (group === undefined) {
          group = [];
          groups.set(key, group);
        }
        group.push(s);
      });
      return Array.from(groups.entries()).map(([category, items]) => ({
        category,
        items,
      }));
    }

    return [{ category: '', items: filtered }];
  });

  // Type predicate to safely filter out undefined values
  const isSpecies = (s: Species | undefined): s is Species => s != null;

  // Selected species objects
  const selectedSpecies = $derived(() =>
    selected.map(id => species.find(s => s.id === id)).filter(isSpecies)
  );

  // Handlers
  function toggleSpecies(species: Species) {
    if (disabled) return;

    const isSelected = selected.includes(species.id);

    if (isSelected) {
      const newSelected = selected.filter(id => id !== species.id);
      dispatch('change', { selected: newSelected });
      dispatch('remove', { species });
    } else {
      if (maxSelections && selected.length >= maxSelections) return;

      const newSelected = [...selected, species.id];
      dispatch('change', { selected: newSelected });
      dispatch('add', { species });
    }
  }

  function removeSpecies(species: Species) {
    if (disabled) return;
    const newSelected = selected.filter(id => id !== species.id);
    dispatch('change', { selected: newSelected });
    dispatch('remove', { species });
  }

  function clearSearch() {
    searchQuery = '';
  }

  function clearAll() {
    if (disabled) return;
    dispatch('change', { selected: [] });
  }

  // Close dropdown when clicking outside
  function handleClickOutside(event: MouseEvent) {
    const target = event.target as Node;
    if (dropdownRef && !dropdownRef.contains(target) && inputRef && !inputRef.contains(target)) {
      isExpanded = false;
      shouldShowDropdown = false;
    }
  }

  // Handle input focus
  function handleInputFocus() {
    if (!disabled) {
      isExpanded = true;
      shouldShowDropdown = true;
    }
  }

  // Handle input blur with delay to allow click events
  function handleInputBlur() {
    // Clear any existing timeout
    if (blurTimeoutId !== undefined) {
      window.clearTimeout(blurTimeoutId);
    }
    // Small delay to allow dropdown clicks to register
    blurTimeoutId = window.setTimeout(() => {
      // Only close if not refocusing on input or clicking in dropdown
      if (document.activeElement !== inputRef && !dropdownRef?.contains(document.activeElement)) {
        shouldShowDropdown = false;
      }
      blurTimeoutId = undefined;
    }, 200);
  }

  // Handle keyboard navigation for combobox pattern
  function handleInputKeydown(e: KeyboardEvent) {
    if (disabled) return;
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      // Open and focus first option
      isExpanded = true;
      shouldShowDropdown = true;
      const first = dropdownRef?.querySelector<HTMLElement>('[role="option"]');
      first?.focus();
    } else if (e.key === 'Escape') {
      isExpanded = false;
      shouldShowDropdown = false;
    }
  }

  $effect(() => {
    if (isExpanded || shouldShowDropdown) {
      document.addEventListener('click', handleClickOutside);
      return () => document.removeEventListener('click', handleClickOutside);
    }
  });

  // Update dropdown visibility based on search query
  // Initialize dropdownId on client mount to avoid SSR hydration mismatch
  $effect(() => {
    if (typeof window !== 'undefined' && dropdownId === '') {
      if (typeof crypto !== 'undefined' && crypto.randomUUID) {
        dropdownId = `species-dropdown-${crypto.randomUUID().slice(0, 8)}`;
      } else {
        dropdownId = `species-dropdown-${Math.random().toString(36).slice(2, 9)}`;
      }
    }
  });

  $effect(() => {
    if (searchQuery && inputRef === document.activeElement) {
      shouldShowDropdown = true;
      isExpanded = true;
    }
  });

  // Helper function for frequency badge rendering
  function getFrequencyBadge(frequency: Species['frequency']) {
    if (!showFrequency || !frequency) {
      return null;
    }
    const freqConfig =
      frequency in frequencyConfig
        ? frequencyConfig[frequency as keyof typeof frequencyConfig]
        : null;
    return freqConfig;
  }

  // Cleanup timeout on component destroy
  onDestroy(() => {
    if (blurTimeoutId !== undefined) {
      window.clearTimeout(blurTimeoutId);
    }
  });
</script>

<!-- eslint-disable-next-line security/detect-object-injection -- Safe: size prop is constrained to specific string literals -->
<div
  class={cn(
    'species-selector relative',
    /* eslint-disable-next-line security/detect-object-injection -- Safe: size prop is constrained to specific string literals */
    sizeConfig[size].container,
    className
  )}
>
  <!-- Chip Variant -->
  {#if variant === 'chip'}
    <div
      class="flex flex-wrap gap-2 p-3 bg-base-100 border border-base-300 rounded-lg min-h-[3rem]"
    >
      <!-- Search Input -->
      {#if searchable}
        <div class="flex-1 min-w-[280px]">
          <div class="relative">
            <input
              type="text"
              bind:this={inputRef}
              bind:value={searchQuery}
              placeholder={selectedSpecies().length ? 'Add more species...' : placeholder}
              class={cn(
                'input input-ghost w-full pr-8',
                /* eslint-disable-next-line security/detect-object-injection -- Safe: size prop is constrained to specific string literals */
                sizeConfig[size].input
              )}
              class:input-disabled={disabled}
              readonly={disabled}
              role="combobox"
              aria-label="Search species"
              aria-autocomplete="list"
              aria-expanded={isExpanded || shouldShowDropdown ? 'true' : 'false'}
              aria-controls={dropdownId}
              aria-haspopup="listbox"
              onfocus={handleInputFocus}
              onblur={handleInputBlur}
              onkeydown={handleInputKeydown}
            />
            {#if searchQuery}
              <button
                type="button"
                class="absolute right-2 top-1/2 -translate-y-1/2 btn btn-ghost btn-xs btn-circle"
                onclick={clearSearch}
                aria-label="Clear search"
              >
                {@html navigationIcons.close}
              </button>
            {/if}
          </div>
        </div>
      {/if}

      <!-- Selected Species Chips -->
      {#if selectedSpecies().length > 0}
        <div class="flex flex-wrap gap-2">
          {#each selectedSpecies() as species (species.id)}
            <div
              class={cn(
                'badge badge-primary gap-2 cursor-pointer hover:badge-primary-focus transition-colors',
                // eslint-disable-next-line security/detect-object-injection -- Safe: size prop is constrained to specific string literals
                sizeConfig[size].chip
              )}
            >
              <span class="truncate max-w-[180px]">{species.commonName}</span>
              {#if !disabled}
                <button
                  type="button"
                  class="hover:text-base-100/70"
                  onclick={() => removeSpecies(species)}
                  aria-label="Remove {species.commonName}"
                >
                  {@html navigationIcons.close}
                </button>
              {/if}
            </div>
          {/each}

          <!-- Clear All Button -->
          {#if selectedSpecies().length > 1 && !disabled}
            <button
              type="button"
              class="badge badge-ghost hover:badge-error gap-1 transition-colors"
              onclick={clearAll}
              title="Clear all selections"
            >
              {@html actionIcons.trash}
              Clear
            </button>
          {/if}
        </div>
      {/if}

      <!-- Max Selections Warning -->
      {#if maxSelections && selected.length >= maxSelections}
        <div class="badge badge-warning gap-2" role="status" aria-live="polite" aria-atomic="true">
          {@html actionIcons.warning}
          Max {maxSelections} species selected
        </div>
      {/if}

      <!-- Add Species Button -->
      {#if !searchable && !disabled}
        <button
          type="button"
          class={cn(
            'btn btn-outline btn-primary',
            /* eslint-disable-next-line security/detect-object-injection -- Safe: size prop is constrained to specific string literals */
            sizeConfig[size].button
          )}
          onclick={() => (isExpanded = !isExpanded)}
          aria-label="Add species"
        >
          {@html actionIcons.plus}
        </button>
      {/if}
    </div>

    <!-- Species Dropdown -->
    {#if isExpanded || shouldShowDropdown}
      <div
        bind:this={dropdownRef}
        id={dropdownId}
        class="absolute z-50 w-full min-w-[320px] mt-2 bg-base-100 border border-base-300 rounded-lg shadow-xl max-h-80 overflow-y-auto"
        role="listbox"
        aria-multiselectable="true"
      >
        {#if loading}
          <div class="p-6 text-center">
            <span class="loading loading-spinner loading-md"></span>
            <p class="mt-2 text-sm text-base-content/60">Loading species...</p>
          </div>
        {:else if filteredSpecies()[0]?.items.length === 0}
          <div class="p-6 text-center text-base-content/60">
            {@html actionIcons.search}
            <p class="mt-2">{emptyText}</p>
          </div>
        {:else}
          {#each filteredSpecies() as group}
            {#if group.category && categorized}
              <div class="px-4 py-2 bg-base-200 text-sm font-medium capitalize">
                {group.category.replaceAll('-', ' ')}
              </div>
            {/if}

            {#each group.items as species (species.id)}
              {@const isSelected = selected.includes(species.id)}
              {@const canSelect = !maxSelections || selected.length < maxSelections || isSelected}

              <button
                type="button"
                class={cn(
                  'w-full px-4 py-3 flex items-center justify-between hover:bg-base-200 transition-colors border-b border-base-200 last:border-b-0',
                  // eslint-disable-next-line security/detect-object-injection -- Safe: size prop is constrained to specific string literals
                  sizeConfig[size].list,
                  isSelected && 'bg-primary/10',
                  !canSelect && 'opacity-50 cursor-not-allowed'
                )}
                disabled={!canSelect}
                role="option"
                aria-selected={isSelected ? 'true' : 'false'}
                aria-checked={isSelected ? 'true' : 'false'}
                aria-disabled={!canSelect ? 'true' : 'false'}
                onclick={() => canSelect && toggleSpecies(species)}
              >
                <div class="flex items-center gap-4 flex-1 min-w-0">
                  <!-- Visual checkbox indicator -->
                  <span
                    class={cn(
                      'checkbox checkbox-primary checkbox-sm flex-shrink-0',
                      isSelected && 'checkbox-checked',
                      !canSelect && 'checkbox-disabled'
                    )}
                    aria-hidden="true"
                  ></span>

                  <!-- Species Info -->
                  <div class="text-left flex-1 min-w-0">
                    {#if speciesDisplay}
                      {@render speciesDisplay(species)}
                    {:else}
                      <div class="font-medium text-sm leading-tight truncate">
                        {species.commonName}
                      </div>
                      {#if species.scientificName}
                        <div
                          class="text-xs text-base-content/60 italic leading-tight mt-1 truncate"
                        >
                          {species.scientificName}
                        </div>
                      {/if}
                    {/if}
                  </div>
                </div>

                <!-- Frequency Badge -->
                {#if showFrequency && species.frequency}
                  {@const freqConfig = getFrequencyBadge(species.frequency)}
                  {#if freqConfig}
                    <div class={cn('badge badge-sm ml-2 flex-shrink-0', freqConfig.color)}>
                      {freqConfig.label}
                    </div>
                  {/if}
                {/if}
              </button>
            {/each}
          {/each}
        {/if}
      </div>
    {/if}
  {/if}

  <!-- Compact List Variant -->
  {#if variant === 'list'}
    <div class="space-y-2">
      {#if searchable}
        <div class="relative">
          <input
            type="text"
            bind:value={searchQuery}
            {placeholder}
            aria-label="Search species"
            class={cn(
              'input input-bordered w-full',
              /* eslint-disable-next-line security/detect-object-injection -- Safe: size prop is constrained to specific string literals */
              sizeConfig[size].input
            )}
            class:input-disabled={disabled}
            readonly={disabled}
          />
          {#if searchQuery}
            <button
              type="button"
              class="absolute right-2 top-1/2 -translate-y-1/2 btn btn-ghost btn-xs btn-circle"
              onclick={clearSearch}
              aria-label="Clear search"
            >
              {@html navigationIcons.close}
            </button>
          {/if}
        </div>
      {/if}

      <div class="max-h-60 overflow-y-auto border border-base-300 rounded-lg">
        {#if loading}
          <div class="p-4 text-center">
            <span class="loading loading-spinner loading-sm"></span>
          </div>
        {:else if filteredSpecies()[0]?.items.length === 0}
          <div class="p-4 text-center text-base-content/60 text-sm">
            {emptyText}
          </div>
        {:else}
          {#each filteredSpecies() as group}
            {#each group.items as species (species.id)}
              {@const isSelected = selected.includes(species.id)}
              {@const canSelect = !maxSelections || selected.length < maxSelections || isSelected}

              <label
                class={cn(
                  'flex items-center gap-3 p-3 cursor-pointer hover:bg-base-100 transition-colors border-b border-base-300 last:border-b-0',
                  !canSelect && 'opacity-50 cursor-not-allowed'
                )}
              >
                <input
                  type="checkbox"
                  class="checkbox checkbox-primary"
                  checked={isSelected}
                  disabled={!canSelect || disabled}
                  onchange={() => canSelect && !disabled && toggleSpecies(species)}
                />
                <div class="flex-1">
                  <div class="font-medium">{species.commonName}</div>
                  {#if species.scientificName}
                    <div class="text-sm text-base-content/60 italic">
                      {species.scientificName}
                    </div>
                  {/if}
                </div>
                {#if showFrequency && species.frequency}
                  {@const freqConfig = getFrequencyBadge(species.frequency)}
                  {#if freqConfig}
                    <div class={cn('badge badge-sm', freqConfig.color)}>
                      {freqConfig.label}
                    </div>
                  {/if}
                {/if}
              </label>
            {/each}
          {/each}
        {/if}
      </div>
    </div>
  {/if}
</div>

<style>
  .species-selector input:focus {
    outline: none;
  }

  /* Smooth animations for chips */
  .badge {
    transition: all 0.2s ease;
  }

  /* Hover effects */
  .species-selector button:hover {
    transform: translateY(-1px);
  }

  /* Loading animation */
  .loading-spinner {
    animation: spin 1s linear infinite;
  }
</style>
