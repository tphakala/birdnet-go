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
import SpeciesRidgeline from '../components/charts/d3/SpeciesRidgeline.svelte';
import DawnChorusOnset from '../components/charts/d3/DawnChorusOnset.svelte';
import { onsetCount } from '../components/charts/d3/utils/dawnOnset';
import type { DawnOnsetData, DawnOnsetPoint } from '../components/charts/d3/utils/dawnOnset';
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

interface HeatmapResponse {
  dates?: unknown;
  slotResolutionMinutes?: unknown;
  cells?: { dateIndex?: unknown; slot?: unknown; count?: unknown };
}

const DEFAULT_SLOT_RESOLUTION_MINUTES = 15;
const VALID_SLOT_RESOLUTIONS = [15, 30, 60];

/**
 * Coerces the three parallel cell columns in lockstep. dateIndex, slot, and count are parallel
 * by contract, so a cell is kept only when all three values at that index are finite numbers;
 * coercing each column independently could drop a value from one column and shift the others
 * out of alignment, mispairing date/slot/count.
 */
function coerceCells(cells: { dateIndex?: unknown; slot?: unknown; count?: unknown }): {
  dateIndex: number[];
  slot: number[];
  count: number[];
} {
  const rawDateIndex = Array.isArray(cells.dateIndex) ? cells.dateIndex : [];
  const rawSlot = Array.isArray(cells.slot) ? cells.slot : [];
  const rawCount = Array.isArray(cells.count) ? cells.count : [];
  const n = Math.min(rawDateIndex.length, rawSlot.length, rawCount.length);

  const dateIndex: number[] = [];
  const slot: number[] = [];
  const count: number[] = [];
  /* eslint-disable security/detect-object-injection -- i is a bounded loop index over our own arrays */
  for (let i = 0; i < n; i++) {
    const di = rawDateIndex[i];
    const s = rawSlot[i];
    const c = rawCount[i];
    if (
      typeof di === 'number' &&
      Number.isFinite(di) &&
      typeof s === 'number' &&
      Number.isFinite(s) &&
      typeof c === 'number' &&
      Number.isFinite(c)
    ) {
      dateIndex.push(di);
      slot.push(s);
      count.push(c);
    }
  }
  /* eslint-enable security/detect-object-injection */
  return { dateIndex, slot, count };
}

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

  // Restrict the resolution to the values the server can emit so the chart's slot math
  // (1440 / resolution) and hourly fold stay well-defined.
  const slotResolutionMinutes =
    typeof body.slotResolutionMinutes === 'number' &&
    VALID_SLOT_RESOLUTIONS.includes(body.slotResolutionMinutes)
      ? body.slotResolutionMinutes
      : DEFAULT_SLOT_RESOLUTION_MINUTES;

  return {
    dates: Array.isArray(body.dates)
      ? body.dates.filter((d): d is string => typeof d === 'string')
      : [],
    slotResolutionMinutes,
    cells: coerceCells(cells),
  };
}

// Top-N species the ridgeline requests; mirrors the chart's maxSpecies cap and the server default.
const SPECIES_RIDGELINE_LIMIT = 5;
const SPECIES_DISTRIBUTION_BUCKETS = 24;

interface SpeciesDistributionDatum {
  scientificName: string;
  density: number[];
  total: number;
}

/**
 * Who-sings-when ridgeline: the top-N species by detection volume in range, each with a normalized
 * 24-bucket hour-of-day distribution. Server-ranked and server-normalized; this defensively coerces
 * the array payload. Common names are resolved later (registry mapProps) from the hub's species map.
 */
async function fetchSpeciesDistribution(
  params: AnalyticsParams,
  signal?: AbortSignal
): Promise<SpeciesDistributionDatum[]> {
  const search = new URLSearchParams({
    start_date: formatDateForAPI(params.startDate),
    end_date: formatDateForAPI(params.endDate),
    limit: String(SPECIES_RIDGELINE_LIMIT),
  });

  const response = await fetch(
    buildAppUrl(`/api/v2/analytics/time/distribution/species?${search}`),
    { signal }
  );
  ensureOk(response);

  const data: unknown = await response.json();
  if (!Array.isArray(data)) {
    throw new Error('Invalid species distribution response: expected an array');
  }

  return data
    .map(raw => {
      if (!raw || typeof raw !== 'object') return null;
      const item = raw as { scientificName?: unknown; buckets?: unknown; total?: unknown };
      const scientificName = typeof item.scientificName === 'string' ? item.scientificName : '';
      if (!scientificName) return null;
      // Coerce every bucket to a finite number; pad/truncate defensively to 24 so the chart's
      // hour axis stays well-defined even on a malformed payload.
      const rawBuckets = Array.isArray(item.buckets) ? item.buckets : [];
      const density: number[] = [];
      for (let i = 0; i < SPECIES_DISTRIBUTION_BUCKETS; i++) {
        // eslint-disable-next-line security/detect-object-injection -- i is a bounded loop index
        const b = rawBuckets[i];
        density.push(typeof b === 'number' && Number.isFinite(b) ? b : 0);
      }
      const total = typeof item.total === 'number' && Number.isFinite(item.total) ? item.total : 0;
      return { scientificName, density, total };
    })
    .filter((d): d is SpeciesDistributionDatum => d !== null);
}

interface DawnOnsetResponseItem {
  date?: unknown;
  onsetRelMinutes?: unknown;
  detectionCount?: unknown;
}

/**
 * Dawn-chorus onset: per-day chorus onset relative to civil dawn. The server emits one entry per
 * calendar day in the range (negative = before civil dawn; null on days with too few detections or
 * no civil dawn). Honors an optional single-species filter (the control bar's first selection).
 * Defensively coerces the array, preserving nulls so the chart's trend line can break over gaps.
 */
async function fetchDawnOnset(
  params: AnalyticsParams,
  signal?: AbortSignal
): Promise<DawnOnsetData> {
  const search = new URLSearchParams({
    start_date: formatDateForAPI(params.startDate),
    end_date: formatDateForAPI(params.endDate),
  });
  // The endpoint filters by a single species; use the first selected (none = all species).
  if (params.species.length > 0) search.append('species', params.species[0]);

  const response = await fetch(buildAppUrl(`/api/v2/analytics/time/dawn-onset?${search}`), {
    signal,
  });
  ensureOk(response);

  const data: unknown = await response.json();
  if (!Array.isArray(data)) {
    throw new Error('Invalid dawn onset response: expected an array');
  }

  const points: DawnOnsetPoint[] = [];
  for (const raw of data) {
    if (!raw || typeof raw !== 'object') continue;
    const item = raw as DawnOnsetResponseItem;
    if (typeof item.date !== 'string') continue;
    // Keep null distinct from 0: a gap day must stay null so the trend line breaks rather than
    // dipping to civil dawn.
    const onsetRelMinutes =
      typeof item.onsetRelMinutes === 'number' && Number.isFinite(item.onsetRelMinutes)
        ? item.onsetRelMinutes
        : null;
    const detectionCount =
      typeof item.detectionCount === 'number' && Number.isFinite(item.detectionCount)
        ? item.detectionCount
        : 0;
    points.push({ date: item.date, onsetRelMinutes, detectionCount });
  }

  return { points };
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
    id: 'species-ridgeline',
    group: 'patterns',
    titleKey: 'analytics.advanced.charts.ridgeline.title',
    descKey: 'analytics.advanced.charts.ridgeline.description',
    emptyKey: 'analytics.advanced.charts.ridgeline.noData',
    emptyHintKey: 'analytics.advanced.charts.ridgeline.noDataHint',
    component: SpeciesRidgeline,
    fetch: fetchSpeciesDistribution,
    // The endpoint is always top-N by volume; supports.species lets the patterns tab's species
    // auto-select run, and the chart notes that it shows the top N regardless of selection.
    mapProps: (data, _params, ctx) => ({
      series: (data as SpeciesDistributionDatum[]).map(d => ({
        scientificName: d.scientificName,
        commonName: ctx.speciesNames.get(d.scientificName) ?? d.scientificName,
        density: d.density,
        total: d.total,
      })),
      noteKey: 'analytics.advanced.charts.ridgeline.note',
    }),
    size: 'full',
    supports: { species: true, source: false },
    // A ridgeline needs at least a couple of species to read as one; one lonely ridge is not useful.
    minDataPoints: 2,
    maxSpecies: SPECIES_RIDGELINE_LIMIT,
  },
  {
    id: 'dawn-chorus-onset',
    group: 'patterns',
    titleKey: 'analytics.advanced.charts.dawnOnset.title',
    descKey: 'analytics.advanced.charts.dawnOnset.description',
    emptyKey: 'analytics.advanced.charts.dawnOnset.noData',
    emptyHintKey: 'analytics.advanced.charts.dawnOnset.noDataHint',
    component: DawnChorusOnset,
    fetch: fetchDawnOnset,
    // The fetch result is an object with a points array (one per day, including nulls); count only
    // the days that have a measurable onset rather than the raw array length.
    countDataPoints: data => onsetCount(data as DawnOnsetData),
    size: 'full',
    supports: { species: true, source: false },
    // A handful of days with onsets is the minimum before the scatter + trend read as anything.
    minDataPoints: 3,
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
