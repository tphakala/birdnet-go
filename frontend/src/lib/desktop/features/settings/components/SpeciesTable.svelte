<!--
  SpeciesTable.svelte

  Purpose: Sortable table for displaying species data with score bars, badges,
  search, and CSV download. Follows the system page design language from
  SystemProcessTable/MetricStrip (surface tokens, icon pills, text-muted).

  Props:
  - species: ActiveSpecies[] - Species data to display
  - loading: boolean - Show loading spinner
  - searchable: boolean - Show search input
  - expandable: boolean - Show expand/collapse toggle
  - onDownloadCsv: () => void - CSV download callback
  - title: string - Table title
  - description: string - Table description

  @component
-->
<script lang="ts">
  import { t } from '$lib/i18n';
  import {
    ChevronUp,
    ChevronDown,
    ChevronsUpDown,
    Search,
    Maximize2,
    Minimize2,
    Download,
    Bird,
  } from '@lucide/svelte';

  interface ActiveSpeciesItem {
    commonName: string;
    scientificName: string;
    score: number;
    isManuallyIncluded: boolean;
    hasCustomConfig: boolean;
  }

  interface Props {
    species: ActiveSpeciesItem[];
    loading?: boolean;
    searchable?: boolean;
    expandable?: boolean;
    onDownloadCsv?: () => void;
    title?: string;
    description?: string;
  }

  let {
    species,
    loading = false,
    searchable = true,
    expandable = true,
    onDownloadCsv,
    title,
    description,
  }: Props = $props();

  type SortColumn = 'commonName' | 'scientificName' | 'score';

  let sortColumn = $state<SortColumn>('score');
  let sortDirection = $state<'asc' | 'desc'>('desc');
  let searchQuery = $state('');
  let isExpanded = $state(false);

  let filteredSpecies = $derived.by(() => {
    const query = searchQuery.trim().toLowerCase();
    if (!query) return species;
    return species.filter(
      s =>
        s.commonName.toLowerCase().includes(query) || s.scientificName.toLowerCase().includes(query)
    );
  });

  let sortedSpecies = $derived.by(() => {
    return [...filteredSpecies].sort((a, b) => {
      let cmp = 0;
      switch (sortColumn) {
        case 'commonName':
          cmp = a.commonName.localeCompare(b.commonName);
          break;
        case 'scientificName':
          cmp = a.scientificName.localeCompare(b.scientificName);
          break;
        case 'score':
          cmp = a.score - b.score;
          break;
      }
      return sortDirection === 'asc' ? cmp : -cmp;
    });
  });

  function toggleSort(col: SortColumn): void {
    if (sortColumn === col) {
      sortDirection = sortDirection === 'asc' ? 'desc' : 'asc';
    } else {
      sortColumn = col;
      sortDirection = col === 'score' ? 'desc' : 'asc';
    }
  }

  const columns: { key: SortColumn; label: string }[] = [
    { key: 'commonName', label: 'settings.species.activeSpecies.columns.commonName' },
    { key: 'scientificName', label: 'settings.species.activeSpecies.columns.scientificName' },
    { key: 'score', label: 'settings.species.activeSpecies.columns.score' },
  ];
</script>

<div class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl shadow-sm">
  <!-- Header -->
  <div class="flex items-center justify-between px-4 py-3 border-b border-[var(--border-100)]">
    <div class="flex items-center gap-3">
      {#if title}
        <div class="flex items-center gap-2">
          <div class="p-1.5 rounded-lg bg-emerald-500/10">
            <Bird class="w-4 h-4 text-emerald-500" />
          </div>
          <div>
            <h3 class="text-xs font-semibold uppercase tracking-wider text-muted">{title}</h3>
            {#if description}
              <p class="text-xs text-muted mt-0.5">{description}</p>
            {/if}
          </div>
        </div>
      {/if}
      <span
        class="inline-flex items-center px-2 py-0.5 rounded-full text-[10px] font-medium bg-slate-500/10 text-muted"
      >
        {filteredSpecies.length}
      </span>
    </div>

    <div class="flex items-center gap-2">
      {#if searchable}
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

      {#if expandable}
        <button
          type="button"
          class="p-1.5 rounded-md transition-colors cursor-pointer hover:bg-black/[0.05] dark:hover:bg-white/[0.05]"
          onclick={() => (isExpanded = !isExpanded)}
          title={isExpanded
            ? t('settings.species.activeSpecies.collapse') || 'Collapse list'
            : t('settings.species.activeSpecies.expand') || 'Expand list'}
          aria-label={isExpanded
            ? t('settings.species.activeSpecies.collapse') || 'Collapse list'
            : t('settings.species.activeSpecies.expand') || 'Expand list'}
          aria-expanded={isExpanded}
        >
          {#if isExpanded}
            <Minimize2 class="w-3.5 h-3.5 text-muted" />
          {:else}
            <Maximize2 class="w-3.5 h-3.5 text-muted" />
          {/if}
        </button>
      {/if}

      {#if onDownloadCsv}
        <button
          type="button"
          class="inline-flex items-center gap-1.5 px-2.5 py-1.5 text-xs font-medium rounded-lg transition-colors cursor-pointer hover:bg-black/[0.05] dark:hover:bg-white/[0.05] text-muted disabled:opacity-50 disabled:cursor-not-allowed"
          onclick={onDownloadCsv}
          disabled={species.length === 0}
          title={t('settings.species.activeSpecies.downloadCsv') || 'Download CSV'}
          aria-label={t('settings.species.activeSpecies.downloadCsv') || 'Download CSV'}
        >
          <Download class="w-3.5 h-3.5" aria-hidden="true" />
          <span>CSV</span>
        </button>
      {/if}
    </div>
  </div>

  <!-- Table -->
  {#if loading}
    <div class="flex items-center justify-center py-12">
      <div
        class="inline-block w-8 h-8 border-4 border-[var(--surface-300)] border-t-blue-500 rounded-full animate-spin"
      ></div>
    </div>
  {:else if sortedSpecies.length > 0}
    <div
      class="overflow-x-auto overflow-y-auto"
      class:max-h-[32rem]={!isExpanded}
      class:max-h-[80vh]={isExpanded}
    >
      <table class="w-full text-sm">
        <thead class="sticky top-0 bg-[var(--surface-100)] z-10">
          <tr class="border-b border-[var(--border-100)]">
            {#each columns as col (col.key)}
              <th
                class="text-left py-2 px-3 text-xs font-medium cursor-pointer select-none hover:text-blue-500 transition-colors text-muted"
                role="columnheader"
                tabindex="0"
                aria-sort={sortColumn === col.key
                  ? sortDirection === 'asc'
                    ? 'ascending'
                    : 'descending'
                  : 'none'}
                onclick={() => toggleSort(col.key)}
                onkeydown={(e: KeyboardEvent) => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault();
                    toggleSort(col.key);
                  }
                }}
              >
                <div class="flex items-center gap-1">
                  {t(col.label)}
                  {#if sortColumn === col.key}
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
            {/each}
            <th class="text-right py-2 px-3 text-xs font-medium text-muted w-48">
              {t('settings.species.activeSpecies.columns.status')}
            </th>
          </tr>
        </thead>
        <tbody>
          {#each sortedSpecies as item, index (`${item.scientificName}_${item.commonName}_${index}`)}
            <tr
              class="border-b last:border-b-0 border-[var(--border-100)]/50 hover:bg-black/[0.02] dark:hover:bg-white/[0.02] transition-colors"
            >
              <td class="py-2 px-3">
                <span class="font-medium text-sm">{item.commonName}</span>
              </td>
              <td class="py-2 px-3">
                <span class="text-xs text-muted italic">{item.scientificName}</span>
              </td>
              <td class="py-2 px-3">
                <div class="flex items-center gap-2">
                  <div class="w-16 h-1.5 rounded-full overflow-hidden bg-[var(--surface-300)]">
                    <div
                      class="h-full rounded-full bg-blue-500 transition-[width] duration-600 ease-out"
                      style:width="{item.score * 100}%"
                    ></div>
                  </div>
                  <span class="font-mono tabular-nums text-xs">{item.score.toFixed(2)}</span>
                </div>
              </td>
              <td class="py-2 px-3">
                <div class="flex items-center gap-1.5 justify-end flex-nowrap">
                  {#if item.isManuallyIncluded}
                    <span
                      class="inline-flex items-center whitespace-nowrap px-2 py-0.5 rounded-full text-[10px] font-medium bg-emerald-500/15 text-emerald-600 dark:text-emerald-400"
                    >
                      + {t('settings.species.activeSpecies.badges.included')}
                    </span>
                  {/if}
                  {#if item.hasCustomConfig}
                    <span
                      class="inline-flex items-center whitespace-nowrap px-2 py-0.5 rounded-full text-[10px] font-medium bg-teal-500/15 text-teal-600 dark:text-teal-400"
                    >
                      &#9733; {t('settings.species.activeSpecies.badges.configured')}
                    </span>
                  {/if}
                </div>
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {:else if searchQuery}
    <div class="text-center py-8 text-muted">
      <p class="text-sm">{t('settings.species.activeSpecies.noResults')}</p>
    </div>
  {:else}
    <div class="text-center py-8 text-muted">
      <Bird class="size-12 mx-auto mb-3 opacity-30" />
      <p class="text-sm font-medium">{t('settings.species.activeSpecies.empty.title')}</p>
      <p class="text-xs mt-1">{t('settings.species.activeSpecies.empty.description')}</p>
    </div>
  {/if}
</div>
