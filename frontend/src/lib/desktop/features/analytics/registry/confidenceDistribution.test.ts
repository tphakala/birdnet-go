import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup } from '@testing-library/svelte';

import { CHART_REGISTRY } from './charts';
import type { AnalyticsParams, ChartPropsContext } from './types';
import SpeciesRidgeline from '../components/charts/d3/SpeciesRidgeline.svelte';
import type { RidgelineSeries } from '../components/charts/d3/utils/ridgeline';

// Verifies the confidence-distribution registry entry reuses the SpeciesRidgeline component with
// confidence-specific props: series mapping (common-name resolution), the bins x-tick formatter,
// empty/sparse handling, and the a11y summary. jsdom has no layout engine; assert on element
// counts/attributes only.

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  cleanup();
});

const def = CHART_REGISTRY.find(c => c.id === 'confidence-distribution');
// Guard at module scope so the rest of the file sees a defined def + mapProps without non-null
// assertions (forbidden by lint); a missing entry fails every test with a clear message.
if (!def?.mapProps) {
  throw new Error('confidence-distribution chart def with mapProps is required');
}
const mapProps = def.mapProps;

function makeCtx(names: [string, string][] = []): ChartPropsContext {
  return {
    options: {},
    onParamsChange: vi.fn(),
    speciesNames: new Map(names),
  };
}

// mapProps for this chart ignores params (always top-N); an empty object is enough.
const params = {} as AnalyticsParams;

/** A 20-length density with a single peak bin, mimicking the server's normalized confidence bins. */
function bins(peak: number): number[] {
  return Array.from({ length: 20 }, (_, i) => (i === peak ? 1 : 0));
}

const sample = [
  { scientificName: 'Turdus merula', density: bins(16), total: 40 },
  { scientificName: 'Erithacus rubecula', density: bins(10), total: 12 },
];

describe('confidence-distribution chart def', () => {
  it('is registered in the quality group reusing the ridgeline component', () => {
    expect(def.group).toBe('quality');
    expect(def.component).toBe(SpeciesRidgeline);
    // Always top-N by volume; never filters by species, so the (sole) quality tab shows no species selector.
    expect(def.supports.species).toBe(false);
    expect(def.supports.source).toBe(false);
    expect(def.maxSpecies).toBe(5);
    expect(def.minDataPoints).toBe(2);
  });

  it('maps confidence data to ridgeline series, resolving common names from the hub map', () => {
    const props = mapProps(sample, params, makeCtx([['Turdus merula', 'Eurasian Blackbird']]));
    const series = props.series as Array<{
      scientificName: string;
      commonName: string;
      density: number[];
      total: number;
    }>;
    expect(series).toHaveLength(2);
    expect(series[0].scientificName).toBe('Turdus merula');
    expect(series[0].commonName).toBe('Eurasian Blackbird');
    // No mapping entry -> falls back to the scientific name.
    expect(series[1].commonName).toBe('Erithacus rubecula');
    expect(series[0].density).toHaveLength(20);
    expect(series[0].total).toBe(40);
  });

  it('labels confidence bins as left-edge percentages (0/25/50/75% for 20 bins)', () => {
    const props = mapProps(sample, params, makeCtx());
    const fmt = props.xTickFormat as (_i: number) => string;
    expect(fmt(0)).toBe('0%');
    expect(fmt(5)).toBe('25%');
    expect(fmt(10)).toBe('50%');
    expect(fmt(15)).toBe('75%');
    expect(props.xTickStep).toBe(5);
  });

  it('keeps the formatter divisor safe on an empty result (bin-count fallback)', () => {
    const props = mapProps([], params, makeCtx());
    expect(props.series).toHaveLength(0);
    const fmt = props.xTickFormat as (_i: number) => string;
    // Falls back to the default 20 bins, so 5/20 = 25% rather than a divide-by-zero.
    expect(fmt(5)).toBe('25%');
  });

  it('wires this chart-specific i18n keys into the shared component', () => {
    const props = mapProps(sample, params, makeCtx());
    expect(props.ariaLabelKey).toBe('analytics.advanced.charts.confidence.ariaLabel');
    expect(props.axisLabelKey).toBe('analytics.advanced.charts.confidence.axisLabel');
    expect(props.summaryKey).toBe('analytics.advanced.charts.confidence.summary');
    expect(props.noteKey).toBe('analytics.advanced.charts.confidence.note');
    expect(props.totalLabelKey).toBe('analytics.advanced.charts.confidence.tooltipCount');
    expect(props.peakLabelKey).toBe('analytics.advanced.charts.confidence.tooltipPeak');
  });

  it('renders one ridge per species through the shared component, with an a11y summary', async () => {
    const series = mapProps(sample, params, makeCtx()).series as RidgelineSeries[];
    const { container } = render(SpeciesRidgeline, { props: { series, width: 800 } });
    await Promise.resolve();
    expect(container.querySelectorAll('path.ridge-area')).toHaveLength(2);
    expect(container.querySelector('[data-testid="ridgeline-summary"]')).toBeTruthy();
  });

  it('renders without throwing for an empty (sparse) result', () => {
    const series = mapProps([], params, makeCtx()).series as RidgelineSeries[];
    expect(() => render(SpeciesRidgeline, { props: { series } })).not.toThrow();
  });
});
