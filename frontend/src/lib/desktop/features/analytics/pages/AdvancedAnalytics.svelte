<!--
  Analytics hub (PR0 foundation).

  Registry-driven tabbed surface for the Advanced Analytics route: a sticky
  shared control bar, a tab bar whose active tab lives in the `?tab=` URL param,
  and a responsive grid of ChartCards for the active tab only. Date range,
  species, source, and active tab all live in URL query params, so views are
  deep-linkable and survive reload and Back/Forward.

  PR0 migrates the three existing charts (time-of-day species, daily species
  trend, species diversity) onto the registry with no behavior change. The
  Overview and Review & Accuracy tabs are intentionally empty here; later PRs
  populate them.
-->
<script lang="ts">
  import { onMount, untrack } from 'svelte';
  import {
    Activity,
    TrendingUp,
    Leaf,
    LayoutDashboard,
    BadgeCheck,
    Construction,
  } from '@lucide/svelte';

  import { t } from '$lib/i18n';
  import { getLogger } from '$lib/utils/logger';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import type { Species, SpeciesFrequency } from '$lib/types/species';
  import { createSpeciesId } from '$lib/types/species';

  import AnalyticsControlBar from '../components/AnalyticsControlBar.svelte';
  import ChartCard from '../components/ChartCard.svelte';
  import { chartsForGroup, groupHasCharts } from '../registry/charts';
  import {
    GROUP_ORDER,
    formatDateForAPI,
    parseAnalyticsParams,
    resolveDateRange,
    serializeAnalyticsParams,
  } from '../registry/analyticsParams';
  import type { AnalyticsParams, ChartGroup } from '../registry/types';

  const logger = getLogger('analytics-hub');

  // Default landing tab: the first group (in display order) that actually has
  // charts. Self-adjusts to Overview once PR0b populates it, so PR0 never lands
  // the user on an empty tab.
  const defaultTab: ChartGroup = GROUP_ORDER.find(groupHasCharts) ?? 'overview';

  // Match the legacy behavior: auto-select the top species when none are in the URL.
  const AUTO_SELECT_TOP = 5;
  const SPECIES_SUMMARY_LIMIT = 50;

  interface SpeciesSummaryResponse {
    scientific_name?: string;
    common_name?: string;
    count?: number;
  }

  // Tab metadata (label key + icon), in display order.
  const TABS: { group: ChartGroup; labelKey: string; icon: typeof Activity }[] = [
    { group: 'overview', labelKey: 'analytics.hub.tabs.overview', icon: LayoutDashboard },
    { group: 'patterns', labelKey: 'analytics.hub.tabs.patterns', icon: Activity },
    { group: 'trends', labelKey: 'analytics.hub.tabs.trends', icon: TrendingUp },
    { group: 'biodiversity', labelKey: 'analytics.hub.tabs.biodiversity', icon: Leaf },
    { group: 'quality', labelKey: 'analytics.hub.tabs.quality', icon: BadgeCheck },
  ];

  function readParams(): AnalyticsParams {
    const search = typeof window !== 'undefined' ? window.location.search : '';
    return parseAnalyticsParams(search, { defaultTab });
  }

  let params = $state<AnalyticsParams>(readParams());

  // Species available for the current range: powers the selector and provides
  // the scientific -> common name map used to label chart series.
  let availableSpecies = $state<Species[]>([]);
  let loadingSpecies = $state(false);
  let speciesController: AbortController | null = null;

  const speciesNames = $derived(
    new Map(availableSpecies.map(s => [s.scientificName ?? s.id, s.commonName]))
  );

  const activeCharts = $derived(chartsForGroup(params.tab));
  const speciesApplicable = $derived(activeCharts.some(c => c.supports.species));
  const activeTabLabelKey = $derived(
    TABS.find(tab => tab.group === params.tab)?.labelKey ?? 'analytics.hub.tabs.overview'
  );

  // --- URL state -----------------------------------------------------------

  function writeUrl(mode: 'push' | 'replace'): void {
    if (typeof window === 'undefined') return;
    const qs = serializeAnalyticsParams(params, { defaultTab });
    const url = window.location.pathname + (qs ? `?${qs}` : '');
    if (mode === 'replace') {
      window.history.replaceState(window.history.state, '', url);
    } else {
      window.history.pushState(window.history.state, '', url);
    }
  }

  function applyParams(partial: Partial<AnalyticsParams>, mode: 'push' | 'replace' = 'push'): void {
    const next: AnalyticsParams = { ...params, ...partial };
    // Keep the parsed dates in sync with range/start/end.
    const [startDate, endDate] = resolveDateRange(next.range, next.start, next.end);
    next.startDate = startDate;
    next.endDate = endDate;
    params = next;
    writeUrl(mode);
  }

  function selectTab(group: ChartGroup): void {
    if (group === params.tab) return;
    applyParams({ tab: group }, 'push');
  }

  // Browser Back/Forward: re-read params from the URL without writing (no loop).
  onMount(() => {
    const handlePopState = () => {
      params = readParams();
    };
    window.addEventListener('popstate', handlePopState);
    return () => window.removeEventListener('popstate', handlePopState);
  });

  // --- Available species ----------------------------------------------------

  // Refetch the species list only when the resolved date range changes.
  const rangeKey = $derived(
    `${formatDateForAPI(params.startDate)}|${formatDateForAPI(params.endDate)}`
  );

  function classifyFrequency(count: number): SpeciesFrequency {
    if (count > 100) return 'very-common';
    if (count > 50) return 'common';
    if (count > 10) return 'uncommon';
    return 'rare';
  }

  async function fetchAvailableSpecies(): Promise<void> {
    const snapshot = untrack(() => params);
    speciesController?.abort();
    const ac = new AbortController();
    speciesController = ac;
    loadingSpecies = true;

    try {
      const search = new URLSearchParams({
        start_date: formatDateForAPI(snapshot.startDate),
        end_date: formatDateForAPI(snapshot.endDate),
        limit: String(SPECIES_SUMMARY_LIMIT),
      });
      const response = await fetch(buildAppUrl(`/api/v2/analytics/species/summary?${search}`), {
        signal: ac.signal,
      });
      if (!response.ok) throw new Error(`HTTP ${response.status}: ${response.statusText}`);

      const data: unknown = await response.json();
      // Invariant: id === scientificName for real rows. The species selector,
      // the URL `species` param, the registry fetchers' `species` query arg, and
      // the chart series keys all identify a species by its scientific name, so
      // id must equal scientificName for the round-trip to work. (The index
      // fallback only applies to degenerate rows missing a scientific name.)
      availableSpecies = Array.isArray(data)
        ? (data as SpeciesSummaryResponse[]).map((item, index) => {
            const count = item.count ?? 0;
            return {
              id: createSpeciesId(item.scientific_name ?? `species-${index}`),
              commonName: item.common_name ?? t('common.unknown'),
              scientificName: item.scientific_name ?? t('common.unknown'),
              frequency: classifyFrequency(count),
              category: t('analytics.advanced.categories.birds'),
              description: t('analytics.advanced.detections', { count }),
              count,
            };
          })
        : [];

      // Auto-select the top species when the URL specifies none. Written via
      // replace so the deep-link is reproducible without adding a history entry.
      if (untrack(() => params).species.length === 0 && availableSpecies.length > 0) {
        const top = availableSpecies
          .slice(0, Math.min(availableSpecies.length, AUTO_SELECT_TOP))
          .map(s => s.scientificName ?? s.id);
        applyParams({ species: top }, 'replace');
      }
    } catch (err) {
      if (err instanceof Error && err.name === 'AbortError') return;
      logger.error('Failed to fetch available species', err);
      availableSpecies = [];
    } finally {
      if (!ac.signal.aborted) loadingSpecies = false;
    }
  }

  // Refetch only when the resolved date range changes. `rangeKey` is day-granular
  // (built from formatDateForAPI), which is load-bearing: the auto-select below
  // writes params via `applyParams` (re-resolving the dates with a fresh clock),
  // and day-granularity keeps `rangeKey` stable so that write does not re-trigger
  // this effect into a fetch loop.
  $effect(() => {
    void rangeKey;
    fetchAvailableSpecies();
    return () => speciesController?.abort();
  });

  // Roving-tabindex keyboard navigation for the tab bar. Focus moves by id so we
  // avoid holding element refs (and the array-index access that comes with them).
  function handleTabKeydown(event: KeyboardEvent): void {
    const currentIndex = TABS.findIndex(tab => tab.group === params.tab);
    if (currentIndex < 0) return;

    let nextIndex = currentIndex;
    switch (event.key) {
      case 'ArrowRight':
        nextIndex = (currentIndex + 1) % TABS.length;
        break;
      case 'ArrowLeft':
        nextIndex = (currentIndex - 1 + TABS.length) % TABS.length;
        break;
      case 'Home':
        nextIndex = 0;
        break;
      case 'End':
        nextIndex = TABS.length - 1;
        break;
      default:
        return;
    }
    event.preventDefault();
    const next = TABS.at(nextIndex);
    if (!next) return;
    selectTab(next.group);
    document.getElementById(`analytics-tab-${next.group}`)?.focus();
  }
</script>

<div class="col-span-12" role="region" aria-label={t('analytics.advanced.title')}>
  <!-- Tab bar: primary navigation, above the filters. Scrolls rather than wraps
       so longer locales (DE/FI) keep a single clean strip; overflow-y is hidden
       so the horizontal scroll container never shows a stray vertical bar. -->
  <div
    role="tablist"
    aria-label={t('analytics.hub.aria.tabs')}
    class="flex flex-nowrap gap-1 overflow-x-auto overflow-y-hidden border-b border-[var(--color-base-300)]/60"
  >
    {#each TABS as tab (tab.group)}
      {@const Icon = tab.icon}
      {@const isActive = params.tab === tab.group}
      <button
        type="button"
        role="tab"
        id={`analytics-tab-${tab.group}`}
        aria-selected={isActive}
        aria-controls={`analytics-panel-${tab.group}`}
        tabindex={isActive ? 0 : -1}
        class="inline-flex shrink-0 items-center gap-2 whitespace-nowrap px-4 py-2.5 text-sm font-medium border-b-2 -mb-px transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[var(--color-primary)] rounded-t-md {isActive
          ? 'border-[var(--color-primary)] text-[var(--color-primary)]'
          : 'border-transparent text-[var(--color-base-content)] opacity-70 hover:opacity-100 hover:border-[var(--color-base-300)]'}"
        onclick={() => selectTab(tab.group)}
        onkeydown={handleTabKeydown}
      >
        <Icon class="h-4 w-4" aria-hidden="true" />
        <span>{t(tab.labelKey)}</span>
      </button>
    {/each}
  </div>

  <!-- Compact filter toolbar (below the tabs) -->
  <div class="mt-3">
    <AnalyticsControlBar
      {params}
      {availableSpecies}
      {loadingSpecies}
      {speciesApplicable}
      onParamsChange={partial => applyParams(partial, 'push')}
    />
  </div>

  <!-- Active tab panel: only this tab's charts are mounted -->
  <div
    role="tabpanel"
    id={`analytics-panel-${params.tab}`}
    aria-labelledby={`analytics-tab-${params.tab}`}
    tabindex="0"
    class="mt-6 focus-visible:outline-none"
  >
    {#if activeCharts.length === 0}
      <!-- Coming soon: populated in later PRs. Never a blank wall. -->
      <div
        class="bg-[var(--color-base-100)] rounded-xl shadow-sm border border-[var(--color-base-200)] p-12"
        role="status"
      >
        <div class="flex flex-col items-center text-center text-[var(--color-base-content)]">
          <Construction class="h-12 w-12 mb-4 opacity-40" aria-hidden="true" />
          <p class="text-lg font-medium mb-1">{t(activeTabLabelKey)}</p>
          <p class="text-sm opacity-70 max-w-md">{t('analytics.hub.comingSoon.description')}</p>
        </div>
      </div>
    {:else}
      <div class="grid grid-cols-1 lg:grid-cols-2 gap-6">
        {#each activeCharts as chart (chart.id)}
          <div class={chart.size === 'normal' ? 'lg:col-span-1' : 'lg:col-span-2'}>
            <ChartCard
              {chart}
              {params}
              {speciesNames}
              speciesLoading={loadingSpecies}
              onParamsChange={partial => applyParams(partial, 'push')}
            />
          </div>
        {/each}
      </div>
    {/if}
  </div>
</div>
