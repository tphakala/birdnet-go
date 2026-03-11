<!--
  SpeciesListCard.svelte

  Purpose: Card for managing Include/Exclude species lists with system page
  design language (surface tokens, icon pills, text-muted). Uses a clean
  table-style row layout with search filtering for long lists.

  Props:
  - title: string - Card title
  - species: string[] - Current species list
  - icon: Component - Lucide icon for the header pill
  - iconColorClass: string - Tailwind color class for icon pill (e.g. 'emerald', 'red')
  - predictions: string[] - Autocomplete predictions
  - inputValue: string - Current input value (bindable)
  - inputLabel: string - Label for the input
  - inputPlaceholder: string - Placeholder for the input
  - emptyMessage: string - Message when list is empty
  - disabled: boolean - Disable all interactions
  - onAdd: (species: string) => void - Add species callback
  - onRemove: (species: string) => void - Remove species callback
  - onInput: (input: string) => void - Input change callback for predictions

  @component
-->
<script lang="ts">
  import type { Component } from 'svelte';
  import type { IconProps } from '@lucide/svelte';
  import { Trash2, Search, Plus, ChevronUp, ChevronDown, ChevronsUpDown } from '@lucide/svelte';
  import { t } from '$lib/i18n';

  interface Props {
    title: string;
    species: string[];
    icon: Component<IconProps>;
    iconColorClass?: string;
    scientificNameMap?: Map<string, string>;
    predictions: string[];
    inputValue: string;
    inputLabel: string;
    inputPlaceholder: string;
    emptyMessage: string;
    disabled?: boolean;
    onAdd: (_species: string) => void;
    onRemove: (_species: string) => void;
    onInput: (_input: string) => void;
  }

  let {
    title,
    species,
    icon: Icon,
    iconColorClass = 'emerald',
    scientificNameMap = new Map(),
    predictions,
    inputValue = $bindable(),
    inputLabel,
    inputPlaceholder,
    emptyMessage,
    disabled = false,
    onAdd,
    onRemove,
    onInput,
  }: Props = $props();

  const colorMap: Record<string, { bg: string; text: string }> = {
    emerald: { bg: 'bg-emerald-500/10', text: 'text-emerald-500' },
    red: { bg: 'bg-red-500/10', text: 'text-red-500' },
    orange: { bg: 'bg-orange-500/10', text: 'text-orange-500' },
    blue: { bg: 'bg-blue-500/10', text: 'text-blue-500' },
    teal: { bg: 'bg-teal-500/10', text: 'text-teal-500' },
  };

  let colors = $derived(colorMap[iconColorClass] ?? colorMap.emerald);

  type SortColumn = 'commonName' | 'scientificName';

  let searchQuery = $state('');
  let sortColumn = $state<SortColumn | null>(null);
  let sortDirection = $state<'asc' | 'desc'>('asc');
  let showSearch = $derived(species.length > 8);

  let filteredSpecies = $derived.by(() => {
    const query = searchQuery.trim().toLowerCase();
    let result = query
      ? species.filter(s => {
          if (s.toLowerCase().includes(query)) return true;
          const sci = scientificNameMap.get(s.toLowerCase());
          return sci ? sci.toLowerCase().includes(query) : false;
        })
      : [...species];
    if (sortColumn) {
      result.sort((a, b) => {
        let cmp = 0;
        if (sortColumn === 'commonName') {
          cmp = a.localeCompare(b);
        } else {
          const sciA = scientificNameMap.get(a.toLowerCase()) ?? '';
          const sciB = scientificNameMap.get(b.toLowerCase()) ?? '';
          cmp = sciA.localeCompare(sciB);
        }
        return sortDirection === 'asc' ? cmp : -cmp;
      });
    }
    return result;
  });

  function toggleSort(column: SortColumn): void {
    if (sortColumn === column) {
      sortDirection = sortDirection === 'asc' ? 'desc' : 'asc';
    } else {
      sortColumn = column;
      sortDirection = 'asc';
    }
  }

  // Autocomplete state
  let showPredictions = $state(false);
  let selectedPredictionIndex = $state(-1);

  let filteredPredictions = $derived.by(() => {
    if (inputValue.length < 2 || predictions.length === 0) return [];
    return predictions
      .filter(p => p.toLowerCase().includes(inputValue.toLowerCase()) && p !== inputValue)
      .slice(0, 8);
  });

  function handleInputChange(e: Event) {
    const target = e.target as HTMLInputElement;
    inputValue = target.value;
    selectedPredictionIndex = -1;
    showPredictions = true;
    onInput(target.value);
  }

  function handleAdd() {
    if (!inputValue.trim() || disabled) return;
    onAdd(inputValue.trim());
    inputValue = '';
    showPredictions = false;
    selectedPredictionIndex = -1;
  }

  function selectPrediction(prediction: string) {
    inputValue = prediction;
    showPredictions = false;
    selectedPredictionIndex = -1;
    // Auto-add after selecting
    setTimeout(() => handleAdd(), 0);
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === 'Enter') {
      e.preventDefault();
      if (selectedPredictionIndex >= 0 && filteredPredictions[selectedPredictionIndex]) {
        selectPrediction(filteredPredictions[selectedPredictionIndex]);
      } else {
        handleAdd();
      }
    } else if (e.key === 'Escape') {
      showPredictions = false;
      selectedPredictionIndex = -1;
    } else if (e.key === 'ArrowDown' && showPredictions && filteredPredictions.length > 0) {
      e.preventDefault();
      selectedPredictionIndex = Math.min(
        selectedPredictionIndex + 1,
        filteredPredictions.length - 1
      );
    } else if (e.key === 'ArrowUp' && showPredictions) {
      e.preventDefault();
      selectedPredictionIndex = Math.max(selectedPredictionIndex - 1, -1);
    }
  }

  function handleBlur() {
    // Delay to allow click on prediction
    setTimeout(() => {
      showPredictions = false;
      selectedPredictionIndex = -1;
    }, 200);
  }
</script>

<div class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl shadow-sm">
  <!-- Header -->
  <div class="flex items-center justify-between px-4 py-3 border-b border-[var(--border-100)]">
    <div class="flex items-center gap-2">
      <div class="p-1.5 rounded-lg {colors.bg}">
        <Icon class="w-4 h-4 {colors.text}" />
      </div>
      <h3 class="text-xs font-semibold uppercase tracking-wider text-muted">{title}</h3>
      <span
        class="inline-flex items-center px-2 py-0.5 rounded-full text-[10px] font-medium bg-slate-500/10 text-muted"
      >
        {species.length}
      </span>
    </div>

    {#if showSearch}
      <div class="relative">
        <Search
          class="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-muted"
          aria-hidden="true"
        />
        <input
          type="text"
          bind:value={searchQuery}
          placeholder={t('settings.species.activeSpecies.search.placeholder')}
          aria-label={t('settings.species.activeSpecies.search.placeholder')}
          autocomplete="off"
          data-1p-ignore
          data-lpignore="true"
          data-form-type="other"
          class="w-48 pl-8 pr-3 py-1.5 text-xs rounded-lg border border-[var(--border-100)] bg-[var(--surface-100)] focus:outline-none focus:ring-2 focus:ring-blue-500/40"
        />
      </div>
    {/if}
  </div>

  <!-- Species List -->
  {#if species.length > 0}
    <div class="overflow-y-auto max-h-[28rem]">
      <table class="w-full text-sm">
        <thead class="sticky top-0 bg-[var(--surface-100)] z-10">
          <tr class="border-b border-[var(--border-100)]">
            <th
              class="text-left py-2 px-3 text-xs font-medium cursor-pointer select-none hover:text-blue-500 transition-colors text-muted"
              role="columnheader"
              tabindex="0"
              aria-sort={sortColumn === 'commonName'
                ? sortDirection === 'asc'
                  ? 'ascending'
                  : 'descending'
                : 'none'}
              onclick={() => toggleSort('commonName')}
              onkeydown={(e: KeyboardEvent) => {
                if (e.key === 'Enter' || e.key === ' ') {
                  e.preventDefault();
                  toggleSort('commonName');
                }
              }}
            >
              <div class="flex items-center gap-1">
                {t('settings.species.activeSpecies.columns.commonName')}
                {#if sortColumn === 'commonName'}
                  {#if sortDirection === 'asc'}
                    <ChevronUp class="w-3 h-3" />
                  {:else}
                    <ChevronDown class="w-3 h-3" />
                  {/if}
                {:else}
                  <ChevronsUpDown class="w-3 h-3 opacity-30" />
                {/if}
              </div>
            </th>
            <th
              class="text-left py-2 px-3 text-xs font-medium cursor-pointer select-none hover:text-blue-500 transition-colors text-muted"
              role="columnheader"
              tabindex="0"
              aria-sort={sortColumn === 'scientificName'
                ? sortDirection === 'asc'
                  ? 'ascending'
                  : 'descending'
                : 'none'}
              onclick={() => toggleSort('scientificName')}
              onkeydown={(e: KeyboardEvent) => {
                if (e.key === 'Enter' || e.key === ' ') {
                  e.preventDefault();
                  toggleSort('scientificName');
                }
              }}
            >
              <div class="flex items-center gap-1">
                {t('settings.species.activeSpecies.columns.scientificName')}
                {#if sortColumn === 'scientificName'}
                  {#if sortDirection === 'asc'}
                    <ChevronUp class="w-3 h-3" />
                  {:else}
                    <ChevronDown class="w-3 h-3" />
                  {/if}
                {:else}
                  <ChevronsUpDown class="w-3 h-3 opacity-30" />
                {/if}
              </div>
            </th>
            <th class="w-12"><span class="sr-only">{t('common.actionsColumn')}</span></th>
          </tr>
        </thead>
        <tbody>
          {#each filteredSpecies as item, index (`${item}_${index}`)}
            <tr
              class="border-b last:border-b-0 border-[var(--border-100)]/50 hover:bg-black/[0.02] dark:hover:bg-white/[0.02] transition-colors"
            >
              <td class="py-2 px-3">
                <span class="font-medium text-sm">{item}</span>
              </td>
              <td class="py-2 px-3">
                <span class="text-xs text-muted italic"
                  >{scientificNameMap.get(item.toLowerCase()) ?? ''}</span
                >
              </td>
              <td class="py-2 px-3 w-12 text-right">
                <button
                  type="button"
                  class="p-1.5 rounded-md transition-colors cursor-pointer hover:bg-red-500/10 text-muted hover:text-red-500 disabled:opacity-50 disabled:cursor-not-allowed"
                  onclick={() => onRemove(item)}
                  {disabled}
                  aria-label={t('settings.species.remove') || `Remove ${item}`}
                  title={t('settings.species.remove') || 'Remove species'}
                >
                  <Trash2 class="w-3.5 h-3.5" />
                </button>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
      {#if searchQuery && filteredSpecies.length === 0}
        <div class="text-sm text-muted italic py-6 text-center">
          {t('settings.species.activeSpecies.noResults')}
        </div>
      {/if}
    </div>
  {:else}
    <div class="text-sm text-muted italic py-8 text-center">
      {emptyMessage}
    </div>
  {/if}

  <!-- Add Species Input -->
  <div class="px-4 py-3 border-t border-[var(--border-100)]">
    <label
      for="species-list-input-{iconColorClass}"
      class="text-xs font-semibold uppercase tracking-wider text-muted mb-2 block"
    >
      {inputLabel}
    </label>
    <div class="relative">
      <div class="flex gap-2">
        <input
          id="species-list-input-{iconColorClass}"
          type="text"
          value={inputValue}
          oninput={handleInputChange}
          onkeydown={handleKeydown}
          onblur={handleBlur}
          onfocus={() => {
            if (filteredPredictions.length > 0) showPredictions = true;
          }}
          placeholder={inputPlaceholder}
          {disabled}
          autocomplete="off"
          data-1p-ignore
          data-lpignore="true"
          data-form-type="other"
          role="combobox"
          aria-controls="species-predictions-{iconColorClass}"
          aria-expanded={showPredictions && filteredPredictions.length > 0}
          aria-haspopup="listbox"
          aria-label={inputLabel}
          class="flex-1 px-3 py-2 text-sm rounded-lg border border-[var(--border-100)] bg-[var(--surface-100)] focus:outline-none focus:ring-2 focus:ring-blue-500/40 disabled:opacity-50 disabled:cursor-not-allowed"
        />
        <button
          type="button"
          onclick={handleAdd}
          disabled={disabled || !inputValue.trim()}
          class="inline-flex items-center gap-1.5 px-3.5 py-2 text-sm font-medium rounded-lg bg-blue-500 text-white hover:bg-blue-600 transition-colors cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
          aria-label={inputLabel}
        >
          <Plus class="w-4 h-4" aria-hidden="true" />
          {t('settings.species.add') || 'Add'}
        </button>
      </div>

      <!-- Predictions Dropdown -->
      {#if showPredictions && filteredPredictions.length > 0}
        <div
          id="species-predictions-{iconColorClass}"
          class="absolute left-0 right-0 top-full mt-1 bg-[var(--surface-100)] border border-[var(--border-100)] rounded-lg shadow-lg max-h-48 overflow-y-auto z-50"
          role="listbox"
          aria-label={t('settings.species.suggestions') || 'Species suggestions'}
        >
          {#each filteredPredictions as prediction, idx (`${prediction}_${idx}`)}
            <button
              type="button"
              role="option"
              aria-selected={idx === selectedPredictionIndex}
              class="w-full text-left px-3 py-2 text-sm transition-colors cursor-pointer hover:bg-black/[0.04] dark:hover:bg-white/[0.04] {idx ===
              selectedPredictionIndex
                ? 'bg-blue-500/10 text-blue-600 dark:text-blue-400'
                : ''}"
              onmousedown={() => selectPrediction(prediction)}
            >
              {prediction}
            </button>
          {/each}
        </div>
      {/if}
    </div>
  </div>
</div>
