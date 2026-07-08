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
import AcousticSuccessionChart from '../components/charts/d3/AcousticSuccessionChart.svelte';
import DawnChorusOnset from '../components/charts/d3/DawnChorusOnset.svelte';
import { onsetCount } from '../components/charts/d3/utils/dawnOnset';
import type { DawnOnsetData, DawnOnsetPoint } from '../components/charts/d3/utils/dawnOnset';
import NocturnalClock from '../components/charts/d3/NocturnalClock.svelte';
import { hourlyTotal } from '../components/charts/d3/utils/nocturnal';
import type { NocturnalClockData, SunTimes } from '../components/charts/d3/utils/nocturnal';
import TrendChartOptions from '../components/TrendChartOptions.svelte';
import SpeciesAccumulationChart from '../components/charts/d3/SpeciesAccumulationChart.svelte';
import { finalCumulative } from '../components/charts/d3/utils/accumulation';
import type {
  AccumulationData,
  AccumulationPoint,
} from '../components/charts/d3/utils/accumulation';
import SpeciesPhenologyChart from '../components/charts/d3/SpeciesPhenologyChart.svelte';
import type {
  PhenologyData,
  PhenologyDatum,
  PhenologyRow,
} from '../components/charts/d3/utils/phenology';
import YearOverYearChart from '../components/charts/d3/YearOverYearChart.svelte';
import { peakCumulative } from '../components/charts/d3/utils/yearOverYear';
import type {
  YearOverYearData,
  YearOverYearPoint,
} from '../components/charts/d3/utils/yearOverYear';

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

// Top-N species the succession streamgraph requests; mirrors the chart's maxSpecies cap and the
// server default. Kept modest so the stacked wiggle bands stay legible within the card's fixed height.
const SUCCESSION_LIMIT = 6;
const SUCCESSION_BUCKETS = 24;

interface SuccessionDatum {
  scientificName: string;
  counts: number[];
  total: number;
}

/**
 * Acoustic succession streamgraph: the top-N species by detection volume in range, each with their
 * raw 24-bucket hour-of-day detection counts. Like the who-sings-when ridgeline (#1159) it always
 * requests the top-N and does not honor the species filter, so the chart shows the diel acoustic
 * handover among the dominant species rather than a single species; the server ranks. Defensively
 * coerces the array payload, padding/truncating counts to 24 so the chart's hour axis stays
 * well-defined even on a malformed payload.
 */
async function fetchAcousticSuccession(
  params: AnalyticsParams,
  signal?: AbortSignal
): Promise<SuccessionDatum[]> {
  const search = new URLSearchParams({
    start_date: formatDateForAPI(params.startDate),
    end_date: formatDateForAPI(params.endDate),
    limit: String(SUCCESSION_LIMIT),
  });

  const response = await fetch(buildAppUrl(`/api/v2/analytics/time/succession?${search}`), {
    signal,
  });
  ensureOk(response);

  const data: unknown = await response.json();
  if (!Array.isArray(data)) {
    throw new Error('Invalid acoustic succession response: expected an array');
  }

  return data
    .map(raw => {
      if (!raw || typeof raw !== 'object') return null;
      const item = raw as { scientificName?: unknown; counts?: unknown; total?: unknown };
      const scientificName = typeof item.scientificName === 'string' ? item.scientificName : '';
      if (!scientificName) return null;
      // Coerce every count to a finite, non-negative number; pad/truncate to 24 so the hour axis
      // stays well-defined (a negative count would invert a stacked band).
      const rawCounts = Array.isArray(item.counts) ? item.counts : [];
      const counts: number[] = [];
      for (let i = 0; i < SUCCESSION_BUCKETS; i++) {
        // eslint-disable-next-line security/detect-object-injection -- i is a bounded loop index
        const c = rawCounts[i];
        counts.push(typeof c === 'number' && Number.isFinite(c) ? Math.max(0, c) : 0);
      }
      const total = typeof item.total === 'number' && Number.isFinite(item.total) ? item.total : 0;
      return { scientificName, counts, total };
    })
    .filter((d): d is SuccessionDatum => d !== null);
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

const HOURS_IN_DAY = 24;
const MINUTES_IN_DAY = HOURS_IN_DAY * 60;

/** Coerces the hourly-distribution payload ([{hour,count}]) into a dense, bounds-checked number[24]. */
function coerceHourly(data: unknown): number[] {
  const hourly = new Array<number>(HOURS_IN_DAY).fill(0);
  if (!Array.isArray(data)) return hourly;
  for (const raw of data) {
    if (!raw || typeof raw !== 'object') continue;
    const item = raw as { hour?: unknown; count?: unknown };
    const hour = typeof item.hour === 'number' ? item.hour : -1;
    // Counts are detection tallies; clamp to >= 0 so a malformed negative never yields a negative
    // (inverted) bar height.
    const count =
      typeof item.count === 'number' && Number.isFinite(item.count) ? Math.max(0, item.count) : 0;
    if (Number.isInteger(hour) && hour >= 0 && hour < HOURS_IN_DAY) {
      // eslint-disable-next-line security/detect-object-injection -- hour is bounds-checked above
      hourly[hour] = count;
    }
  }
  return hourly;
}

/**
 * A minute-of-day value (0..1439) or null. Out-of-domain numbers are rejected (null) so a malformed
 * payload cannot place sun shading off the 24h canvas; the chart treats null as an undefined event.
 */
function coerceSunMinute(value: unknown): number | null {
  if (typeof value !== 'number' || !Number.isFinite(value)) return null;
  return value >= 0 && value < MINUTES_IN_DAY ? value : null;
}

/**
 * Nocturnal activity clock: hourly detection counts (reusing the unchanged hourly-distribution
 * endpoint) plus sun times for the day/night shading (from the separate /analytics/sun endpoint).
 * The two are fetched in parallel; a sun-fetch failure degrades to no shading rather than failing
 * the whole card. Honors an optional single-species filter on the counts only (sun is
 * species-independent). The sun endpoint receives the same range and picks the representative
 * midpoint date server-side.
 */
async function fetchNocturnal(
  params: AnalyticsParams,
  signal?: AbortSignal
): Promise<NocturnalClockData> {
  const start = formatDateForAPI(params.startDate);
  const end = formatDateForAPI(params.endDate);

  const hourlySearch = new URLSearchParams({ start_date: start, end_date: end });
  // The endpoint filters by a single species; use the first selected (none = all species).
  if (params.species.length > 0) hourlySearch.append('species', params.species[0]);

  const hourlyPromise = (async (): Promise<number[]> => {
    const response = await fetch(
      buildAppUrl(`/api/v2/analytics/time/distribution/hourly?${hourlySearch}`),
      { signal }
    );
    ensureOk(response);
    return coerceHourly(await response.json());
  })();

  const sunSearch = new URLSearchParams({ start_date: start, end_date: end });
  const sunPromise: Promise<SunTimes | null> = (async (): Promise<SunTimes | null> => {
    const response = await fetch(buildAppUrl(`/api/v2/analytics/sun?${sunSearch}`), { signal });
    ensureOk(response);
    const raw: unknown = await response.json();
    if (!raw || typeof raw !== 'object' || Array.isArray(raw)) return null;
    const body = raw as {
      date?: unknown;
      sunrise?: unknown;
      sunset?: unknown;
      civilDawn?: unknown;
      civilDusk?: unknown;
      available?: unknown;
    };
    return {
      date: typeof body.date === 'string' ? body.date : '',
      sunrise: coerceSunMinute(body.sunrise),
      sunset: coerceSunMinute(body.sunset),
      civilDawn: coerceSunMinute(body.civilDawn),
      civilDusk: coerceSunMinute(body.civilDusk),
      available: body.available === true,
    };
  })().catch((err: unknown) => {
    // Propagate cancellation so an aborted request rejects (the card ignores aborts) rather than
    // resolving with partial data; only a genuine sun failure degrades to no shading.
    if (err instanceof Error && err.name === 'AbortError') throw err;
    return null;
  });

  const [hourly, sun] = await Promise.all([hourlyPromise, sunPromise]);
  return { hourly, sun };
}

// Top-N species the confidence histogram requests; mirrors the chart's maxSpecies cap. BINS matches
// the server default; MAX_CONFIDENCE_BINS bounds the coerced density array defensively (the server
// clamps bins to 50).
const CONFIDENCE_DISTRIBUTION_LIMIT = 5;
const CONFIDENCE_DISTRIBUTION_BINS = 20;
const MAX_CONFIDENCE_BINS = 50;

interface ConfidenceDistributionDatum {
  scientificName: string;
  density: number[];
  total: number;
}

/**
 * Confidence distribution per species: the top-N species by detection volume, each with a normalized
 * histogram of detection confidence scores (bins over 0..1). Like the who-sings-when ridgeline
 * (#1159) it always requests the top-N and does not honor the species filter, so the chart compares
 * several species' confidence shapes rather than collapsing to a single ridge; the server ranks and
 * normalizes. Defensively coerces the array payload, keeping the server's (variable) bin count.
 */
async function fetchConfidenceDistribution(
  params: AnalyticsParams,
  signal?: AbortSignal
): Promise<ConfidenceDistributionDatum[]> {
  const search = new URLSearchParams({
    start_date: formatDateForAPI(params.startDate),
    end_date: formatDateForAPI(params.endDate),
    limit: String(CONFIDENCE_DISTRIBUTION_LIMIT),
    bins: String(CONFIDENCE_DISTRIBUTION_BINS),
  });

  const response = await fetch(buildAppUrl(`/api/v2/analytics/confidence/distribution?${search}`), {
    signal,
  });
  ensureOk(response);

  const data: unknown = await response.json();
  if (!Array.isArray(data)) {
    throw new Error('Invalid confidence distribution response: expected an array');
  }

  return data
    .map(raw => {
      if (!raw || typeof raw !== 'object') return null;
      const item = raw as { scientificName?: unknown; bins?: unknown; total?: unknown };
      const scientificName = typeof item.scientificName === 'string' ? item.scientificName : '';
      if (!scientificName) return null;
      // Coerce every bin to a finite number; keep the server's bin count (variable, unlike the
      // hourly ridgeline's fixed 24) but cap the length defensively so a malformed payload cannot
      // allocate an unreasonable density array.
      const rawBins = Array.isArray(item.bins) ? item.bins : [];
      const density = rawBins
        .slice(0, MAX_CONFIDENCE_BINS)
        .map(b => (typeof b === 'number' && Number.isFinite(b) ? b : 0));
      const total = typeof item.total === 'number' && Number.isFinite(item.total) ? item.total : 0;
      return { scientificName, density, total };
    })
    .filter((d): d is ConfidenceDistributionDatum => d !== null);
}

interface AccumulationResponseItem {
  date?: unknown;
  cumulativeSpecies?: unknown;
  newSpecies?: unknown;
}

/**
 * Species accumulation curve: per calendar day, the cumulative count of distinct species first seen
 * within the range (false positives excluded; "first seen" is windowed, not lifetime). The server
 * emits one entry per day (a monotonic non-decreasing series). All-species, so the species filter is
 * never sent. Defensively coerces the array payload into the chart's points shape.
 */
async function fetchSpeciesAccumulation(
  params: AnalyticsParams,
  signal?: AbortSignal
): Promise<AccumulationData> {
  const search = new URLSearchParams({
    start_date: formatDateForAPI(params.startDate),
    end_date: formatDateForAPI(params.endDate),
  });

  const response = await fetch(buildAppUrl(`/api/v2/analytics/species/accumulation?${search}`), {
    signal,
  });
  ensureOk(response);

  const data: unknown = await response.json();
  if (!Array.isArray(data)) {
    throw new Error('Invalid species accumulation response: expected an array');
  }

  const points: AccumulationPoint[] = [];
  for (const raw of data) {
    if (!raw || typeof raw !== 'object') continue;
    const item = raw as AccumulationResponseItem;
    if (typeof item.date !== 'string') continue;
    const cumulativeSpecies =
      typeof item.cumulativeSpecies === 'number' && Number.isFinite(item.cumulativeSpecies)
        ? item.cumulativeSpecies
        : 0;
    const newSpecies =
      typeof item.newSpecies === 'number' && Number.isFinite(item.newSpecies) ? item.newSpecies : 0;
    points.push({ date: item.date, cumulativeSpecies, newSpecies });
  }

  return { points };
}

interface YearOverYearResponseItem {
  date?: unknown;
  monthDay?: unknown;
  thisYear?: unknown;
  lastYear?: unknown;
  delta?: unknown;
}

interface YearOverYearResponseShape {
  currentYear?: unknown;
  previousYear?: unknown;
  points?: unknown;
}

/**
 * Year-over-year tracker: current year-to-date cumulative detections versus the same calendar span one
 * year earlier (false positives excluded). The server returns an object with year labels and a points
 * array (one entry per current-year day). All-species, so the species filter is never sent; the
 * control bar's end date is the as-of date. Defensively coerces the payload into the chart's shape.
 */
async function fetchYearOverYear(
  params: AnalyticsParams,
  signal?: AbortSignal
): Promise<YearOverYearData> {
  const search = new URLSearchParams({
    date: formatDateForAPI(params.endDate),
  });

  const response = await fetch(buildAppUrl(`/api/v2/analytics/time/year-over-year?${search}`), {
    signal,
  });
  ensureOk(response);

  const raw: unknown = await response.json();
  if (!raw || typeof raw !== 'object') {
    throw new Error('Invalid year-over-year response: expected an object');
  }
  const body = raw as YearOverYearResponseShape;
  const currentYear =
    typeof body.currentYear === 'number' && Number.isFinite(body.currentYear)
      ? body.currentYear
      : 0;
  const previousYear =
    typeof body.previousYear === 'number' && Number.isFinite(body.previousYear)
      ? body.previousYear
      : 0;
  const rawPoints = Array.isArray(body.points) ? body.points : [];

  const points: YearOverYearPoint[] = [];
  for (const r of rawPoints) {
    if (!r || typeof r !== 'object') continue;
    const item = r as YearOverYearResponseItem;
    if (typeof item.date !== 'string') continue;
    const monthDay = typeof item.monthDay === 'string' ? item.monthDay : '';
    const thisYear =
      typeof item.thisYear === 'number' && Number.isFinite(item.thisYear) ? item.thisYear : 0;
    const lastYear =
      typeof item.lastYear === 'number' && Number.isFinite(item.lastYear) ? item.lastYear : 0;
    const delta =
      typeof item.delta === 'number' && Number.isFinite(item.delta)
        ? item.delta
        : thisYear - lastYear;
    points.push({ date: item.date, monthDay, thisYear, lastYear, delta });
  }

  return { currentYear, previousYear, points };
}

// Top-N species the phenology Gantt requests; mirrors the chart's maxSpecies cap and the server
// default. Kept modest so the residency bars stay legible within the card's fixed height.
const SPECIES_PHENOLOGY_LIMIT = 12;

interface PhenologyResponseItem {
  scientificName?: unknown;
  firstSeen?: unknown;
  lastSeen?: unknown;
  count?: unknown;
}

/**
 * Arrival/departure phenology: the top-N species by detection volume in range, each with its first
 * and last in-range detection date (station-local YYYY-MM-DD) and detection count. Server-ranked and
 * server-sorted by arrival; this defensively coerces the array payload, dropping rows missing either
 * date. Common names are resolved later (registry mapProps) from the hub's species map.
 */
async function fetchSpeciesPhenology(
  params: AnalyticsParams,
  signal?: AbortSignal
): Promise<PhenologyDatum[]> {
  const search = new URLSearchParams({
    start_date: formatDateForAPI(params.startDate),
    end_date: formatDateForAPI(params.endDate),
    limit: String(SPECIES_PHENOLOGY_LIMIT),
  });

  const response = await fetch(buildAppUrl(`/api/v2/analytics/species/phenology?${search}`), {
    signal,
  });
  ensureOk(response);

  const data: unknown = await response.json();
  if (!Array.isArray(data)) {
    throw new Error('Invalid species phenology response: expected an array');
  }

  return data
    .map(raw => {
      if (!raw || typeof raw !== 'object') return null;
      const item = raw as PhenologyResponseItem;
      const scientificName = typeof item.scientificName === 'string' ? item.scientificName : '';
      const firstSeen = typeof item.firstSeen === 'string' ? item.firstSeen : '';
      const lastSeen = typeof item.lastSeen === 'string' ? item.lastSeen : '';
      // A residency bar needs both endpoints; a row missing either date is unrenderable, so drop it.
      if (!scientificName || !firstSeen || !lastSeen) return null;
      const count = typeof item.count === 'number' && Number.isFinite(item.count) ? item.count : 0;
      return { scientificName, firstSeen, lastSeen, count };
    })
    .filter((d): d is PhenologyDatum => d !== null);
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
    id: 'acoustic-succession',
    group: 'patterns',
    titleKey: 'analytics.advanced.charts.succession.title',
    descKey: 'analytics.advanced.charts.succession.description',
    emptyKey: 'analytics.advanced.charts.succession.noData',
    emptyHintKey: 'analytics.advanced.charts.succession.noDataHint',
    component: AcousticSuccessionChart,
    fetch: fetchAcousticSuccession,
    // The endpoint is always top-N by volume; like the sibling ridgeline, supports.species lets the
    // patterns tab's species auto-select run, and the chart's note states it shows the top N
    // regardless of selection. The fetch result is the raw row array, so the default array-length
    // count (the band count) drives the not-enough-data gate.
    mapProps: (data, _params, ctx) => ({
      series: (data as SuccessionDatum[]).map(d => ({
        scientificName: d.scientificName,
        commonName: ctx.speciesNames.get(d.scientificName) ?? d.scientificName,
        counts: d.counts,
        total: d.total,
      })),
      noteKey: 'analytics.advanced.charts.succession.note',
    }),
    size: 'full',
    supports: { species: true, source: false },
    // A streamgraph needs at least a few bands to weave into a visible handover; one or two bands is
    // just a worm, not a succession.
    minDataPoints: 3,
    maxSpecies: SUCCESSION_LIMIT,
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
    id: 'nocturnal-clock',
    group: 'nocturnal',
    titleKey: 'analytics.advanced.charts.nocturnal.title',
    descKey: 'analytics.advanced.charts.nocturnal.description',
    emptyKey: 'analytics.advanced.charts.nocturnal.noData',
    emptyHintKey: 'analytics.advanced.charts.nocturnal.noDataHint',
    component: NocturnalClock,
    fetch: fetchNocturnal,
    // The fetch result is an object (hourly counts + sun times), so count total detections across
    // the day rather than relying on ChartCard's default array-length count.
    countDataPoints: data => hourlyTotal(data as NocturnalClockData),
    size: 'full',
    supports: { species: true, source: false },
    // Below this many detections the 24h dial is too sparse to read as an activity pattern.
    minDataPoints: 10,
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
    id: 'year-over-year',
    group: 'trends',
    titleKey: 'analytics.advanced.charts.yearOverYear.title',
    descKey: 'analytics.advanced.charts.yearOverYear.description',
    emptyKey: 'analytics.advanced.charts.yearOverYear.noData',
    emptyHintKey: 'analytics.advanced.charts.yearOverYear.noDataHint',
    component: YearOverYearChart,
    fetch: fetchYearOverYear,
    // Object payload (year labels + a points array spanning the whole year-to-date), so the default
    // array-length count would always look like plenty of data. The meaningful count is how many
    // detections actually accumulated in either year; below 2 the chart is a trivial flat step, so
    // ChartCard shows the not-enough-data state. All-species, so neither filter applies.
    countDataPoints: data => peakCumulative(data as YearOverYearData),
    size: 'full',
    supports: { species: false, source: false },
    minDataPoints: 2,
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
  {
    id: 'species-accumulation',
    group: 'biodiversity',
    titleKey: 'analytics.advanced.charts.accumulation.title',
    descKey: 'analytics.advanced.charts.accumulation.description',
    emptyKey: 'analytics.advanced.charts.accumulation.noData',
    emptyHintKey: 'analytics.advanced.charts.accumulation.noDataHint',
    component: SpeciesAccumulationChart,
    fetch: fetchSpeciesAccumulation,
    // The fetch result is an object with a points array (one per day across the whole range), so the
    // default array-length count would always look like plenty of data. The meaningful count is how
    // many distinct species actually accumulated; below 2 the curve is a trivial flat step, so
    // ChartCard shows the not-enough-data state. The metric is all-species, so supports.species is
    // false (the sibling diversity chart in this tab is also species:false, so no dead selector).
    countDataPoints: data => finalCumulative(data as AccumulationData),
    size: 'full',
    supports: { species: false, source: false },
    minDataPoints: 2,
  },
  {
    id: 'species-phenology',
    group: 'biodiversity',
    titleKey: 'analytics.advanced.charts.phenology.title',
    descKey: 'analytics.advanced.charts.phenology.description',
    emptyKey: 'analytics.advanced.charts.phenology.noData',
    emptyHintKey: 'analytics.advanced.charts.phenology.noDataHint',
    component: SpeciesPhenologyChart,
    fetch: fetchSpeciesPhenology,
    // The endpoint is always top-N by volume and never filters by species, so supports.species is
    // false; the biodiversity tab's siblings (diversity, accumulation) are also species:false, so no
    // dead selector is shown. The fetch result is the raw row array, so the default array-length count
    // (the species count) drives the not-enough-data gate; a one-bar Gantt is not a comparison.
    mapProps: (data, _params, ctx) => ({
      data: {
        rows: (data as PhenologyDatum[]).map((d): PhenologyRow => ({
          ...d,
          commonName: ctx.speciesNames.get(d.scientificName) ?? d.scientificName,
        })),
      } as PhenologyData,
    }),
    size: 'full',
    supports: { species: false, source: false },
    minDataPoints: 2,
    maxSpecies: SPECIES_PHENOLOGY_LIMIT,
  },
  {
    id: 'confidence-distribution',
    group: 'quality',
    titleKey: 'analytics.advanced.charts.confidence.title',
    descKey: 'analytics.advanced.charts.confidence.description',
    emptyKey: 'analytics.advanced.charts.confidence.noData',
    emptyHintKey: 'analytics.advanced.charts.confidence.noDataHint',
    component: SpeciesRidgeline,
    fetch: fetchConfidenceDistribution,
    // Reuses the ridgeline: each species' confidence histogram becomes a density row, with a
    // confidence-bin x-tick formatter (label each bin's left-edge confidence as a percentage) and
    // this chart's own i18n keys. The endpoint is always top-N by detection volume and never filters
    // by species, so supports.species is false: this is the only chart in the quality tab, so a
    // species selector there would be an inert control (the note states the chart shows the top N).
    mapProps: (data, _params, ctx) => {
      const rows = data as ConfidenceDistributionDatum[];
      // All species share the server's bin count; fall back to the requested default for an empty
      // result so the formatter's divisor is never zero.
      const firstLen = rows[0]?.density.length ?? 0;
      const binCount = firstLen > 0 ? firstLen : CONFIDENCE_DISTRIBUTION_BINS;
      return {
        series: rows.map(d => ({
          scientificName: d.scientificName,
          commonName: ctx.speciesNames.get(d.scientificName) ?? d.scientificName,
          density: d.density,
          total: d.total,
        })),
        // Label bin index i by the confidence at its left edge (i / binCount) as a percentage, so a
        // 20-bin histogram reads 0% / 25% / 50% / 75% at step binCount/4.
        xTickFormat: (index: number) => `${Math.round((index / binCount) * 100)}%`,
        xTickStep: Math.max(1, Math.round(binCount / 4)),
        ariaLabelKey: 'analytics.advanced.charts.confidence.ariaLabel',
        axisLabelKey: 'analytics.advanced.charts.confidence.axisLabel',
        summaryKey: 'analytics.advanced.charts.confidence.summary',
        noteKey: 'analytics.advanced.charts.confidence.note',
        totalLabelKey: 'analytics.advanced.charts.confidence.tooltipCount',
        peakLabelKey: 'analytics.advanced.charts.confidence.tooltipPeak',
      };
    },
    size: 'full',
    supports: { species: false, source: false },
    // A ridgeline needs at least a couple of species to read as a comparison; one lonely ridge does
    // not tell the user which species are shakier than others.
    minDataPoints: 2,
    maxSpecies: CONFIDENCE_DISTRIBUTION_LIMIT,
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
