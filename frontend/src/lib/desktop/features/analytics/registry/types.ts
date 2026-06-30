/**
 * Analytics chart registry types.
 *
 * The registry is the single source of truth for which charts render in which
 * tab of the analytics hub. Each `ChartDef` describes one chart: how to fetch
 * its data, which control-bar filters it honors, how to map the fetched data
 * onto the concrete D3 chart component, and the chrome metadata `ChartCard`
 * needs (title, description, sizing, "not enough data yet" threshold).
 *
 * Adding a statistic is one `ChartDef` plus a chart component plus (later) an
 * endpoint. See `charts.ts` for the registered entries.
 */
import type { Component } from 'svelte';

/**
 * A chart component held in the registry. The registry is heterogeneous (each
 * chart has its own prop shape) and maps data -> props at runtime via
 * `ChartDef.mapProps`, so the component type is intentionally permissive; the
 * concrete prop types are enforced where each chart is authored, not here.
 */
// eslint-disable-next-line @typescript-eslint/no-explicit-any -- heterogeneous registry; props validated per-chart, not at the registry boundary
export type AnyChartComponent = Component<any>;

/** Tab groups shown in the hub, in display order. */
export type ChartGroup =
  'overview' | 'patterns' | 'trends' | 'biodiversity' | 'quality' | 'nocturnal';

/** Date-range presets shared by the control bar and the registry fetchers. */
export type DateRangePreset = 'week' | 'month' | 'quarter' | 'year' | 'custom';

/**
 * Resolved analytics parameters shared across the control bar, the registry
 * fetchers, and the chart components.
 *
 * `start`/`end` are the canonical URL-serialised form (YYYY-MM-DD, station-local
 * time). `startDate`/`endDate` are the parsed `Date` objects the D3 charts
 * consume; they are derived from `range`/`start`/`end` so callers never have to
 * re-parse.
 */
export interface AnalyticsParams {
  /** Selected date-range preset. */
  range: DateRangePreset;
  /** Effective range start (YYYY-MM-DD, local). */
  start: string;
  /** Effective range end (YYYY-MM-DD, local). */
  end: string;
  /** Selected species, by scientific name. Empty means none selected. */
  species: string[];
  /** Selected audio source/mic id. Empty means all sources. */
  source: string;
  /** Parsed range start (derived from range/start). */
  startDate: Date;
  /** Parsed range end (derived from range/end). */
  endDate: Date;
}

/** Which shared control-bar filters a chart honors. */
export interface ChartSupports {
  species: boolean;
  source: boolean;
}

/**
 * One audio source option for the control bar's source/mic filter, as returned by
 * `GET /api/v2/analytics/sources`. `id` is the opaque, stable source identifier the filter writes to
 * `AnalyticsParams.source` (and the URL); `name` is the display label (already anonymized server-side
 * for unauthenticated clients); `count` is the source's in-range detection volume.
 */
export interface AudioSourceOption {
  id: string;
  name: string;
  count: number;
}

/** Relative width a card occupies in the responsive grid. */
export type ChartSize = 'normal' | 'wide' | 'full';

/**
 * Context handed to a `ChartDef.mapProps` so a chart can wire fetched data,
 * per-card options, and a way to request shared-param changes (e.g. a brush
 * gesture rewriting the date range) onto its component props.
 */
export interface ChartPropsContext {
  /** Per-card display options owned by ChartCard (seeded from `defaultOptions`). */
  options: Record<string, unknown>;
  /** Request a change to the shared params (writes URL state in the hub). */
  onParamsChange: (_partial: Partial<AnalyticsParams>) => void;
  /**
   * Scientific name -> server-provided common name, sourced from the species
   * summary endpoint the hub already fetches. Lets charts label series without
   * an extra request (the per-visitor dictionary, when enabled, still wins via
   * `localizeSpeciesName`).
   */
  speciesNames: Map<string, string>;
}

/**
 * Per-card display options toolbar. Rendered by ChartCard in the card header
 * for charts that declare `controls`. Receives the current `options` and a
 * `setOption` callback; it must not mutate `options` directly.
 */
export interface ChartControlsProps {
  options: Record<string, unknown>;
  setOption: (_key: string, _value: unknown) => void;
}

export interface ChartDef {
  /** Stable id; also the per-card DOM anchor. */
  id: string;
  /** Tab this chart belongs to. */
  group: ChartGroup;
  /** i18n key for the card title. */
  titleKey: string;
  /** i18n key for the card description. */
  descKey: string;
  /** i18n key for the empty-state title (no data for the current params). */
  emptyKey: string;
  /** i18n key for the empty-state hint. */
  emptyHintKey: string;
  /** The D3 chart component (wraps BaseChart). */
  component: AnyChartComponent;
  /**
   * Optional per-card display-options toolbar, rendered in the card header.
   * Keeps chart-specific controls out of the shared control bar.
   */
  controls?: AnyChartComponent;
  /** Seed values for ChartCard's per-card options state. */
  defaultOptions?: Record<string, unknown>;
  /**
   * Fetches the chart's data for the given params. Implementations should
   * honor the `AbortSignal` so stale in-flight requests are cancelled.
   */
  fetch: (_params: AnalyticsParams, _signal?: AbortSignal) => Promise<unknown>;
  /**
   * Maps fetched data + params (+ options/callbacks) onto the chart component's
   * props. Defaults to `{ data }` when omitted.
   */
  mapProps?: (
    _data: unknown,
    _params: AnalyticsParams,
    _ctx: ChartPropsContext
  ) => Record<string, unknown>;
  /** Relative size in the responsive grid. */
  size: ChartSize;
  /** Which shared filters this chart honors. */
  supports: ChartSupports;
  /** Below this many data points, ChartCard shows "not enough data yet". */
  minDataPoints?: number;
  /**
   * Counts data points in a fetch result for the empty / not-enough-data
   * checks. Defaults to array length (or 0 for non-arrays).
   */
  countDataPoints?: (_data: unknown) => number;
  /** Enables CSV export (stubbed in PR0). */
  export?: 'csv';
  /** Caps the number of species rendered (e.g. ridgeline top-N). */
  maxSpecies?: number;
}
