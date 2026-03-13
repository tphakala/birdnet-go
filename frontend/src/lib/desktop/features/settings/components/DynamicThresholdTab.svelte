<!--
  Dynamic Threshold Tab Component

  Purpose: Display and manage dynamic threshold runtime data with reset controls.
  BG-59: Shows real-time learned thresholds with event history and reset functionality.

  Features:
  - Statistics overview cards
  - Searchable threshold list with expandable event history
  - Reset controls for individual species and all thresholds
  - Visual level indicators with progress bars

  @component
-->
<script lang="ts">
  import { onMount } from 'svelte';
  import {
    Activity,
    ChevronDown,
    ChevronUp,
    ChevronsUpDown,
    Trash2,
    AlertTriangle,
    RefreshCw,
    Search,
    Clock,
    TrendingDown,
    Gauge,
  } from '@lucide/svelte';
  import { SvelteMap, SvelteSet } from 'svelte/reactivity';
  import { t } from '$lib/i18n';
  import { api } from '$lib/utils/api';
  import { loggers } from '$lib/utils/logger';
  import { toastActions } from '$lib/stores/toast';
  import { handleBirdImageError } from '$lib/desktop/components/ui/image-utils.js';
  import SettingsNote from './SettingsNote.svelte';

  const logger = loggers.settings;

  // API pagination limit - matches backend maxThresholdLimit
  const THRESHOLD_PAGE_LIMIT = 250;

  import type {
    DynamicThreshold,
    ThresholdEvent,
    ThresholdStats,
    ThresholdLevel,
  } from '$lib/types/dynamic-threshold';
  import { getLevelDisplay, getTimeRemaining } from '$lib/types/dynamic-threshold';
  import type { ThresholdChangeReason } from '$lib/types/dynamic-threshold';

  // Sort types
  type SortColumn = 'species' | 'scientificName' | 'threshold' | 'expires';
  type SortDirection = 'asc' | 'desc';

  // State
  let thresholds = $state<DynamicThreshold[]>([]);
  let stats = $state<ThresholdStats | null>(null);
  let loading = $state(false);
  let searchQuery = $state('');
  let expandedSpecies = new SvelteSet<string>();
  let speciesEvents = new SvelteMap<string, ThresholdEvent[]>();
  let loadingEvents = new SvelteSet<string>();
  let resetConfirmSpecies = $state<string | null>(null);
  let resetAllConfirm = $state(false);
  let resetting = $state(false);

  // Sort state
  let sortColumn = $state<SortColumn>('species');
  let sortDirection = $state<SortDirection>('asc');

  // Handle column header click to toggle sort
  function handleSort(column: SortColumn) {
    if (sortColumn === column) {
      sortDirection = sortDirection === 'asc' ? 'desc' : 'asc';
    } else {
      sortColumn = column;
      sortDirection = 'asc';
    }
  }

  // Derived state - filter and sort by selected column
  let filteredThresholds = $derived.by(() => {
    const filtered = searchQuery
      ? thresholds.filter(th => {
          const q = searchQuery.toLowerCase();
          return (
            th.speciesName.toLowerCase().includes(q) ||
            (th.scientificName && th.scientificName.toLowerCase().includes(q))
          );
        })
      : thresholds;

    return [...filtered].sort((a, b) => {
      let comparison = 0;

      switch (sortColumn) {
        case 'species':
          comparison = a.speciesName.localeCompare(b.speciesName);
          break;
        case 'scientificName':
          comparison = (a.scientificName ?? '').localeCompare(b.scientificName ?? '');
          break;
        case 'threshold':
          comparison = a.currentValue - b.currentValue;
          break;
        case 'expires': {
          if (a.isActive !== b.isActive) {
            comparison = a.isActive ? -1 : 1;
          } else if (a.isActive && b.isActive) {
            comparison = new Date(a.expiresAt).getTime() - new Date(b.expiresAt).getTime();
          } else {
            comparison = new Date(b.expiresAt).getTime() - new Date(a.expiresAt).getTime();
          }
          break;
        }
      }

      return sortDirection === 'asc' ? comparison : -comparison;
    });
  });

  let activeThresholds = $derived(thresholds.filter(t => t.isActive));

  onMount(() => {
    loadData();
  });

  async function loadData() {
    if (loading) return;
    loading = true;
    try {
      const [thresholdResponse, statsResponse] = await Promise.all([
        api.get<{ data: DynamicThreshold[]; total: number }>(
          `/api/v2/dynamic-thresholds?limit=${THRESHOLD_PAGE_LIMIT}`
        ),
        api.get<ThresholdStats>('/api/v2/dynamic-thresholds/stats'),
      ]);

      thresholds = thresholdResponse.data || [];
      stats = statsResponse;
    } catch (error) {
      logger.error('Failed to load dynamic thresholds:', error);
      toastActions.error(t('settings.species.dynamicThreshold.loadError'));
    } finally {
      loading = false;
    }
  }

  async function loadEvents(species: string) {
    if (speciesEvents.has(species) || loadingEvents.has(species)) return;

    loadingEvents.add(species);
    try {
      const response = await api.get<{ data: ThresholdEvent[] }>(
        `/api/v2/dynamic-thresholds/${encodeURIComponent(species)}/events?limit=10`
      );
      speciesEvents.set(species, response.data || []);
    } catch (error) {
      logger.error('Failed to load events:', error);
    } finally {
      loadingEvents.delete(species);
    }
  }

  function toggleExpanded(species: string) {
    if (expandedSpecies.has(species)) {
      expandedSpecies.delete(species);
    } else {
      expandedSpecies.add(species);
      loadEvents(species);
    }
  }

  async function resetThreshold(species: string) {
    resetting = true;
    try {
      await api.delete(`/api/v2/dynamic-thresholds/${encodeURIComponent(species)}`);
      toastActions.success(t('settings.species.dynamicThreshold.resetSuccess', { species }));
      resetConfirmSpecies = null;
      expandedSpecies.delete(species);
      speciesEvents.delete(species);
      await loadData();
    } catch (error) {
      logger.error('Failed to reset threshold:', error);
      toastActions.error(t('settings.species.dynamicThreshold.resetError'));
    } finally {
      resetting = false;
    }
  }

  async function resetAllThresholds() {
    resetting = true;
    try {
      const response = await api.delete<{ count: number }>(
        '/api/v2/dynamic-thresholds?confirm=true'
      );
      toastActions.success(
        t('settings.species.dynamicThreshold.resetAllSuccess', { count: response.count })
      );
      resetAllConfirm = false;
      expandedSpecies.clear();
      speciesEvents.clear();
      await loadData();
    } catch (error) {
      logger.error('Failed to reset all thresholds:', error);
      toastActions.error(t('settings.species.dynamicThreshold.resetAllError'));
    } finally {
      resetting = false;
    }
  }

  function formatDate(isoDate: string): string {
    return new Date(isoDate).toLocaleString();
  }

  function getChangeReasonKey(reason: ThresholdChangeReason): string {
    switch (reason) {
      case 'high_confidence':
        return 'settings.species.dynamicThreshold.changeReason.highConfidence';
      case 'expiry':
        return 'settings.species.dynamicThreshold.changeReason.expiry';
      case 'manual_reset':
        return 'settings.species.dynamicThreshold.changeReason.manualReset';
      default:
        return 'settings.species.dynamicThreshold.changeReason.highConfidence';
    }
  }
</script>

<div class="space-y-4">
  <!-- Description -->
  <p class="text-sm text-muted">{t('settings.species.dynamicThreshold.description')}</p>

  <!-- Stats Cards -->
  {#if stats}
    <div class="grid grid-cols-2 lg:grid-cols-4 gap-3">
      <div
        class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-3 shadow-sm"
      >
        <div class="flex items-center gap-2 mb-1.5">
          <div class="p-1 rounded-md bg-violet-500/10">
            <Activity class="w-3.5 h-3.5 text-violet-500" />
          </div>
          <span class="text-xs font-medium text-muted"
            >{t('settings.species.dynamicThreshold.stats.active')}</span
          >
        </div>
        <span class="font-mono tabular-nums text-xl font-semibold pl-0.5">{stats.activeCount}</span>
      </div>

      <div
        class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-3 shadow-sm"
      >
        <div class="flex items-center gap-2 mb-1.5">
          <div class="p-1 rounded-md bg-[color-mix(in_srgb,var(--color-warning)_10%,transparent)]">
            <TrendingDown class="w-3.5 h-3.5 text-[var(--color-warning)]" />
          </div>
          <span class="text-xs font-medium text-muted"
            >{t('settings.species.dynamicThreshold.stats.atMinimum')}</span
          >
        </div>
        <span class="font-mono tabular-nums text-xl font-semibold pl-0.5"
          >{stats.atMinimumCount}</span
        >
      </div>

      <div
        class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-3 shadow-sm"
      >
        <div class="flex items-center gap-2 mb-1.5">
          <div class="p-1 rounded-md bg-[color-mix(in_srgb,var(--color-success)_10%,transparent)]">
            <Gauge class="w-3.5 h-3.5 text-[var(--color-success)]" />
          </div>
          <span class="text-xs font-medium text-muted"
            >{t('settings.species.dynamicThreshold.stats.minThreshold')}</span
          >
        </div>
        <span class="font-mono tabular-nums text-xl font-semibold pl-0.5"
          >{(stats.minThreshold * 100).toFixed(0)}%</span
        >
      </div>

      <div
        class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl p-3 shadow-sm"
      >
        <div class="flex items-center gap-2 mb-1.5">
          <div class="p-1 rounded-md bg-[color-mix(in_srgb,var(--color-info)_10%,transparent)]">
            <Clock class="w-3.5 h-3.5 text-[var(--color-info)]" />
          </div>
          <span class="text-xs font-medium text-muted"
            >{t('settings.species.dynamicThreshold.stats.validityPeriod')}</span
          >
        </div>
        <span class="font-mono tabular-nums text-xl font-semibold pl-0.5"
          >{stats.validHours}{t('common.hoursShort')}</span
        >
      </div>
    </div>
  {/if}

  <!-- Threshold List -->
  <div class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl shadow-sm">
    <!-- Header -->
    <div class="flex items-center justify-between px-4 py-3 border-b border-[var(--border-100)]">
      <div class="flex items-center gap-2">
        <div class="p-1.5 rounded-lg bg-violet-500/10">
          <Activity class="w-4 h-4 text-violet-500" />
        </div>
        <h3 class="text-xs font-semibold uppercase tracking-wider text-muted">
          {t('settings.species.dynamicThreshold.header.species')}
        </h3>
        <span
          class="inline-flex items-center px-2 py-0.5 rounded-full text-[10px] font-medium bg-slate-500/10 text-muted"
        >
          {thresholds.length}
        </span>
      </div>

      <div class="flex items-center gap-2">
        {#if thresholds.length > 8}
          <div class="relative">
            <Search
              class="absolute left-2.5 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-muted"
              aria-hidden="true"
            />
            <input
              type="text"
              placeholder={t('settings.species.dynamicThreshold.searchPlaceholder')}
              aria-label={t('settings.species.dynamicThreshold.searchPlaceholder')}
              class="w-48 pl-8 pr-3 py-1.5 text-xs rounded-lg border border-[var(--border-100)] bg-[var(--surface-100)] focus:outline-none focus:ring-2 focus:ring-blue-500/40"
              bind:value={searchQuery}
              autocomplete="off"
              data-1p-ignore
              data-lpignore="true"
              data-form-type="other"
            />
          </div>
        {/if}

        <button
          type="button"
          class="inline-flex items-center gap-1.5 px-2.5 py-1.5 text-xs font-medium rounded-lg transition-colors cursor-pointer hover:bg-black/[0.05] dark:hover:bg-white/[0.05] text-muted disabled:opacity-50 disabled:cursor-not-allowed"
          onclick={() => loadData()}
          disabled={loading}
        >
          <RefreshCw class="size-3.5 {loading ? 'animate-spin' : ''}" />
          {t('common.refresh')}
        </button>

        {#if activeThresholds.length > 0}
          <button
            type="button"
            class="inline-flex items-center gap-1.5 px-2.5 py-1.5 text-xs font-medium rounded-lg bg-[var(--color-error)] text-[var(--color-error-content)] hover:bg-[var(--color-error-hover)] transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
            onclick={() => (resetAllConfirm = true)}
            disabled={resetting}
          >
            <Trash2 class="size-3.5" />
            {t('settings.species.dynamicThreshold.resetAll')}
          </button>
        {/if}
      </div>
    </div>
    {#if loading}
      <div class="flex items-center justify-center py-12">
        <div
          class="inline-block w-8 h-8 border-4 border-[var(--surface-300)] border-t-[var(--color-primary)] rounded-full animate-spin"
        ></div>
      </div>
    {:else if filteredThresholds.length === 0}
      <div class="text-center py-8 text-muted">
        <Activity class="size-12 mx-auto mb-3 opacity-30" />
        <p class="text-sm font-medium">{t('settings.species.dynamicThreshold.empty.title')}</p>
        <p class="text-xs mt-1">{t('settings.species.dynamicThreshold.empty.description')}</p>
      </div>
    {:else}
      <div class="overflow-y-auto max-h-[28rem]">
        <table class="w-full text-sm">
          <thead class="sticky top-0 bg-[var(--surface-100)] z-10">
            <tr class="border-b border-[var(--border-100)]">
              <th class="w-8 py-2 px-2"><span class="sr-only">{t('common.toggle')}</span></th>
              <th class="w-10 py-2 px-1"><span class="sr-only">{t('common.image')}</span></th>
              <th
                class="text-left py-2 px-3 text-xs font-medium cursor-pointer select-none hover:text-primary transition-colors text-muted"
                role="columnheader"
                tabindex="0"
                aria-sort={sortColumn === 'species'
                  ? sortDirection === 'asc'
                    ? 'ascending'
                    : 'descending'
                  : 'none'}
                onclick={() => handleSort('species')}
                onkeydown={(e: KeyboardEvent) => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault();
                    handleSort('species');
                  }
                }}
              >
                <div class="flex items-center gap-1">
                  {t('settings.species.activeSpecies.columns.commonName')}
                  {#if sortColumn === 'species'}
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
                class="text-left py-2 px-3 text-xs font-medium cursor-pointer select-none hover:text-primary transition-colors text-muted"
                role="columnheader"
                tabindex="0"
                aria-sort={sortColumn === 'scientificName'
                  ? sortDirection === 'asc'
                    ? 'ascending'
                    : 'descending'
                  : 'none'}
                onclick={() => handleSort('scientificName')}
                onkeydown={(e: KeyboardEvent) => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault();
                    handleSort('scientificName');
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
              <th
                class="text-center py-2 px-3 text-xs font-medium cursor-pointer select-none hover:text-primary transition-colors text-muted w-24"
                role="columnheader"
                tabindex="0"
                aria-sort={sortColumn === 'threshold'
                  ? sortDirection === 'asc'
                    ? 'ascending'
                    : 'descending'
                  : 'none'}
                onclick={() => handleSort('threshold')}
                onkeydown={(e: KeyboardEvent) => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault();
                    handleSort('threshold');
                  }
                }}
              >
                <div class="flex items-center justify-center gap-1">
                  {t('settings.species.dynamicThreshold.header.threshold')}
                  {#if sortColumn === 'threshold'}
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
                class="text-center py-2 px-3 text-xs font-medium cursor-pointer select-none hover:text-primary transition-colors text-muted w-24"
                role="columnheader"
                tabindex="0"
                aria-sort={sortColumn === 'expires'
                  ? sortDirection === 'asc'
                    ? 'ascending'
                    : 'descending'
                  : 'none'}
                onclick={() => handleSort('expires')}
                onkeydown={(e: KeyboardEvent) => {
                  if (e.key === 'Enter' || e.key === ' ') {
                    e.preventDefault();
                    handleSort('expires');
                  }
                }}
              >
                <div class="flex items-center justify-center gap-1">
                  {t('settings.species.dynamicThreshold.header.expires')}
                  {#if sortColumn === 'expires'}
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
            {#each filteredThresholds as threshold, index (`${threshold.speciesName}_${index}`)}
              {@const levelDisplay = getLevelDisplay(threshold.level as ThresholdLevel)}
              {@const isExpanded = expandedSpecies.has(threshold.speciesName)}
              {@const events = speciesEvents.get(threshold.speciesName) || []}
              {@const isLoadingEvents = loadingEvents.has(threshold.speciesName)}

              <tr
                class="border-b last:border-b-0 border-[var(--border-100)]/50 hover:bg-black/[0.02] dark:hover:bg-white/[0.02] transition-colors"
              >
                <td class="py-2 px-2">
                  <button
                    type="button"
                    class="inline-flex items-center justify-center p-1 rounded-md hover:bg-[var(--surface-200)] transition-colors"
                    onclick={() => toggleExpanded(threshold.speciesName)}
                    aria-label={isExpanded
                      ? t('settings.species.dynamicThreshold.collapse')
                      : t('settings.species.dynamicThreshold.expand')}
                    aria-expanded={isExpanded}
                  >
                    {#if isExpanded}
                      <ChevronUp class="size-4" />
                    {:else}
                      <ChevronDown class="size-4" />
                    {/if}
                  </button>
                </td>
                <td class="py-2 px-1">
                  <div
                    class="w-10 aspect-[4/3] rounded overflow-hidden bg-[var(--surface-200)] shrink-0"
                  >
                    {#if threshold.scientificName}
                      <img
                        src="/api/v2/media/species-image?name={encodeURIComponent(
                          threshold.scientificName
                        )}"
                        alt=""
                        class="w-full h-full object-cover"
                        onerror={handleBirdImageError}
                        loading="lazy"
                      />
                    {/if}
                  </div>
                </td>
                <td class="py-2 px-3">
                  <span class="font-medium text-sm">{threshold.speciesName}</span>
                </td>
                <td class="py-2 px-3">
                  <span class="text-xs text-muted italic">{threshold.scientificName ?? ''}</span>
                </td>
                <td class="py-2 px-3 text-center">
                  <span
                    class="inline-flex items-center px-2 py-0.5 rounded-full text-xs font-medium {levelDisplay.badgeClass}"
                  >
                    {(threshold.currentValue * 100).toFixed(0)}%
                  </span>
                </td>
                <td class="py-2 px-3 text-center">
                  {#if threshold.isActive}
                    <span class="text-xs font-medium">{getTimeRemaining(threshold.expiresAt)}</span>
                  {:else}
                    <span class="text-xs font-medium text-[var(--color-warning)]"
                      >{t('settings.species.dynamicThreshold.expired')}</span
                    >
                  {/if}
                </td>
                <td class="py-2 px-3 w-12 text-right">
                  {#if resetConfirmSpecies === threshold.speciesName}
                    <div class="flex items-center gap-1">
                      <button
                        type="button"
                        class="inline-flex items-center justify-center px-2 py-1 text-[10px] font-medium rounded-md bg-[var(--color-error)] text-[var(--color-error-content)] hover:bg-[var(--color-error-hover)] transition-colors disabled:opacity-50"
                        onclick={() => resetThreshold(threshold.speciesName)}
                        disabled={resetting}
                      >
                        {t('common.confirm')}
                      </button>
                      <button
                        type="button"
                        class="inline-flex items-center justify-center px-2 py-1 text-[10px] font-medium rounded-md hover:bg-[var(--surface-200)] transition-colors"
                        onclick={() => (resetConfirmSpecies = null)}
                      >
                        {t('common.cancel')}
                      </button>
                    </div>
                  {:else}
                    <button
                      type="button"
                      class="inline-flex items-center justify-center p-1.5 rounded-md transition-colors cursor-pointer hover:bg-[color-mix(in_srgb,var(--color-error)_10%,transparent)] text-muted hover:text-[var(--color-error)]"
                      onclick={() => (resetConfirmSpecies = threshold.speciesName)}
                      title={t('settings.species.dynamicThreshold.resetSpecies')}
                      aria-label={t('settings.species.dynamicThreshold.resetSpecies')}
                    >
                      <Trash2 class="w-3.5 h-3.5" />
                    </button>
                  {/if}
                </td>
              </tr>

              <!-- Expanded Events Row -->
              {#if isExpanded}
                <tr>
                  <td colspan="7" class="px-4 pb-3">
                    <div class="ml-8 pl-4 border-l-2 border-[var(--border-100)] mt-1">
                      {#if isLoadingEvents}
                        <div class="flex items-center gap-2 py-2 text-muted">
                          <div
                            class="inline-block w-3 h-3 border-2 border-[var(--surface-300)] border-t-[var(--color-primary)] rounded-full animate-spin"
                          ></div>
                          <span class="text-xs">{t('common.loading')}</span>
                        </div>
                      {:else if events.length === 0}
                        <p class="text-xs text-muted py-2">
                          {t('settings.species.dynamicThreshold.noEvents')}
                        </p>
                      {:else}
                        <div class="space-y-2 py-1">
                          {#each events as event (event.id)}
                            <div class="flex items-start gap-2 text-xs">
                              <div
                                class="w-1.5 h-1.5 rounded-full mt-1.5 shrink-0 {event.changeReason ===
                                'high_confidence'
                                  ? 'bg-[var(--color-info)]'
                                  : event.changeReason === 'expiry'
                                    ? 'bg-gray-400'
                                    : event.changeReason === 'manual_reset'
                                      ? 'bg-[var(--color-warning)]'
                                      : 'bg-slate-400'}"
                              ></div>
                              <div class="flex-1 min-w-0">
                                <div class="flex items-center gap-2 flex-wrap">
                                  <span class="font-medium">
                                    {t('settings.species.dynamicThreshold.requiredConfidence')}:
                                    {(event.previousValue * 100).toFixed(0)}% → {(
                                      event.newValue * 100
                                    ).toFixed(0)}%
                                  </span>
                                  <span class="text-muted">
                                    ({t(getChangeReasonKey(event.changeReason))})
                                  </span>
                                </div>
                                <div class="text-muted mt-0.5">
                                  {formatDate(event.createdAt)}
                                  {#if event.confidence && event.confidence > 0}
                                    <span class="ml-2">
                                      {t('settings.species.dynamicThreshold.confidence')}: {(
                                        event.confidence * 100
                                      ).toFixed(0)}%
                                    </span>
                                  {/if}
                                </div>
                              </div>
                            </div>
                          {/each}
                        </div>
                      {/if}
                    </div>
                  </td>
                </tr>
              {/if}
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </div>

  <!-- Info Note -->
  <SettingsNote>
    {t('settings.species.dynamicThreshold.note')}
  </SettingsNote>
</div>

<!-- Global Escape key handler for modal -->
<svelte:window
  onkeydown={e => e.key === 'Escape' && resetAllConfirm && (resetAllConfirm = false)}
/>

<!-- Reset All Confirmation Modal -->
{#if resetAllConfirm}
  <div
    class="fixed inset-0 z-50 flex items-center justify-center bg-black/50"
    role="dialog"
    aria-modal="true"
    aria-labelledby="reset-modal-title"
  >
    <div
      class="bg-[var(--surface-100)] border border-[var(--border-100)] rounded-xl shadow-lg p-6 max-w-md mx-4"
    >
      <h3 id="reset-modal-title" class="font-bold text-lg flex items-center gap-2">
        <AlertTriangle class="size-5 text-[var(--color-warning)]" />
        {t('settings.species.dynamicThreshold.resetAllConfirm.title')}
      </h3>
      <p class="text-sm text-muted mt-3">
        {t('settings.species.dynamicThreshold.resetAllConfirm.message', {
          count: activeThresholds.length,
        })}
      </p>
      <div class="flex justify-end gap-2 mt-6">
        <button
          type="button"
          class="inline-flex items-center justify-center h-8 px-3 text-sm font-medium rounded-lg hover:bg-[var(--surface-200)] transition-colors disabled:opacity-50"
          onclick={() => (resetAllConfirm = false)}
          disabled={resetting}
        >
          {t('common.cancel')}
        </button>
        <button
          type="button"
          class="inline-flex items-center justify-center h-8 px-3 text-sm font-medium rounded-lg bg-[var(--color-error)] text-[var(--color-error-content)] hover:bg-[var(--color-error-hover)] transition-colors disabled:opacity-50"
          onclick={resetAllThresholds}
          disabled={resetting}
        >
          {#if resetting}
            <div
              class="inline-block w-3 h-3 border-2 border-white border-t-transparent rounded-full animate-spin mr-1.5"
            ></div>
          {/if}
          {t('settings.species.dynamicThreshold.resetAll')}
        </button>
      </div>
    </div>
    <button
      type="button"
      class="fixed inset-0 -z-10"
      onclick={() => (resetAllConfirm = false)}
      aria-label={t('common.closeModal')}
      tabindex="-1"
    ></button>
  </div>
{/if}
