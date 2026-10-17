/**
 * FilterResponseGraph plots the curve the backend computes. These tests cover
 * the request it sends and the states it shows around the canvas (loading,
 * updating, sample rate caption, skipped filters, error with Retry); the
 * canvas pixels themselves are not checked.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { tick } from 'svelte';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { t } from '$lib/i18n';
import { isPlainObject } from '$lib/utils/security';
import { renderTyped } from '../../../../../test/render-helpers';
import FilterResponseGraph from './FilterResponseGraph.svelte';
import { EQUALIZER_RESPONSE_DEBOUNCE_MS, EQUALIZER_RESPONSE_URL } from '$lib/api/equalizer';
import type { EqualizerResponseRequest } from '$lib/api/equalizer';

type ApiPost = (
  url: string,
  data?: unknown,
  options?: { signal?: AbortSignal }
) => Promise<unknown>;

const { apiPost, realPost } = vi.hoisted(() => {
  const holder: { current?: ApiPost } = {};
  return { apiPost: vi.fn<ApiPost>(), realPost: holder };
});

vi.mock('$lib/utils/api', async importOriginal => {
  const actual = await importOriginal<typeof import('$lib/utils/api')>();
  realPost.current = (url, data, options) => actual.api.post(url, data, options);
  return { ...actual, api: { ...actual.api, post: apiPost } };
});

const LOW_PASS = { id: 'f1', type: 'LowPass' as const, frequency: 15000, q: 0.707, passes: 1 };
const HIGH_PASS = { id: 'f2', type: 'HighPass' as const, frequency: 100, q: 0.707, passes: 1 };

function responseBody(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    sampleRate: 48000,
    enabled: true,
    points: [
      { hz: 20, gainDb: -12 },
      { hz: 1000, gainDb: 0 },
      { hz: 20000, gainDb: -6 },
    ],
    skipped: [],
    ...overrides,
  };
}

/** Run the debounce and let the request's promise and Svelte's updates settle. */
async function settle(ms = EQUALIZER_RESPONSE_DEBOUNCE_MS): Promise<void> {
  await vi.advanceTimersByTimeAsync(ms);
  await tick();
}

function isRequestBody(value: unknown): value is EqualizerResponseRequest {
  return (
    isPlainObject(value) &&
    isPlainObject(value.equalizer) &&
    typeof value.equalizer.enabled === 'boolean' &&
    Array.isArray(value.equalizer.filters)
  );
}

function lastBody(): EqualizerResponseRequest | undefined {
  const body = apiPost.mock.calls.at(-1)?.[1];
  return isRequestBody(body) ? body : undefined;
}

/** A 2d context with every method drawGraph uses, so the curve is drawn. */
function createContextMock() {
  return {
    clearRect: vi.fn(),
    fillRect: vi.fn(),
    beginPath: vi.fn(),
    moveTo: vi.fn(),
    lineTo: vi.fn(),
    stroke: vi.fn(),
    rect: vi.fn(),
    fillText: vi.fn(),
    save: vi.fn(),
    restore: vi.fn(),
    translate: vi.fn(),
    rotate: vi.fn(),
    setLineDash: vi.fn(),
    createLinearGradient: vi.fn(() => ({ addColorStop: vi.fn() })),
  };
}

/** Grid and reference lines drawn with lineTo on every draw, curve excluded. */
const GRID_LINE_COUNT = 16 + 6 + 1;

/** lineTo calls made by the most recent draw (after its clearRect). */
function lineToInLastDraw(ctx: ReturnType<typeof createContextMock>): number {
  const lastClear = Math.max(0, ...ctx.clearRect.mock.invocationCallOrder);
  return ctx.lineTo.mock.invocationCallOrder.filter(order => order > lastClear).length;
}

/** Strokes made by the most recent draw: the plot border, the grid lines and the reference line, plus one for a curve. */
const GRID_STROKE_COUNT = 1 + 16 + 6 + 1;

/** Calls of one context method made by the most recent draw, with their arguments. */
function callsInLastDraw(
  ctx: ReturnType<typeof createContextMock>,
  method: 'moveTo' | 'lineTo' | 'stroke'
): unknown[][] {
  const lastClear = Math.max(0, ...ctx.clearRect.mock.invocationCallOrder);
  const mock =
    method === 'moveTo' ? ctx.moveTo.mock : method === 'lineTo' ? ctx.lineTo.mock : ctx.stroke.mock;
  return mock.calls.filter((_call, i) => (mock.invocationCallOrder.at(i) ?? 0) > lastClear);
}

/** fillText labels drawn by the most recent draw. */
function labelsInLastDraw(ctx: ReturnType<typeof createContextMock>): unknown[] {
  const lastClear = Math.max(0, ...ctx.clearRect.mock.invocationCallOrder);
  return ctx.fillText.mock.calls
    .filter((_call, i) => (ctx.fillText.mock.invocationCallOrder.at(i) ?? 0) > lastClear)
    .map(call => call[0]);
}

const FLAT_LABEL = 'settings.audio.audioFilters.graph.flatResponse';

describe('FilterResponseGraph', () => {
  const originalGetContext = vi
    .mocked(HTMLCanvasElement.prototype.getContext)
    .getMockImplementation();

  beforeEach(() => {
    vi.useFakeTimers();
    apiPost.mockReset();
    vi.mocked(t).mockClear();
  });

  afterEach(() => {
    cleanup();
    apiPost.mockReset();
    vi.useRealTimers();
    vi.mocked(HTMLCanvasElement.prototype.getContext).mockReset();
    if (originalGetContext) {
      vi.mocked(HTMLCanvasElement.prototype.getContext).mockImplementation(originalGetContext);
    }
  });

  /** Make every canvas return one full context mock, and return it. */
  function useFullCanvas(): ReturnType<typeof createContextMock> {
    const ctx = createContextMock();
    // A partial CanvasRenderingContext2D: drawGraph only calls the methods above
    vi.mocked(HTMLCanvasElement.prototype.getContext).mockImplementation(
      () => ctx as unknown as CanvasRenderingContext2D
    );
    return ctx;
  }

  it('shows the loading text before the first response', async () => {
    apiPost.mockImplementation(() => new Promise(() => {}));
    renderTyped(FilterResponseGraph, {
      props: { equalizer: { enabled: true, filters: [LOW_PASS] } },
    });
    expect(screen.getByTestId('eq-graph-loading')).toHaveTextContent(
      'settings.audio.audioFilters.graph.loading'
    );
    // The live region is inserted empty and gets its text in a later task,
    // so screen readers announce the change.
    const region = screen.getByRole('status');
    expect(region.textContent).toBe('');
    await settle(0);
    expect(region).toHaveTextContent('settings.audio.audioFilters.graph.loading');
  });

  it('shows the sample rate caption from the response', async () => {
    apiPost.mockResolvedValue(responseBody({ sampleRate: 96000 }));
    renderTyped(FilterResponseGraph, {
      props: { equalizer: { enabled: true, filters: [LOW_PASS] }, sampleRate: 96000 },
    });
    await settle();
    expect(screen.getByTestId('eq-graph-sample-rate')).toBeInTheDocument();
    expect(t).toHaveBeenCalledWith('settings.audio.audioFilters.graph.sampleRate', {
      rate: 96000,
    });
    expect(screen.queryByTestId('eq-graph-loading')).not.toBeInTheDocument();
  });

  it('strokes the curve through the returned points', async () => {
    const ctx = useFullCanvas();
    apiPost.mockResolvedValue(responseBody());
    renderTyped(FilterResponseGraph, {
      props: { equalizer: { enabled: true, filters: [LOW_PASS] } },
    });
    expect(lineToInLastDraw(ctx)).toBe(GRID_LINE_COUNT);
    await settle();
    // Three points: one moveTo and two lineTo segments on top of the grid,
    // painted by one extra stroke.
    expect(lineToInLastDraw(ctx)).toBe(GRID_LINE_COUNT + 2);
    expect(callsInLastDraw(ctx, 'stroke')).toHaveLength(GRID_STROKE_COUNT + 1);
    // The canvas is 600 px wide in jsdom: the plot spans x 100..540 and
    // y 40..300, and -96..12 dB maps onto that height. The curve starts at the
    // first point (20 Hz, -12 dB) and ends at the last one (20 kHz, -6 dB).
    const start = callsInLastDraw(ctx, 'moveTo').at(-1);
    const end = callsInLastDraw(ctx, 'lineTo').at(-1);
    expect(start?.[0]).toBeCloseTo(100);
    expect(start?.[1]).toBeCloseTo(300 - (84 / 108) * 260);
    expect(end?.[0]).toBeCloseTo(540);
    expect(end?.[1]).toBeCloseTo(300 - (90 / 108) * 260);
    expect(labelsInLastDraw(ctx)).not.toContain(FLAT_LABEL);
  });

  it('keeps the previous curve and shows the updating text while a new request is pending', async () => {
    const ctx = useFullCanvas();
    apiPost.mockResolvedValueOnce(responseBody());
    const view = render(FilterResponseGraph, {
      props: { equalizer: { enabled: true, filters: [LOW_PASS] } },
    });
    await settle();
    expect(screen.getByTestId('eq-graph-sample-rate')).toBeInTheDocument();
    apiPost.mockImplementation(() => new Promise(() => {}));
    await view.rerender({
      equalizer: { enabled: true, filters: [{ ...LOW_PASS, frequency: 12000 }] },
    });
    await settle();
    expect(screen.getByTestId('eq-graph-updating')).toHaveTextContent(
      'settings.audio.audioFilters.graph.updating'
    );
    expect(screen.queryByTestId('eq-graph-loading')).not.toBeInTheDocument();
    expect(screen.queryByTestId('eq-graph-sample-rate')).not.toBeInTheDocument();
    expect(lineToInLastDraw(ctx)).toBe(GRID_LINE_COUNT + 2);
  });

  it('draws the flat label for an empty filter list and for all filters skipped', async () => {
    const ctx = useFullCanvas();
    apiPost.mockResolvedValueOnce(responseBody());
    const view = render(FilterResponseGraph, {
      props: { equalizer: { enabled: true, filters: [] } },
    });
    await settle();
    expect(labelsInLastDraw(ctx)).toContain(FLAT_LABEL);

    apiPost.mockResolvedValueOnce(
      responseBody({
        skipped: [
          { index: 0, type: 'HighPass', reason: 'invalid_parameters' },
          { index: 1, type: 'LowPass', reason: 'invalid_parameters' },
        ],
      })
    );
    await view.rerender({ equalizer: { enabled: true, filters: [HIGH_PASS, LOW_PASS] } });
    await settle();
    expect(screen.getByTestId('eq-graph-skipped')).toBeInTheDocument();
    expect(labelsInLastDraw(ctx)).toContain(FLAT_LABEL);
  });

  it('lists each skipped filter with its translated reason and the posted type and frequency', async () => {
    apiPost.mockResolvedValue(
      responseBody({
        skipped: [{ index: 1, type: 'LowPass', reason: 'invalid_parameters', message: 'x' }],
      })
    );
    renderTyped(FilterResponseGraph, {
      props: { equalizer: { enabled: true, filters: [HIGH_PASS, LOW_PASS] } },
    });
    await settle();
    const notice = screen.getByTestId('eq-graph-skipped');
    expect(notice).toHaveTextContent(
      'settings.audio.audioFilters.graph.skippedReasons.invalid_parameters'
    );
    expect(notice).toHaveTextContent('settings.audio.audioFilters.graph.skippedHeading');
    expect(t).toHaveBeenCalledWith(
      'settings.audio.audioFilters.graph.skippedReasons.invalid_parameters',
      expect.objectContaining({ number: 2, type: 'LowPass', frequency: 15000, rate: 48000 })
    );
    expect(t).toHaveBeenCalledWith('settings.audio.audioFilters.graph.skippedHeading', {
      count: 1,
    });
    expect(screen.getByRole('status')).toHaveTextContent(
      'settings.audio.audioFilters.graph.skippedHeading'
    );
  });

  it('numbers the first skipped filter 1 and names it from the posted request', async () => {
    apiPost.mockResolvedValue(
      responseBody({ skipped: [{ index: 0, type: 'Unrecognised', reason: 'unknown_type' }] })
    );
    renderTyped(FilterResponseGraph, {
      props: { equalizer: { enabled: true, filters: [HIGH_PASS, LOW_PASS] } },
    });
    await settle();
    expect(t).toHaveBeenCalledWith(
      'settings.audio.audioFilters.graph.skippedReasons.unknown_type',
      expect.objectContaining({ number: 1, type: 'HighPass', frequency: 100 })
    );
  });

  it('shows no skipped notice and announces nothing when no filter is skipped', async () => {
    apiPost.mockResolvedValue(responseBody());
    renderTyped(FilterResponseGraph, {
      props: { equalizer: { enabled: true, filters: [HIGH_PASS, LOW_PASS] } },
    });
    await settle();
    expect(screen.getByTestId('eq-graph-sample-rate')).toBeInTheDocument();
    expect(screen.queryByTestId('eq-graph-skipped')).not.toBeInTheDocument();
    expect(screen.getByRole('status').textContent).toBe('');
  });

  it('shows the nearest returned point in the tooltip and hides it on a new state', async () => {
    useFullCanvas();
    apiPost.mockResolvedValueOnce(responseBody());
    const view = render(FilterResponseGraph, {
      props: { equalizer: { enabled: true, filters: [LOW_PASS] } },
    });
    await settle();
    const canvas = view.container.querySelector('canvas');
    if (!canvas) throw new Error('canvas not rendered');
    // jsdom reports a zero bounding box, so client coordinates are canvas
    // coordinates: x 100 is the left edge of the plot (20 Hz).
    await fireEvent.mouseMove(canvas, { clientX: 100, clientY: 100 });
    expect(screen.getByText('20 Hz')).toBeInTheDocument();
    expect(screen.getByText('-12 dB')).toBeInTheDocument();

    apiPost.mockImplementation(() => new Promise(() => {}));
    await view.rerender({ equalizer: { enabled: true, filters: [] } });
    await tick();
    expect(screen.queryByText('20 Hz')).not.toBeInTheDocument();
  });

  it('removes a drawn curve when a later request fails', async () => {
    const ctx = useFullCanvas();
    apiPost.mockResolvedValueOnce(responseBody());
    const view = render(FilterResponseGraph, {
      props: { equalizer: { enabled: true, filters: [LOW_PASS] } },
    });
    await settle();
    expect(lineToInLastDraw(ctx)).toBe(GRID_LINE_COUNT + 2);

    apiPost.mockRejectedValueOnce(new Error('server error'));
    await view.rerender({
      equalizer: { enabled: true, filters: [{ ...LOW_PASS, frequency: 12000 }] },
    });
    await settle();
    expect(screen.getByTestId('eq-graph-error')).toBeInTheDocument();
    expect(lineToInLastDraw(ctx)).toBe(GRID_LINE_COUNT);
    expect(callsInLastDraw(ctx, 'stroke')).toHaveLength(GRID_STROKE_COUNT);
  });

  it('shows the error alert, draws no curve and retries on Retry', async () => {
    const ctx = useFullCanvas();
    apiPost.mockRejectedValueOnce(new Error('server error'));
    apiPost.mockResolvedValueOnce(responseBody());
    renderTyped(FilterResponseGraph, {
      props: { equalizer: { enabled: true, filters: [LOW_PASS] } },
    });
    await settle();
    const alert = screen.getByTestId('eq-graph-error');
    expect(alert).toHaveTextContent('settings.audio.audioFilters.graph.error');
    expect(lineToInLastDraw(ctx)).toBe(GRID_LINE_COUNT);
    // The always-rendered alert region announces the error; the visible box
    // is not a second live region.
    expect(alert).toHaveAttribute('role', 'note');
    expect(screen.getByRole('alert')).toHaveTextContent('settings.audio.audioFilters.graph.error');
    await fireEvent.click(screen.getByRole('button', { name: 'common.retry' }));
    expect(apiPost).toHaveBeenCalledTimes(2);
    // The alert and its button are gone, so focus moves to the graph, not the body.
    expect(document.activeElement).not.toBe(document.body);
    expect(document.activeElement).toHaveAttribute('tabindex', '-1');
    await settle(0);
    expect(screen.getByTestId('eq-graph-sample-rate')).toBeInTheDocument();
    expect(screen.queryByTestId('eq-graph-error')).not.toBeInTheDocument();
  });

  it('shows the error state, not a curve, for the default {data: []} response', async () => {
    const post = realPost.current;
    if (!post) throw new Error('real api.post not captured');
    apiPost.mockImplementation(post);
    renderTyped(FilterResponseGraph, {
      props: { equalizer: { enabled: true, filters: [LOW_PASS] } },
    });
    await settle();
    await settle(0);
    expect(screen.getByTestId('eq-graph-error')).toBeInTheDocument();
    expect(apiPost).toHaveBeenCalledWith(
      EQUALIZER_RESPONSE_URL,
      expect.anything(),
      expect.anything()
    );
    expect(screen.queryByTestId('eq-graph-sample-rate')).not.toBeInTheDocument();
  });

  it('posts the sampleRate prop and omits it when absent', async () => {
    apiPost.mockImplementation(() => new Promise(() => {}));
    const view = render(FilterResponseGraph, {
      props: { equalizer: { enabled: false, filters: [LOW_PASS] }, sampleRate: 192000 },
    });
    await settle();
    expect(apiPost).toHaveBeenCalledTimes(1);
    expect(apiPost).toHaveBeenCalledWith(
      EQUALIZER_RESPONSE_URL,
      {
        equalizer: {
          enabled: false,
          filters: [{ type: 'LowPass', frequency: 15000, q: 0.707, passes: 1 }],
        },
        sampleRate: 192000,
      },
      expect.objectContaining({ signal: expect.any(AbortSignal) })
    );
    await view.rerender({
      equalizer: { enabled: false, filters: [LOW_PASS] },
      sampleRate: undefined,
    });
    await settle();
    expect(apiPost).toHaveBeenCalledTimes(2);
    expect(lastBody()).not.toHaveProperty('sampleRate');
  });

  it('stops requesting after unmount', async () => {
    apiPost.mockImplementation(() => new Promise(() => {}));
    const view = render(FilterResponseGraph, {
      props: { equalizer: { enabled: true, filters: [LOW_PASS] } },
    });
    view.unmount();
    await settle(EQUALIZER_RESPONSE_DEBOUNCE_MS * 2);
    expect(apiPost).not.toHaveBeenCalled();
  });
});
