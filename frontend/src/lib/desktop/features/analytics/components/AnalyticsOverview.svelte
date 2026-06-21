<!--
  Analytics hub: Overview tab.

  The old standalone Analytics overview page (summary StatCards, top-species /
  time-of-day / trend / new-species charts, and the recent-detections table),
  folded into the tabbed hub as the Overview tab (PR0b). The only behavioral
  change from the old page is that the date range now comes from the hub's
  shared control bar (AnalyticsParams) instead of a per-page FilterForm, so the
  whole hub shares one date control. Data fetching, transforms, charts, and the
  recent-detections table are carried over unchanged.
-->
<script lang="ts">
  import { untrack, type Snippet } from 'svelte';
  import { XCircle } from '@lucide/svelte';

  import { t, type TranslationKey } from '$lib/i18n';
  import { api } from '$lib/utils/api';
  import { formatNumber, formatDateTime } from '$lib/utils/formatters';
  import { getLogger } from '$lib/utils/logger';
  import { buildAppUrl } from '$lib/utils/urlHelpers';
  import { localizeSpeciesName } from '$lib/utils/speciesDisplay';
  import { handleBirdImageError } from '$lib/desktop/components/ui/image-utils';
  import LoadingSpinner from '$lib/desktop/components/ui/LoadingSpinner.svelte';
  import SourceBadge from '$lib/desktop/features/dashboard/components/SourceBadge.svelte';
  import type { SourceInfo } from '$lib/types/detection.types';

  import StatCard from './ui/StatCard.svelte';
  import BarChart from './charts/d3/BarChart.svelte';
  import LineChart from './charts/d3/LineChart.svelte';
  import NewSpeciesTimelineChart from './charts/d3/NewSpeciesTimelineChart.svelte';
  import {
    bucketHourlyByPeriod,
    aggregateTrendPoints,
    mapNewSpecies,
  } from '../utils/analyticsTransforms';
  import { formatDateForAPI } from '../registry/analyticsParams';
  import type { AnalyticsParams, DateRangePreset } from '../registry/types';

  interface Props {
    /** Shared hub params; only the resolved date range drives this tab. */
    params: AnalyticsParams;
  }

  let { params }: Props = $props();

  const logger = getLogger('analytics-overview');

  // Cap the new-species timeline to the most recent N entries to keep the chart
  // readable (matches the prior overview page's 20-item limit).
  const NEW_SPECIES_LIMIT = 20;

  // Period subtitle for the StatCards, derived from the shared range preset.
  // Reuses the existing analytics.periods.* strings; `quarter` (90 days) maps to
  // the legacy "Last 90 Days" label.
  const RANGE_LABEL_KEYS: Record<DateRangePreset, TranslationKey> = {
    week: 'analytics.periods.lastWeek',
    month: 'analytics.periods.lastMonth',
    quarter: 'analytics.periods.last90Days',
    year: 'analytics.periods.lastYear',
    custom: 'analytics.periods.customRange',
  };

  // Type definitions
  interface Summary {
    totalDetections: number;
    uniqueSpecies: number;
    avgConfidence: number;
    mostCommonSpecies: string;
    // Canonical scientific name of the most-common species, so the displayed
    // common name can localize per visitor while the lookup stays canonical.
    mostCommonScientific: string;
    mostCommonCount: number;
  }

  interface Detection {
    id: string;
    timestamp: string | null;
    commonName: string;
    scientificName: string;
    confidence: number;
    timeOfDay: string;
    source?: SourceInfo | null;
  }

  // API response type (may have date/time instead of timestamp)
  interface ApiDetection {
    id: string;
    timestamp?: string;
    date?: string;
    time?: string;
    commonName: string;
    scientificName: string;
    confidence: number;
    timeOfDay?: string;
    source?: SourceInfo | null;
  }

  interface SpeciesData {
    common_name: string;
    scientific_name?: string;
    count: number;
    avg_confidence: number;
  }

  interface TimeOfDayData {
    hour: number;
    count: number;
  }

  interface TrendData {
    data: {
      date: string;
      count: number;
    }[];
  }

  interface NewSpeciesData {
    common_name: string;
    scientific_name: string;
    first_heard_date: string;
  }

  interface ChartData {
    species: SpeciesData[];
    timeOfDay: TimeOfDayData[];
    trend: TrendData | null;
    newSpecies: NewSpeciesData[];
  }

  // State variables
  let isLoading = $state<boolean>(true);
  let error = $state<string | null>(null);

  // Summary data
  let summary = $state<Summary>({
    totalDetections: 0,
    uniqueSpecies: 0,
    avgConfidence: 0,
    mostCommonSpecies: '',
    mostCommonScientific: '',
    mostCommonCount: 0,
  });

  // Data arrays
  let recentDetections = $state<Detection[]>([]);

  // Chart data storage
  let chartData = $state<ChartData>({
    species: [],
    timeOfDay: [],
    trend: null,
    newSpecies: [],
  });

  // Monotonic token guarding against stale-response races. Each fetchData() run
  // captures the latest value; a fetcher only commits its result when its captured
  // token still matches, so a slower earlier request can no longer overwrite the
  // data from a newer one after rapid range changes. Non-reactive on purpose.
  let analyticsFetchSeq = 0;

  // Derived chart inputs built from the reactive chartData via pure transforms.
  // Species distribution: sorted desc by count, mapped to labelled bars with
  // per-species colors from the D3 theme palette (applied inside BarChart).
  const speciesBars = $derived(
    [...(chartData.species ?? [])]
      .sort((a, b) => b.count - a.count)
      .map(s => ({ label: localizeSpeciesName(s.scientific_name, s.common_name), value: s.count }))
  );

  // Time of day: hourly counts bucketed into the six fixed periods. The period
  // display labels are localized at render time so the pure transform stays
  // i18n-free. The map is keyed on the transform's stable English labels, so the
  // localization is robust to bucket order or length; an unmapped label falls
  // back to its English text. The two Night labels (0-4 and 20-23) must remain
  // distinct in every locale: the BarChart uses the label as its d3 band-scale
  // domain key, so identical strings would collapse the two buckets into one bar.
  const TIME_OF_DAY_PERIOD_LABEL_KEYS = new Map<string, TranslationKey>([
    ['Night (0-4)', 'analytics.timeOfDayPeriods.night0to4'],
    ['Dawn (5-8)', 'analytics.timeOfDayPeriods.dawn5to8'],
    ['Morning (9-11)', 'analytics.timeOfDayPeriods.morning9to11'],
    ['Afternoon (12-16)', 'analytics.timeOfDayPeriods.afternoon12to16'],
    ['Evening (17-19)', 'analytics.timeOfDayPeriods.evening17to19'],
    ['Night (20-23)', 'analytics.timeOfDayPeriods.night20to23'],
  ]);
  const timeOfDayBars = $derived.by(() =>
    bucketHourlyByPeriod(chartData.timeOfDay).map(bucket => {
      const key = TIME_OF_DAY_PERIOD_LABEL_KEYS.get(bucket.label);
      return { label: key ? t(key) : bucket.label, value: bucket.value };
    })
  );

  // Detection trend: aggregated/sorted daily points, wrapped as a single series.
  const trendSeries = $derived([
    {
      id: 'daily',
      label: t('analytics.charts.dailyDetections'),
      data: aggregateTrendPoints(chartData.trend),
    },
  ]);

  // New species: API rows mapped to { commonName, scientificName, firstHeard }.
  // Sorted desc by date then limited to the most recent NEW_SPECIES_LIMIT, to
  // match the original chart which capped the display at 20 species.
  const newSpeciesPoints = $derived(
    mapNewSpecies(chartData.newSpecies)
      .sort((a, b) => b.firstHeard.getTime() - a.firstHeard.getTime())
      .slice(0, NEW_SPECIES_LIMIT)
  );

  // Explicit date range for the time-based charts, taken from the shared params.
  const chartDateRange = $derived<[Date, Date]>([params.startDate, params.endDate]);

  // Subtitle shown on every StatCard, describing the active range.
  const periodLabel = $derived(t(RANGE_LABEL_KEYS[params.range]));

  // Format percentage
  function formatPercentage(value: number): string {
    return (value * 100).toFixed(1) + '%';
  }

  // Refetch whenever the resolved date range changes. Day-granular so a same-day
  // re-resolve of the Date objects does not spuriously refetch. The first run
  // (on mount) performs the initial load, so no separate onMount is needed.
  const rangeKey = $derived(
    `${formatDateForAPI(params.startDate)}|${formatDateForAPI(params.endDate)}`
  );
  $effect(() => {
    void rangeKey;
    untrack(() => fetchData());
  });

  // Fetch all data for the current range.
  async function fetchData() {
    const snapshot = untrack(() => params);
    const startDate = formatDateForAPI(snapshot.startDate);
    const endDate = formatDateForAPI(snapshot.endDate);

    const seq = ++analyticsFetchSeq;
    isLoading = true;
    error = null;

    // Clear the previous range's data up front so a slow or failing request can
    // never leave stale metrics on screen (the loading overlays mask the blank
    // slate). Each fetcher then only commits success; failures just log + rethrow.
    summary = {
      totalDetections: 0,
      uniqueSpecies: 0,
      avgConfidence: 0,
      mostCommonSpecies: '',
      mostCommonScientific: '',
      mostCommonCount: 0,
    };
    recentDetections = [];
    chartData = { species: [], timeOfDay: [], trend: null, newSpecies: [] };

    logger.debug('Loading overview analytics', { startDate, endDate });

    // Each fetcher commits only for the latest run (the sequence-token guard)
    // and rethrows on failure after logging, so allSettled lets us tell whether
    // the whole load failed. Only the most recent run owns the shared loading /
    // error state; a superseded run bails before touching it.
    const results = await Promise.allSettled([
      fetchSummaryData(seq, startDate, endDate),
      fetchSpeciesSummary(seq, startDate, endDate),
      fetchRecentDetections(seq),
      fetchTimeOfDayData(seq, startDate, endDate),
      fetchTrendData(seq, startDate, endDate),
      fetchNewSpeciesData(seq, startDate, endDate),
    ]);

    if (seq !== analyticsFetchSeq) return;

    // Surface a global error only when every request failed; a partial failure
    // is communicated by the affected chart's own empty state, so a full-page
    // error banner over otherwise-loaded data would be misleading.
    if (results.every(result => result.status === 'rejected')) {
      error = t('analytics.loadingError');
    }
    isLoading = false;
  }

  // Fetch summary metrics
  async function fetchSummaryData(seq: number, startDate: string, endDate: string) {
    try {
      const search = new URLSearchParams();
      if (startDate) search.set('start_date', startDate);
      if (endDate) search.set('end_date', endDate);

      const url = `/api/v2/analytics/species/summary?${search}`;
      const speciesData = await api.get<SpeciesData[]>(url);
      if (seq !== analyticsFetchSeq) return; // superseded by a newer fetch
      const speciesArray = Array.isArray(speciesData) ? speciesData : [];

      // Calculate summary metrics
      let totalDetections = 0;
      let totalConfidence = 0;
      let mostCommonSpecies = '';
      let mostCommonScientific = '';
      let mostCommonCount = 0;

      speciesArray.forEach(species => {
        const count = species.count || 0;
        const confidence = species.avg_confidence || 0;

        totalDetections += count;
        totalConfidence += confidence * count;

        if (count > mostCommonCount) {
          mostCommonCount = count;
          mostCommonSpecies = species.common_name || t('analytics.recentDetections.unknown');
          mostCommonScientific = species.scientific_name || '';
        }
      });

      summary = {
        totalDetections,
        uniqueSpecies: speciesArray.length,
        avgConfidence: totalDetections > 0 ? totalConfidence / totalDetections : 0,
        mostCommonSpecies,
        mostCommonScientific,
        mostCommonCount,
      };
    } catch (err) {
      if (seq !== analyticsFetchSeq) return; // superseded by a newer fetch
      logger.error('Error fetching summary data:', err);
      throw err; // rethrow so fetchData can detect a total-failure load
    }
  }

  // Fetch species summary for chart
  async function fetchSpeciesSummary(seq: number, startDate: string, endDate: string) {
    try {
      const search = new URLSearchParams({ limit: '10' });
      if (startDate) search.set('start_date', startDate);
      if (endDate) search.set('end_date', endDate);

      const url = `/api/v2/analytics/species/summary?${search}`;
      const speciesData = await api.get<SpeciesData[]>(url);
      if (seq !== analyticsFetchSeq) return; // superseded by a newer fetch
      chartData.species = Array.isArray(speciesData) ? speciesData : [];
    } catch (err) {
      if (seq !== analyticsFetchSeq) return; // superseded by a newer fetch
      logger.error('Error fetching species summary:', err);
      throw err; // rethrow so fetchData can detect a total-failure load
    }
  }

  // Fetch recent detections
  async function fetchRecentDetections(seq: number) {
    try {
      const data = await api.get<ApiDetection[]>('/api/v2/detections/recent?limit=10');
      if (seq !== analyticsFetchSeq) return; // superseded by a newer fetch
      const detections = Array.isArray(data) ? data : [];

      recentDetections = detections.map(detection => {
        // Compute timestamp once to avoid 'undefined undefined' edge case
        const computedTimestamp =
          detection.timestamp ||
          (detection.date && detection.time ? `${detection.date}T${detection.time}` : null);

        return {
          id: detection.id,
          timestamp: computedTimestamp,
          commonName: detection.commonName,
          scientificName: detection.scientificName,
          confidence: detection.confidence,
          timeOfDay:
            detection.timeOfDay || (computedTimestamp ? calculateTimeOfDay(computedTimestamp) : ''),
          source: detection.source ?? null,
        };
      });
    } catch (err) {
      if (seq !== analyticsFetchSeq) return; // superseded by a newer fetch
      logger.error('Error fetching recent detections:', err);
      throw err; // rethrow so fetchData can detect a total-failure load
    }
  }

  // Calculate time of day from timestamp
  function calculateTimeOfDay(timestamp: string) {
    const date = new Date(timestamp);
    const hour = date.getHours();

    if (hour >= 5 && hour < 8) return 'Sunrise';
    if (hour >= 8 && hour < 17) return 'Day';
    if (hour >= 17 && hour < 20) return 'Sunset';
    return 'Night';
  }

  // Fetch time of day data
  async function fetchTimeOfDayData(seq: number, startDate: string, endDate: string) {
    try {
      const search = new URLSearchParams();
      if (startDate) search.set('start_date', startDate);
      if (endDate) search.set('end_date', endDate);

      const url = `/api/v2/analytics/time/distribution/hourly?${search}`;
      const timeData = await api.get<TimeOfDayData[]>(url);
      if (seq !== analyticsFetchSeq) return; // superseded by a newer fetch
      chartData.timeOfDay = Array.isArray(timeData) ? timeData : [];
    } catch (err) {
      if (seq !== analyticsFetchSeq) return; // superseded by a newer fetch
      logger.error('Error fetching time of day data:', err);
      throw err; // rethrow so fetchData can detect a total-failure load
    }
  }

  // Fetch trend data
  async function fetchTrendData(seq: number, startDate: string, endDate: string) {
    try {
      const search = new URLSearchParams();

      // The daily analytics endpoint requires a start_date parameter; the shared
      // range always resolves one, but keep the legacy 30-day fallback for safety.
      if (startDate) {
        search.set('start_date', startDate);
      } else {
        const defaultStart = new Date();
        defaultStart.setDate(defaultStart.getDate() - 30);
        search.set('start_date', formatDateForAPI(defaultStart));
      }

      if (endDate) search.set('end_date', endDate);

      const url = `/api/v2/analytics/time/daily?${search}`;
      const trendData = await api.get<TrendData>(url);
      if (seq !== analyticsFetchSeq) return; // superseded by a newer fetch
      chartData.trend = trendData ?? { data: [] };
    } catch (err) {
      if (seq !== analyticsFetchSeq) return; // superseded by a newer fetch
      logger.error('Error fetching trend data:', err);
      throw err; // rethrow so fetchData can detect a total-failure load
    }
  }

  // Fetch new species data
  async function fetchNewSpeciesData(seq: number, startDate: string, endDate: string) {
    try {
      const search = new URLSearchParams();
      if (startDate) search.set('start_date', startDate);
      if (endDate) search.set('end_date', endDate);

      const url = `/api/v2/analytics/species/detections/new?${search}`;
      const data = await api.get<NewSpeciesData[]>(url);
      if (seq !== analyticsFetchSeq) return; // superseded by a newer fetch
      chartData.newSpecies = Array.isArray(data) ? data : [];
    } catch (err) {
      if (seq !== analyticsFetchSeq) return; // superseded by a newer fetch
      logger.error('Error fetching new species data:', err);
      throw err; // rethrow so fetchData can detect a total-failure load
    }
  }
</script>

<div class="space-y-4">
  {#if error}
    <div class="alert alert-error">
      <XCircle class="size-6" />
      <span>{error}</span>
    </div>
  {/if}

  <!-- Summary Stats Cards -->
  <div class="grid gap-4 summary-cards-grid">
    <!-- Total Detections Card -->
    <StatCard
      title={t('analytics.stats.totalDetections')}
      value={formatNumber(summary.totalDetections)}
      subtitle={periodLabel}
      iconClassName="bg-[var(--color-primary)]/20"
      {isLoading}
    >
      {#snippet icon()}
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="h-6 w-6 text-[var(--color-primary)]"
          viewBox="0 0 921.998 921.998"
          fill="currentColor"
          aria-hidden="true"
        >
          <path
            d="M869.694,385.652c-11.246-12.453-132.373-110.907-154.023-117.272c-9.421-2.735-18.892-4.447-28.681-5.164
              c-45.272-3.315-95.213,10.875-126.684,44.794c-2.741,2.956-4.311,4.645-4.311,4.645s1.172-1.996,3.224-5.488
              c9.706-16.365,23.847-30.577,38.989-41.956c6.979-5.243,14.37-9.937,22.088-14.014c2.116-1.118,21.797-11.751,23.12-10.357
              c-0.003-0.003-10.744-11.33-10.744-11.33c-17.273-17.276-35.963-32.167-61.415-32.167c-31.547,0-58.505,19.559-69.472,47.201
              c-9.306-6.917-24.11-11.392-40.788-11.392c-16.678,0-31.481,4.475-40.788,11.392c-10.967-27.643-37.925-47.201-69.472-47.201
              c-25.452,0-44.142,14.891-61.416,32.166c0,0-10.741,11.327-10.744,11.33c1.322-1.395,21.003,9.239,23.12,10.357
              c7.718,4.077,15.109,8.771,22.088,14.014c15.145,11.378,29.283,25.591,38.989,41.956c2.052,3.493,3.224,5.488,3.224,5.488
              s-1.566-1.689-4.31-4.645c-31.471-33.919-81.411-48.109-126.683-44.794c-9.789,0.717-19.26,2.429-28.681,5.164
              c-21.651,6.365-142.778,104.819-154.023,117.272C19.797,421.645,0,469.336,0,521.655c0,112.112,90.886,203,203,203
              c102.56,0,187.34-76.062,201.048-174.851c15.983,11.645,35.663,18.52,56.951,18.52c21.289,0,40.968-6.875,56.951-18.52
              c13.708,98.788,98.487,174.851,201.048,174.851c112.114,0,203-90.888,203-203C921.996,469.336,902.199,421.647,869.694,385.652z
              M198.497,649.155c-67.611,0-122.421-54.811-122.421-122.421s54.81-122.42,122.421-122.42s122.421,54.81,122.421,122.42
              S266.108,649.155,198.497,649.155z M460.997,515.234c-17.833,0-32.29-14.457-32.29-32.29s14.457-32.289,32.29-32.289
              s32.29,14.457,32.29,32.289C493.287,500.777,478.83,515.234,460.997,515.234z M723.497,649.155
              c-67.611,0-122.421-54.811-122.421-122.421s54.81-122.42,122.421-122.42s122.421,54.81,122.421,122.42
              S791.108,649.155,723.497,649.155z"
          />
        </svg>
      {/snippet}
    </StatCard>

    <!-- Unique Species Card -->
    <StatCard
      title={t('analytics.stats.uniqueSpecies')}
      value={formatNumber(summary.uniqueSpecies)}
      subtitle={periodLabel}
      iconClassName="bg-[var(--color-secondary)]/20"
      {isLoading}
    >
      {#snippet icon()}
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="h-6 w-6 text-[var(--color-secondary)]"
          viewBox="0 0 256 256"
          fill="currentColor"
          aria-hidden="true"
        >
          <path
            d="M236.4375,73.34375,213.207,57.85547A60.00943,60.00943,0,0,0,96,76V93.19385L1.75293,211.00244A7.99963,7.99963,0,0,0,8,224H112A104.11791,104.11791,0,0,0,216,120V100.28125l20.4375-13.625a7.99959,7.99959,0,0,0,0-13.3125Zm-126.292,67.77783-40,48a7.99987,7.99987,0,0,1-12.291-10.24316l40-48a7.99987,7.99987,0,0,1,12.291,10.24316ZM164,80a12,12,0,1,1,12-12A12,12,0,0,1,164,80Z"
          />
        </svg>
      {/snippet}
    </StatCard>

    <!-- Average Confidence Card -->
    <StatCard
      title={t('analytics.stats.avgConfidence')}
      value={formatPercentage(summary.avgConfidence)}
      subtitle={periodLabel}
      iconClassName="bg-[var(--color-accent)]/20"
      {isLoading}
    >
      {#snippet icon()}
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="h-6 w-6 text-[var(--color-accent)]"
          viewBox="0 0 20 20"
          fill="currentColor"
          aria-hidden="true"
        >
          <path
            fill-rule="evenodd"
            d="M18 10a8 8 0 11-16 0 8 8 0 0116 0zm-7-4a1 1 0 11-2 0 1 1 0 012 0zM9 9a.75.75 0 000 1.5h.253a.25.25 0 01.244.304l-.459 2.066A1.75 1.75 0 0010.747 15H11a.75.75 0 000-1.5h-.253a.25.25 0 01-.244-.304l.459-2.066A1.75 1.75 0 009.253 9H9z"
            clip-rule="evenodd"
          />
        </svg>
      {/snippet}
    </StatCard>

    <!-- Most Common Species Card -->
    <StatCard
      title={t('analytics.stats.mostCommon')}
      value={summary.mostCommonCount > 0
        ? localizeSpeciesName(summary.mostCommonScientific, summary.mostCommonSpecies)
        : t('analytics.stats.none')}
      subtitle={summary.mostCommonCount > 0
        ? formatNumber(summary.mostCommonCount) + ' ' + t('analytics.stats.detections')
        : ''}
      iconClassName="bg-[var(--color-success)]/20"
      valueClassName="text-lg truncate max-w-[150px]"
      {isLoading}
    >
      {#snippet icon()}
        <svg
          xmlns="http://www.w3.org/2000/svg"
          class="h-6 w-6 text-[var(--color-success)]"
          viewBox="0 0 20 20"
          fill="currentColor"
          aria-hidden="true"
        >
          <path
            fill-rule="evenodd"
            d="M3.293 9.707a1 1 0 010-1.414l6-6a1 1 0 011.414 0l6 6a1 1 0 01-1.414 1.414L11 5.414V17a1 1 0 11-2 0V5.414L4.707 9.707a1 1 0 01-1.414 0z"
            clip-rule="evenodd"
          />
        </svg>
      {/snippet}
    </StatCard>
  </div>

  <!-- Charts Section -->
  <div class="grid gap-4 charts-grid">
    <!-- Species Distribution Chart -->
    {@render chartCard(
      t('analytics.charts.top10Species'),
      'h-80',
      speciesChart,
      speciesBars.length === 0,
      t('analytics.charts.noDataAvailable')
    )}

    <!-- Time of Day Chart -->
    {@render chartCard(
      t('analytics.charts.detectionsByTimeOfDay'),
      'h-80',
      timeOfDayChart,
      timeOfDayBars.every(b => b.value === 0),
      t('analytics.charts.noDataAvailable')
    )}
  </div>

  <!-- Trend Chart -->
  {@render chartCard(
    t('analytics.charts.detectionTrends'),
    'h-80',
    trendChart,
    trendSeries[0].data.length === 0,
    t('analytics.charts.noDataAvailable')
  )}

  <!-- New Species Chart -->
  {@render chartCard(
    t('analytics.charts.newSpeciesDetected'),
    'h-96',
    newSpeciesChart,
    newSpeciesPoints.length === 0,
    t('analytics.charts.noNewSpecies')
  )}

  <!-- Data Table for Recent Detections -->
  <div class="card bg-[var(--color-base-100)] shadow-xs">
    <div class="card-body card-padding">
      <h2 class="card-title">{t('analytics.recentDetections.title')}</h2>
      {#if isLoading}
        <div class="flex justify-center items-center p-8">
          <LoadingSpinner size="lg" />
        </div>
      {:else}
        <!-- Desktop/tablet table -->
        <div class="overflow-x-auto hidden md:block">
          <table class="table w-full">
            <thead>
              <tr>
                <th>{t('analytics.recentDetections.headers.dateTime')}</th>
                <th>{t('analytics.recentDetections.headers.species')}</th>
                <th>{t('analytics.recentDetections.headers.confidence')}</th>
                <th>{t('analytics.recentDetections.headers.source')}</th>
                <th>{t('analytics.recentDetections.headers.timeOfDay')}</th>
              </tr>
            </thead>
            <tbody>
              {#each recentDetections as detection, index (detection.id ?? index)}
                <tr
                  class={index % 2 === 0
                    ? 'bg-[var(--color-base-100)]'
                    : 'bg-[var(--color-base-200)]'}
                >
                  <td>{detection.timestamp ? formatDateTime(detection.timestamp) : '-'}</td>
                  <td>
                    <div class="flex items-center gap-2">
                      <div class="w-8 h-8 rounded-full bg-[var(--color-base-200)] overflow-hidden">
                        <!-- PERFORMANCE OPTIMIZATION: Enhanced image loading for species thumbnails -->
                        <img
                          src={buildAppUrl(
                            `/api/v2/media/species-image?name=${encodeURIComponent(detection.scientificName ?? '')}`
                          )}
                          alt={detection.commonName ||
                            t('analytics.recentDetections.unknownSpecies')}
                          class="w-full h-full object-cover"
                          onerror={handleBirdImageError}
                          loading="lazy"
                          decoding="async"
                          fetchpriority="low"
                        />
                      </div>
                      <div>
                        <div class="font-medium">
                          {localizeSpeciesName(detection.scientificName, detection.commonName) ||
                            t('analytics.recentDetections.unknownSpecies')}
                        </div>
                        <div class="text-xs opacity-50">{detection.scientificName || ''}</div>
                      </div>
                    </div>
                  </td>
                  <td>
                    <div class="flex items-center gap-2">
                      <div class="w-16 h-4 rounded-full overflow-hidden bg-[var(--color-base-200)]">
                        <div
                          class="h-full {detection.confidence >= 0.8
                            ? 'bg-[var(--color-success)]'
                            : detection.confidence >= 0.4
                              ? 'bg-[var(--color-warning)]'
                              : 'bg-[var(--color-error)]'}"
                          style:width="{detection.confidence * 100}%"
                        ></div>
                      </div>
                      <span class="text-sm">{formatPercentage(detection.confidence)}</span>
                    </div>
                  </td>
                  <td>
                    <SourceBadge {detection} variant="inline" />
                    {#if !detection.source}
                      <span class="text-xs opacity-40">-</span>
                    {/if}
                  </td>
                  <td>{detection.timeOfDay || t('analytics.recentDetections.unknown')}</td>
                </tr>
              {:else}
                <tr>
                  <td
                    colspan="5"
                    class="text-center py-4 text-[var(--color-base-content)] opacity-50"
                    >{t('analytics.recentDetections.noRecentDetections')}</td
                  >
                </tr>
              {/each}
            </tbody>
          </table>
        </div>

        <!-- Mobile list -->
        <div class="md:hidden space-y-2">
          {#each recentDetections as detection, index (detection.id ?? index)}
            <div class="bg-[var(--color-base-100)] rounded-lg p-3">
              <div class="flex items-start gap-3">
                <!-- Thumbnail -->
                <div
                  class="w-10 h-10 rounded-full bg-[var(--color-base-200)] overflow-hidden shrink-0"
                >
                  <img
                    src={buildAppUrl(
                      `/api/v2/media/species-image?name=${encodeURIComponent(detection.scientificName ?? '')}`
                    )}
                    alt={detection.commonName || t('analytics.recentDetections.unknownSpecies')}
                    class="w-full h-full object-cover"
                    onerror={handleBirdImageError}
                    loading="lazy"
                    decoding="async"
                    fetchpriority="low"
                  />
                </div>
                <!-- Content -->
                <div class="flex-1 min-w-0">
                  <div class="text-sm text-[var(--color-base-content)]/70">
                    {detection.timestamp ? formatDateTime(detection.timestamp) : '-'}
                  </div>
                  <div class="font-medium leading-tight truncate">
                    {localizeSpeciesName(detection.scientificName, detection.commonName) ||
                      t('analytics.recentDetections.unknownSpecies')}
                  </div>
                  <div class="text-xs opacity-60 truncate">{detection.scientificName || ''}</div>
                  <div class="mt-2 flex items-center justify-between">
                    <!-- Confidence badge -->
                    <span
                      class="badge {detection.confidence >= 0.8
                        ? 'badge-success'
                        : detection.confidence >= 0.4
                          ? 'badge-warning'
                          : 'badge-error'}"
                    >
                      {formatPercentage(detection.confidence)}
                    </span>
                    <SourceBadge {detection} variant="inline" />
                    <span class="text-xs opacity-70"
                      >{detection.timeOfDay || t('analytics.recentDetections.unknown')}</span
                    >
                  </div>
                </div>
              </div>
            </div>
          {:else}
            <div class="text-center py-4 text-[var(--color-base-content)] opacity-50">
              {t('analytics.recentDetections.noRecentDetections')}
            </div>
          {/each}
        </div>
      {/if}
    </div>
  </div>
</div>

<!-- Shared card chrome for the overview charts (loading overlay + empty state). -->
{#snippet chartCard(
  title: string,
  chartHeight: string,
  chart: Snippet,
  empty: boolean,
  emptyMessage: string
)}
  <div class="card bg-[var(--color-base-100)] shadow-xs">
    <div class="card-body p-4 md:p-6">
      <h2 class="card-title">{title}</h2>
      {#if empty && !isLoading}
        <div class="text-center py-4 text-[var(--color-base-content)] opacity-50">
          {emptyMessage}
        </div>
      {:else}
        <div class="relative" aria-busy={isLoading}>
          <div class="chart-container {chartHeight}" class:invisible={isLoading}>
            {@render chart()}
          </div>
          {#if isLoading}
            <div class="absolute inset-0 flex justify-center items-center">
              <LoadingSpinner size="lg" />
            </div>
          {/if}
        </div>
      {/if}
    </div>
  </div>
{/snippet}

{#snippet speciesChart()}
  <BarChart
    data={speciesBars}
    orientation="horizontal"
    valueAxisLabel={t('analytics.charts.numberOfDetections')}
    valueTooltipLabel={t('analytics.charts.detections')}
    formatValue={formatNumber}
    ariaLabel={t('analytics.charts.top10Species')}
  />
{/snippet}

{#snippet timeOfDayChart()}
  <BarChart
    data={timeOfDayBars}
    orientation="vertical"
    valueAxisLabel={t('analytics.charts.numberOfDetections')}
    categoryAxisLabel={t('analytics.charts.timePeriod')}
    valueTooltipLabel={t('analytics.charts.detections')}
    formatValue={formatNumber}
    ariaLabel={t('analytics.charts.detectionsByTimeOfDay')}
  />
{/snippet}

{#snippet trendChart()}
  <LineChart
    series={trendSeries}
    valueAxisLabel={t('analytics.charts.numberOfDetections')}
    dateAxisLabel={t('analytics.charts.date')}
    valueTooltipLabel={t('analytics.charts.detections')}
    dateRange={chartDateRange}
    formatValue={formatNumber}
    ariaLabel={t('analytics.charts.detectionTrends')}
  />
{/snippet}

{#snippet newSpeciesChart()}
  <NewSpeciesTimelineChart
    data={newSpeciesPoints}
    dateRange={chartDateRange}
    firstHeardLabel={t('analytics.charts.firstHeard')}
    dateAxisLabel={t('analytics.charts.firstHeardDate')}
    ariaLabel={t('analytics.charts.newSpeciesDetected')}
  />
{/snippet}

<style>
  .card-padding {
    padding: 1rem;
  }

  @media (min-width: 768px) {
    .card-padding {
      padding: 1.5rem;
    }
  }

  /* Summary cards grid - matches grid-cols-1 md:grid-cols-2 lg:grid-cols-4 */
  .summary-cards-grid {
    display: grid;
    grid-template-columns: 1fr;
  }

  @media (min-width: 768px) {
    .summary-cards-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }

  @media (min-width: 1024px) {
    .summary-cards-grid {
      grid-template-columns: repeat(4, minmax(0, 1fr));
    }
  }

  /* Charts grid - matches grid-cols-1 lg:grid-cols-2 */
  .charts-grid {
    display: grid;
    grid-template-columns: 1fr;
  }

  @media (min-width: 1024px) {
    .charts-grid {
      grid-template-columns: repeat(2, minmax(0, 1fr));
    }
  }
</style>
