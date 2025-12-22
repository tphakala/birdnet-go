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
  import { t } from '$lib/i18n';
  import { api } from '$lib/utils/api';
  import { loggers } from '$lib/utils/logger';
  import { toastActions } from '$lib/stores/toast';
  import { handleBirdImageError } from '$lib/desktop/components/ui/image-utils.js';
  import SettingsCard from './SettingsCard.svelte';
  import SettingsNote from './SettingsNote.svelte';
  import StatsCard from './StatsCard.svelte';

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
  type SortColumn = 'species' | 'threshold' | 'expires';
  type SortDirection = 'asc' | 'desc';

  // State
  let thresholds = $state<DynamicThreshold[]>([]);
  let stats = $state<ThresholdStats | null>(null);
  let loading = $state(true);
  let searchQuery = $state('');
  let expandedSpecies = $state<Set<string>>(new Set());
  let speciesEvents = $state<Map<string, ThresholdEvent[]>>(new Map());
  let loadingEvents = $state<Set<string>>(new Set());
  let resetConfirmSpecies = $state<string | null>(null);
  let resetAllConfirm = $state(false);
  let resetting = $state(false);

  // Sort state
  let sortColumn = $state<SortColumn>('species');
  let sortDirection = $state<SortDirection>('asc');

  // Handle column header click to toggle sort
  function handleSort(column: SortColumn) {
    if (sortColumn === column) {
      // Toggle direction if clicking same column
      sortDirection = sortDirection === 'asc' ? 'desc' : 'asc';
    } else {
      // New column: set to ascending by default
      sortColumn = column;
      sortDirection = 'asc';
    }
  }

  // Derived state - filter and sort by selected column
  let filteredThresholds = $derived.by(() => {
    // First filter by search query
    const filtered = searchQuery
      ? thresholds.filter(t => t.speciesName.toLowerCase().includes(searchQuery.toLowerCase()))
      : thresholds;

    // Then sort by selected column
    return [...filtered].sort((a, b) => {
      let comparison = 0;

      switch (sortColumn) {
        case 'species':
          comparison = a.speciesName.localeCompare(b.speciesName);
          break;
        case 'threshold':
          comparison = a.currentValue - b.currentValue;
          break;
        case 'expires': {
          // Sort by active status first, then by expiration date
          // Active items come before expired ones in ascending order
          if (a.isActive !== b.isActive) {
            comparison = a.isActive ? -1 : 1;
          } else if (a.isActive && b.isActive) {
            // Both active: compare expiration dates
            comparison = new Date(a.expiresAt).getTime() - new Date(b.expiresAt).getTime();
          } else {
            // Both expired: compare by expiration date (most recently expired first)
            comparison = new Date(b.expiresAt).getTime() - new Date(a.expiresAt).getTime();
          }
          break;
        }
      }

      return sortDirection === 'asc' ? comparison : -comparison;
    });
  });

  let activeThresholds = $derived(thresholds.filter(t => t.isActive));

  // Load data on mount
  onMount(() => {
    loadData();
  });

  async function loadData() {
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
    if (speciesEvents.has(species)) return;

    loadingEvents = new Set([...loadingEvents, species]);
    try {
      const response = await api.get<{ data: ThresholdEvent[] }>(
        `/api/v2/dynamic-thresholds/${encodeURIComponent(species)}/events?limit=10`
      );
      speciesEvents = new Map([...speciesEvents, [species, response.data || []]]);
    } catch (error) {
      logger.error('Failed to load events:', error);
    } finally {
      loadingEvents = new Set([...loadingEvents].filter(s => s !== species));
    }
  }

  function toggleExpanded(species: string) {
    if (expandedSpecies.has(species)) {
      expandedSpecies = new Set([...expandedSpecies].filter(s => s !== species));
    } else {
      expandedSpecies = new Set([...expandedSpecies, species]);
      loadEvents(species);
    }
  }

  async function resetThreshold(species: string) {
    resetting = true;
    try {
      await api.delete(`/api/v2/dynamic-thresholds/${encodeURIComponent(species)}`);
      toastActions.success(t('settings.species.dynamicThreshold.resetSuccess', { species }));
      resetConfirmSpecies = null;
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

  // Map change reason to i18n key
  function getChangeReasonKey(reason: ThresholdChangeReason): string {
    switch (reason) {
      case 'high_confidence':
        return 'settings.species.dynamicThreshold.changeReason.highConfidence';
      case 'expiry':
        return 'settings.species.dynamicThreshold.changeReason.expiry';
      case 'manual_reset':
        return 'settings.species.dynamicThreshold.changeReason.manualReset';
      default:
        // Fallback for any future change reasons
        return 'settings.species.dynamicThreshold.changeReason.highConfidence';
    }
  }
</script>

<div class="space-y-4">
  <!-- Description -->
  <div class="text-sm text-base-content/70">
    <p>{t('settings.species.dynamicThreshold.description')}</p>
  </div>

  <!-- Stats Cards -->
  {#if stats}
    <div class="grid grid-cols-2 lg:grid-cols-4 gap-3">
      <StatsCard
        icon={Activity}
        label={t('settings.species.dynamicThreshold.stats.active')}
        value={stats.activeCount}
      />

      <StatsCard
        icon={TrendingDown}
        label={t('settings.species.dynamicThreshold.stats.atMinimum')}
        value={stats.atMinimumCount}
      />

      <StatsCard
        icon={Gauge}
        label={t('settings.species.dynamicThreshold.stats.minThreshold')}
        value="{(stats.minThreshold * 100).toFixed(0)}%"
      />

      <StatsCard
        icon={Clock}
        label={t('settings.species.dynamicThreshold.stats.validityPeriod')}
        value="{stats.validHours}h"
      />
    </div>
  {/if}

  <!-- Action Bar -->
  <div class="flex flex-wrap items-center gap-3">
    <div class="relative flex-1 min-w-48">
      <Search class="absolute left-3 top-1/2 -translate-y-1/2 size-4 opacity-50" />
      <input
        type="text"
        placeholder={t('settings.species.dynamicThreshold.searchPlaceholder')}
        class="input input-bordered w-full pl-10"
        bind:value={searchQuery}
      />
    </div>

    <button class="btn btn-ghost btn-sm gap-2" onclick={() => loadData()} disabled={loading}>
      <RefreshCw class="size-4 {loading ? 'animate-spin' : ''}" />
      {t('common.refresh')}
    </button>

    {#if activeThresholds.length > 0}
      <button
        class="btn btn-error btn-sm gap-2"
        onclick={() => (resetAllConfirm = true)}
        disabled={resetting}
      >
        <Trash2 class="size-4" />
        {t('settings.species.dynamicThreshold.resetAll')}
      </button>
    {/if}
  </div>

  <!-- Threshold List -->
  <SettingsCard>
    {#if loading}
      <div class="flex justify-center py-8">
        <span class="loading loading-spinner loading-lg"></span>
      </div>
    {:else if filteredThresholds.length === 0}
      <div class="text-center py-8 text-base-content/60">
        <Activity class="size-12 mx-auto mb-3 opacity-40" />
        <p class="font-medium">{t('settings.species.dynamicThreshold.empty.title')}</p>
        <p class="text-sm">{t('settings.species.dynamicThreshold.empty.description')}</p>
      </div>
    {:else}
      <!-- Table Header -->
      <div
        class="grid grid-cols-[auto_auto_1fr_auto_auto_auto] gap-3 items-center px-2 py-2 text-xs font-medium text-base-content/60 border-b border-base-300"
      >
        <div class="w-6"></div>
        <div class="w-10"></div>
        <button
          type="button"
          class="flex items-center gap-1 hover:text-primary transition-colors cursor-pointer text-left"
          onclick={() => handleSort('species')}
          aria-label={t('dataDisplay.table.sortBy', {
            column: t('settings.species.dynamicThreshold.header.species'),
          })}
        >
          {t('settings.species.dynamicThreshold.header.species')}
          {#if sortColumn === 'species'}
            {#if sortDirection === 'asc'}
              <ChevronUp class="size-3.5" />
            {:else}
              <ChevronDown class="size-3.5" />
            {/if}
          {:else}
            <ChevronsUpDown class="size-3.5 opacity-30" />
          {/if}
        </button>
        <button
          type="button"
          class="flex items-center justify-center gap-1 hover:text-primary transition-colors cursor-pointer w-20"
          onclick={() => handleSort('threshold')}
          aria-label={t('dataDisplay.table.sortBy', {
            column: t('settings.species.dynamicThreshold.header.threshold'),
          })}
        >
          {t('settings.species.dynamicThreshold.header.threshold')}
          {#if sortColumn === 'threshold'}
            {#if sortDirection === 'asc'}
              <ChevronUp class="size-3.5" />
            {:else}
              <ChevronDown class="size-3.5" />
            {/if}
          {:else}
            <ChevronsUpDown class="size-3.5 opacity-30" />
          {/if}
        </button>
        <button
          type="button"
          class="flex items-center justify-center gap-1 hover:text-primary transition-colors cursor-pointer w-20"
          onclick={() => handleSort('expires')}
          aria-label={t('dataDisplay.table.sortBy', {
            column: t('settings.species.dynamicThreshold.header.expires'),
          })}
        >
          {t('settings.species.dynamicThreshold.header.expires')}
          {#if sortColumn === 'expires'}
            {#if sortDirection === 'asc'}
              <ChevronUp class="size-3.5" />
            {:else}
              <ChevronDown class="size-3.5" />
            {/if}
          {:else}
            <ChevronsUpDown class="size-3.5 opacity-30" />
          {/if}
        </button>
        <div class="w-8"></div>
      </div>

      <!-- Table Body -->
      <div class="divide-y divide-base-300">
        {#each filteredThresholds as threshold (threshold.speciesName)}
          {@const levelDisplay = getLevelDisplay(threshold.level as ThresholdLevel)}
          {@const isExpanded = expandedSpecies.has(threshold.speciesName)}
          {@const events = speciesEvents.get(threshold.speciesName) || []}
          {@const isLoadingEvents = loadingEvents.has(threshold.speciesName)}

          <div class="py-2">
            <!-- Main Row -->
            <div class="grid grid-cols-[auto_auto_1fr_auto_auto_auto] gap-3 items-center px-2">
              <button
                class="btn btn-ghost btn-xs btn-square"
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

              <!-- Thumbnail -->
              <div class="w-10 h-8 rounded overflow-hidden bg-base-200 shrink-0">
                {#if threshold.scientificName}
                  <img
                    src="/api/v2/media/species-image?name={encodeURIComponent(
                      threshold.scientificName
                    )}"
                    alt={threshold.speciesName}
                    class="w-full h-full object-cover"
                    onerror={handleBirdImageError}
                    loading="lazy"
                  />
                {/if}
              </div>

              <div class="font-medium truncate">{threshold.speciesName}</div>

              <div class="text-center w-20">
                <span class="badge {levelDisplay.badgeClass}">
                  {(threshold.currentValue * 100).toFixed(0)}%
                </span>
              </div>

              <div class="text-center w-20 text-sm">
                {#if threshold.isActive}
                  {getTimeRemaining(threshold.expiresAt)}
                {:else}
                  <span class="text-warning">{t('settings.species.dynamicThreshold.expired')}</span>
                {/if}
              </div>

              <!-- Reset Button -->
              {#if resetConfirmSpecies === threshold.speciesName}
                <div class="flex items-center gap-1">
                  <button
                    class="btn btn-error btn-xs"
                    onclick={() => resetThreshold(threshold.speciesName)}
                    disabled={resetting}
                  >
                    {t('common.confirm')}
                  </button>
                  <button class="btn btn-ghost btn-xs" onclick={() => (resetConfirmSpecies = null)}>
                    {t('common.cancel')}
                  </button>
                </div>
              {:else}
                <button
                  class="btn btn-ghost btn-xs btn-square"
                  onclick={() => (resetConfirmSpecies = threshold.speciesName)}
                  title={t('settings.species.dynamicThreshold.resetSpecies')}
                  aria-label={t('settings.species.dynamicThreshold.resetSpecies')}
                >
                  <Trash2 class="size-4" />
                </button>
              {/if}
            </div>

            <!-- Expanded Events -->
            {#if isExpanded}
              <div class="mt-3 ml-8 pl-4 border-l-2 border-base-300">
                {#if isLoadingEvents}
                  <div class="flex items-center gap-2 py-2 text-base-content/60">
                    <span class="loading loading-spinner loading-xs"></span>
                    <span class="text-sm">{t('common.loading')}</span>
                  </div>
                {:else if events.length === 0}
                  <p class="text-sm text-base-content/60 py-2">
                    {t('settings.species.dynamicThreshold.noEvents')}
                  </p>
                {:else}
                  <div class="space-y-2">
                    {#each events as event (event.id)}
                      <div class="flex items-start gap-2 text-sm">
                        <div
                          class="w-2 h-2 rounded-full mt-1.5 shrink-0"
                          class:bg-blue-500={event.changeReason === 'high_confidence'}
                          class:bg-gray-400={event.changeReason === 'expiry'}
                          class:bg-orange-500={event.changeReason === 'manual_reset'}
                        ></div>
                        <div class="flex-1 min-w-0">
                          <div class="flex items-center gap-2 flex-wrap">
                            <span class="font-medium">
                              {t('settings.species.dynamicThreshold.requiredConfidence')}:
                              {(event.previousValue * 100).toFixed(0)}% → {(
                                event.newValue * 100
                              ).toFixed(0)}%
                            </span>
                            <span class="text-xs text-base-content/60">
                              ({t(getChangeReasonKey(event.changeReason))})
                            </span>
                          </div>
                          <div class="text-xs text-base-content/60">
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
            {/if}
          </div>
        {/each}
      </div>
    {/if}
  </SettingsCard>

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
  <div class="modal modal-open">
    <div class="modal-box">
      <h3 class="font-bold text-lg flex items-center gap-2">
        <AlertTriangle class="size-5 text-warning" />
        {t('settings.species.dynamicThreshold.resetAllConfirm.title')}
      </h3>
      <p class="py-4">
        {t('settings.species.dynamicThreshold.resetAllConfirm.message', {
          count: activeThresholds.length,
        })}
      </p>
      <div class="modal-action">
        <button
          class="btn btn-ghost"
          onclick={() => (resetAllConfirm = false)}
          disabled={resetting}
        >
          {t('common.cancel')}
        </button>
        <button class="btn btn-error" onclick={resetAllThresholds} disabled={resetting}>
          {#if resetting}
            <span class="loading loading-spinner loading-sm"></span>
          {/if}
          {t('settings.species.dynamicThreshold.resetAll')}
        </button>
      </div>
    </div>
    <div
      class="modal-backdrop"
      onclick={() => (resetAllConfirm = false)}
      onkeydown={e => e.key === 'Escape' && (resetAllConfirm = false)}
      role="button"
      tabindex="-1"
      aria-label="Close modal"
    ></div>
  </div>
{/if}
