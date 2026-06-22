import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/svelte';
import { tick } from 'svelte';

import ChartCard from './ChartCard.svelte';
import TrendChartOptions from './TrendChartOptions.svelte';
import StubChart from './__tests__/StubChart.svelte';
import { parseAnalyticsParams } from '../registry/analyticsParams';
import type { AnalyticsParams, ChartDef } from '../registry/types';

// The shared setup mocks `t` to echo the key, so assertions match i18n keys.

afterEach(() => cleanup());

const params: AnalyticsParams = parseAnalyticsParams('?range=week', {
  defaultTab: 'patterns',
  now: new Date(2026, 5, 19, 12, 0, 0),
});

function makeDef(overrides: Partial<ChartDef> = {}): ChartDef {
  return {
    id: 'stub-chart',
    group: 'patterns',
    titleKey: 'analytics.advanced.charts.timeOfDay.title',
    descKey: 'analytics.advanced.charts.timeOfDay.description',
    emptyKey: 'analytics.advanced.charts.timeOfDay.noData',
    emptyHintKey: 'analytics.advanced.charts.timeOfDay.noDataHint',
    component: StubChart,
    fetch: vi.fn().mockResolvedValue([]),
    size: 'full',
    supports: { species: true, source: false },
    ...overrides,
  };
}

describe('ChartCard state matrix', () => {
  it('shows the loading state before the fetch resolves', () => {
    // A never-resolving fetch keeps the card in the loading state.
    const def = makeDef({ fetch: vi.fn(() => new Promise<unknown>(() => {})) });
    render(ChartCard, { props: { chart: def, params } });

    expect(screen.getByLabelText('analytics.advanced.aria.loadingAnalytics')).toBeInTheDocument();
    expect(screen.queryByTestId('stub-chart')).not.toBeInTheDocument();
  });

  it('shows the empty state when the fetch returns no data', async () => {
    const def = makeDef({ fetch: vi.fn().mockResolvedValue([]) });
    render(ChartCard, { props: { chart: def, params } });

    expect(
      await screen.findByText('analytics.advanced.charts.timeOfDay.noData')
    ).toBeInTheDocument();
    expect(screen.queryByTestId('stub-chart')).not.toBeInTheDocument();
  });

  it('shows the distinct "not enough data yet" state below minDataPoints', async () => {
    const def = makeDef({
      minDataPoints: 5,
      fetch: vi.fn().mockResolvedValue([{ a: 1 }, { a: 2 }]), // 2 < 5
    });
    render(ChartCard, { props: { chart: def, params } });

    expect(await screen.findByText('analytics.hub.card.notEnoughData')).toBeInTheDocument();
    expect(screen.queryByTestId('stub-chart')).not.toBeInTheDocument();
    // Not the same as the generic empty state.
    expect(
      screen.queryByText('analytics.advanced.charts.timeOfDay.noData')
    ).not.toBeInTheDocument();
  });

  it('renders the chart when enough data is returned', async () => {
    const rows = [{ a: 1 }, { a: 2 }, { a: 3 }, { a: 4 }, { a: 5 }];
    const def = makeDef({ minDataPoints: 5, fetch: vi.fn().mockResolvedValue(rows) });
    render(ChartCard, { props: { chart: def, params } });

    const stub = await screen.findByTestId('stub-chart');
    expect(stub).toHaveAttribute('data-count', '5');
    expect(screen.queryByText('analytics.hub.card.notEnoughData')).not.toBeInTheDocument();
  });

  it('shows the error state and recovers via retry', async () => {
    const fetchMock = vi
      .fn()
      .mockRejectedValueOnce(new Error('boom'))
      .mockResolvedValueOnce([{ a: 1 }, { a: 2 }]);
    const def = makeDef({ fetch: fetchMock });
    render(ChartCard, { props: { chart: def, params } });

    expect(await screen.findByText('analytics.hub.card.error')).toBeInTheDocument();
    const retry = screen.getByText('analytics.hub.card.retry');

    await fireEvent.click(retry);

    expect(await screen.findByTestId('stub-chart')).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });
});

describe('ChartCard chrome', () => {
  it('renders the title and description from the registry', () => {
    render(ChartCard, { props: { chart: makeDef(), params } });
    expect(screen.getByText('analytics.advanced.charts.timeOfDay.title')).toBeInTheDocument();
    expect(screen.getByText('analytics.advanced.charts.timeOfDay.description')).toBeInTheDocument();
  });

  it('shows a disabled export stub only when the chart enables export', () => {
    const { unmount } = render(ChartCard, { props: { chart: makeDef(), params } });
    expect(screen.queryByText('analytics.hub.card.export')).not.toBeInTheDocument();
    unmount();

    render(ChartCard, { props: { chart: makeDef({ export: 'csv' }), params } });
    const exportBtn = screen.getByText('analytics.hub.card.export').closest('button');
    expect(exportBtn).toBeDisabled();
  });

  it('renders a per-card controls toolbar when the chart provides one', () => {
    const def = makeDef({
      controls: TrendChartOptions,
      defaultOptions: { showRelative: false, enableZoom: true, enableBrush: false },
    });
    render(ChartCard, { props: { chart: def, params } });
    expect(screen.getByText('analytics.advanced.options.relativeTrends')).toBeInTheDocument();
    expect(screen.getByText('analytics.advanced.options.zoomPan')).toBeInTheDocument();
  });
});

describe('ChartCard refetch gating', () => {
  const withSpecies = (species: string[]): AnalyticsParams => ({
    ...parseAnalyticsParams('?range=week', {
      defaultTab: 'patterns',
      now: new Date(2026, 5, 19, 12, 0, 0),
    }),
    species,
  });

  it('refetches when selected species change for a species-driven chart', async () => {
    const fetchMock = vi.fn().mockResolvedValue([{ a: 1 }]);
    const def = makeDef({ supports: { species: true, source: false }, fetch: fetchMock });
    const { rerender } = render(ChartCard, {
      props: { chart: def, params: withSpecies(['Turdus merula']) },
    });
    await tick();
    expect(fetchMock).toHaveBeenCalledTimes(1);

    await rerender({ chart: def, params: withSpecies(['Turdus merula', 'Parus major']) });
    await tick();
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it('does NOT refetch when species change for a chart that ignores species', async () => {
    const fetchMock = vi.fn().mockResolvedValue([{ a: 1 }]);
    const def = makeDef({ supports: { species: false, source: false }, fetch: fetchMock });
    const { rerender } = render(ChartCard, {
      props: { chart: def, params: withSpecies(['Turdus merula']) },
    });
    await tick();
    expect(fetchMock).toHaveBeenCalledTimes(1);

    await rerender({ chart: def, params: withSpecies(['Turdus merula', 'Parus major']) });
    await tick();
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });
});

describe('ChartCard brush -> onParamsChange chain', () => {
  it('routes a chart-initiated range change up through onParamsChange', async () => {
    const onParamsChange = vi.fn();
    const def = makeDef({
      fetch: vi.fn().mockResolvedValue([{ a: 1 }]),
      // Mirrors the trend chart's mapProps wiring of onDateRangeChange.
      mapProps: (data, _params, ctx) => ({
        data,
        trigger: () =>
          ctx.onParamsChange({ range: 'custom', start: '2026-01-01', end: '2026-01-31' }),
      }),
    });
    render(ChartCard, { props: { chart: def, params, onParamsChange } });

    await fireEvent.click(await screen.findByTestId('stub-trigger'));

    expect(onParamsChange).toHaveBeenCalledWith(
      expect.objectContaining({ range: 'custom', start: '2026-01-01', end: '2026-01-31' })
    );
  });
});

describe('ChartCard pending (species auto-select)', () => {
  it('holds the loading state instead of flashing empty while species load', async () => {
    const def = makeDef({
      supports: { species: true, source: false },
      fetch: vi.fn().mockResolvedValue([]),
    });
    // No species selected yet and the hub is still loading the species list.
    render(ChartCard, { props: { chart: def, params, speciesLoading: true } });
    await tick();

    expect(screen.getByLabelText('analytics.advanced.aria.loadingAnalytics')).toBeInTheDocument();
    expect(
      screen.queryByText('analytics.advanced.charts.timeOfDay.noData')
    ).not.toBeInTheDocument();
  });

  it('shows the empty state once species loading finishes with no data', async () => {
    const def = makeDef({
      supports: { species: true, source: false },
      fetch: vi.fn().mockResolvedValue([]),
    });
    render(ChartCard, { props: { chart: def, params, speciesLoading: false } });

    expect(
      await screen.findByText('analytics.advanced.charts.timeOfDay.noData')
    ).toBeInTheDocument();
  });
});
