/**
 * Analytics chart registry.
 *
 * Single source of truth for which charts render in which tab. PR0 migrates the
 * three existing Advanced Analytics charts onto this registry with no behavior
 * change: the fetchers below are lifted verbatim from the legacy Advanced
 * Analytics page (same endpoints, params, and parsing), and the `mapProps`
 * mappers reproduce the props the page used to pass each chart.
 *
 * Later PRs add Tier-1 charts to the empty `overview`/`quality` groups.
 */
import { buildAppUrl } from '$lib/utils/urlHelpers';
import { parseLocalDateString } from '$lib/utils/date';

import TimeOfDaySpeciesChart from '../components/charts/d3/TimeOfDaySpeciesChart.svelte';
import DailySpeciesTrendChart from '../components/charts/d3/DailySpeciesTrendChart.svelte';
import SpeciesDiversityChart from '../components/charts/d3/SpeciesDiversityChart.svelte';
import SeasonalHeatmap from '../components/charts/d3/SeasonalHeatmap.svelte';
import type { HeatmapData } from '../components/charts/d3/utils/heatmap';
import TrendChartOptions from '../components/TrendChartOptions.svelte';

import { formatDateForAPI } from './analyticsParams';
import type { AnalyticsParams, ChartDef } from './types';

// --- Fetch result shapes (network layer, before name enrichment) ----------

interface TimeOfDayDatum {
  hour: number;
  count: number;
}
interface TimeOfDaySeries {
  species: string;
  data: TimeOfDayDatum[];
}

interface DailyTrendDatum {
  date: Date;
  count: number;
}
interface DailyTrendSeries {
  species: string;
  data: DailyTrendDatum[];
}

interface DiversityDatum {
  date: Date;
  uniqueSpecies: number;
}

// Raw API response fragments we read defensively.
interface SpeciesDailyData {
  data?: unknown;
}
interface DailyDataItem {
  date: string;
  count?: number;
}
interface DiversityResponse {
  data?: { date: string; unique_species: number }[];
}

function ensureOk(response: Response): void {
  if (!response.ok) {
    throw new Error(`HTTP ${response.status}: ${response.statusText}`);
  }
}

// --- Fetchers (verbatim endpoints/params from the legacy page) -------------

/**
 * Hourly detection counts for the range's start date, per selected species.
 * Endpoint and the single-date semantics match the legacy page exactly.
 */
async function fetchTimeOfDay(
  params: AnalyticsParams,
  signal?: AbortSignal
): Promise<TimeOfDaySeries[]> {
  if (params.species.length === 0) return [];

  const search = new URLSearchParams({
    date: formatDateForAPI(params.startDate),
    min_confidence: '0',
  });
  params.species.forEach(name => search.append('species', name));

  const response = await fetch(buildAppUrl(`/api/v2/analytics/time/hourly/batch?${search}`), {
    signal,
  });
  ensureOk(response);

  const data: unknown = await response.json();
  if (!data || typeof data !== 'object' || Array.isArray(data)) {
    throw new Error('Invalid hourly batch response: expected an object');
  }

  return Object.entries(data as Record<string, unknown>).map(([species, hourlyData]) => ({
    species,
    data: Array.isArray(hourlyData)
      ? (hourlyData as TimeOfDayDatum[]).map(item => ({
          hour: typeof item.hour === 'number' ? item.hour : 0,
          count: typeof item.count === 'number' ? item.count : 0,
        }))
      : [],
  }));
}

/** Daily detection counts across the range, per selected species. */
async function fetchDailyTrend(
  params: AnalyticsParams,
  signal?: AbortSignal
): Promise<DailyTrendSeries[]> {
  if (params.species.length === 0) return [];

  const search = new URLSearchParams({
    start_date: formatDateForAPI(params.startDate),
    end_date: formatDateForAPI(params.endDate),
  });
  params.species.forEach(name => search.append('species', name));

  const response = await fetch(buildAppUrl(`/api/v2/analytics/time/daily/batch?${search}`), {
    signal,
  });
  ensureOk(response);

  const data: unknown = await response.json();
  if (!data || typeof data !== 'object' || Array.isArray(data)) {
    throw new Error('Invalid daily batch response: expected an object');
  }

  return Object.entries(data as Record<string, unknown>).map(([species, trendData]) => {
    if (!trendData || typeof trendData !== 'object') {
      return { species, data: [] };
    }
    const apiData = trendData as SpeciesDailyData;
    const dataArray: unknown[] = Array.isArray(apiData.data) ? apiData.data : [];

    return {
      species,
      data: dataArray
        .map(raw => {
          if (!raw || typeof raw !== 'object') return null;
          const item = raw as DailyDataItem;
          const date = parseLocalDateString(item.date);
          const count = typeof item.count === 'number' ? item.count : 0;
          if (!date || isNaN(date.getTime())) return null;
          return { date, count };
        })
        .filter((item): item is DailyTrendDatum => item !== null),
    };
  });
}

/** Unique-species-per-day diversity across the range (species-independent). */
async function fetchDiversity(
  params: AnalyticsParams,
  signal?: AbortSignal
): Promise<DiversityDatum[]> {
  const search = new URLSearchParams({
    start_date: formatDateForAPI(params.startDate),
    end_date: formatDateForAPI(params.endDate),
  });

  const response = await fetch(buildAppUrl(`/api/v2/analytics/species/diversity?${search}`), {
    signal,
  });
  ensureOk(response);

  const data: unknown = await response.json();
  if (!data || typeof data !== 'object' || Array.isArray(data)) {
    throw new Error('Invalid diversity response: expected an object');
  }

  const result = data as DiversityResponse;
  return (result.data ?? [])
    .map(item => {
      const date = parseLocalDateString(item.date);
      if (!date || isNaN(date.getTime())) return null;
      // Number.isFinite is false for non-numbers (no coercion) and NaN/Infinity.
      const uniqueSpecies = Number.isFinite(item.unique_species) ? item.unique_species : 0;
      return { date, uniqueSpecies };
    })
    .filter((item): item is DiversityDatum => item !== null);
}

/** Keeps only finite numbers from an unknown value; drops anything malformed. */
function asNumberArray(value: unknown): number[] {
  return Array.isArray(value)
    ? value.filter((n): n is number => typeof n === 'number' && Number.isFinite(n))
    : [];
}

interface HeatmapResponse {
  dates?: unknown;
  slotResolutionMinutes?: unknown;
  cells?: { dateIndex?: unknown; slot?: unknown; count?: unknown };
}

const DEFAULT_SLOT_RESOLUTION_MINUTES = 15;

/**
 * Seasonal density heatmap: detection counts by (date, intra-day slot). Honors an optional
 * single-species filter (the control bar's first selected species; none means all species).
 * Returns the server's columnar sparse payload, defensively coerced.
 */
async function fetchHeatmap(params: AnalyticsParams, signal?: AbortSignal): Promise<HeatmapData> {
  const search = new URLSearchParams({
    start_date: formatDateForAPI(params.startDate),
    end_date: formatDateForAPI(params.endDate),
  });
  // The endpoint filters by a single species; use the first selected (none = all species).
  if (params.species.length > 0) search.append('species', params.species[0]);

  const response = await fetch(buildAppUrl(`/api/v2/analytics/time/heatmap?${search}`), { signal });
  ensureOk(response);

  const data: unknown = await response.json();
  if (!data || typeof data !== 'object' || Array.isArray(data)) {
    throw new Error('Invalid heatmap response: expected an object');
  }
  const body = data as HeatmapResponse;
  const cells = (body.cells ?? {}) as { dateIndex?: unknown; slot?: unknown; count?: unknown };

  return {
    dates: Array.isArray(body.dates)
      ? body.dates.filter((d): d is string => typeof d === 'string')
      : [],
    slotResolutionMinutes:
      typeof body.slotResolutionMinutes === 'number'
        ? body.slotResolutionMinutes
        : DEFAULT_SLOT_RESOLUTION_MINUTES,
    cells: {
      dateIndex: asNumberArray(cells.dateIndex),
      slot: asNumberArray(cells.slot),
      count: asNumberArray(cells.count),
    },
  };
}

// --- Registry --------------------------------------------------------------

export const CHART_REGISTRY: ChartDef[] = [
  {
    id: 'seasonal-heatmap',
    group: 'patterns',
    titleKey: 'analytics.advanced.charts.heatmap.title',
    descKey: 'analytics.advanced.charts.heatmap.description',
    emptyKey: 'analytics.advanced.charts.heatmap.noData',
    emptyHintKey: 'analytics.advanced.charts.heatmap.noDataHint',
    component: SeasonalHeatmap,
    fetch: fetchHeatmap,
    // The fetch result is the columnar payload (an object), so count its non-zero cells rather
    // than relying on ChartCard's default array-length count.
    countDataPoints: data => (data as HeatmapData).cells.count.length,
    size: 'full',
    supports: { species: true, source: false },
    minDataPoints: 20,
    export: 'csv',
  },
  {
    id: 'time-of-day-species',
    group: 'patterns',
    titleKey: 'analytics.advanced.charts.timeOfDay.title',
    descKey: 'analytics.advanced.charts.timeOfDay.description',
    emptyKey: 'analytics.advanced.charts.timeOfDay.noData',
    emptyHintKey: 'analytics.advanced.charts.timeOfDay.noDataHint',
    component: TimeOfDaySpeciesChart,
    fetch: fetchTimeOfDay,
    mapProps: (data, params, ctx) => ({
      data: (data as TimeOfDaySeries[]).map(series => ({
        species: series.species,
        commonName: ctx.speciesNames.get(series.species) ?? series.species,
        data: series.data,
        visible: true,
      })),
      selectedSpecies: params.species,
    }),
    size: 'full',
    supports: { species: true, source: false },
  },
  {
    id: 'daily-species-trend',
    group: 'trends',
    titleKey: 'analytics.advanced.charts.dailyTrend.title',
    descKey: 'analytics.advanced.charts.dailyTrend.description',
    emptyKey: 'analytics.advanced.charts.dailyTrend.noData',
    emptyHintKey: 'analytics.advanced.charts.dailyTrend.noDataHint',
    component: DailySpeciesTrendChart,
    controls: TrendChartOptions,
    defaultOptions: { showRelative: false, enableZoom: true, enableBrush: false },
    fetch: fetchDailyTrend,
    mapProps: (data, params, ctx) => ({
      data: (data as DailyTrendSeries[]).map(series => ({
        species: series.species,
        commonName: ctx.speciesNames.get(series.species) ?? series.species,
        data: series.data,
        visible: true,
      })),
      selectedSpecies: params.species,
      dateRange: [params.startDate, params.endDate] as [Date, Date],
      showRelative: Boolean(ctx.options.showRelative),
      enableZoom: Boolean(ctx.options.enableZoom),
      enableBrush: Boolean(ctx.options.enableBrush),
      onDateRangeChange: (range: [Date, Date]) =>
        ctx.onParamsChange({
          range: 'custom',
          start: formatDateForAPI(range[0]),
          end: formatDateForAPI(range[1]),
        }),
    }),
    size: 'full',
    supports: { species: true, source: false },
  },
  {
    id: 'species-diversity',
    group: 'biodiversity',
    titleKey: 'analytics.advanced.charts.diversity.title',
    descKey: 'analytics.advanced.charts.diversity.description',
    emptyKey: 'analytics.advanced.charts.diversity.noData',
    emptyHintKey: 'analytics.advanced.charts.diversity.noDataHint',
    component: SpeciesDiversityChart,
    fetch: fetchDiversity,
    mapProps: (data, params) => ({
      data,
      dateRange: [params.startDate, params.endDate] as [Date, Date],
    }),
    size: 'full',
    supports: { species: false, source: false },
  },
];

/** Charts registered for a given tab group, in registry order. */
export function chartsForGroup(group: ChartDef['group']): ChartDef[] {
  return CHART_REGISTRY.filter(chart => chart.group === group);
}

/** Whether any registered chart belongs to the given group. */
export function groupHasCharts(group: ChartDef['group']): boolean {
  return CHART_REGISTRY.some(chart => chart.group === group);
}
