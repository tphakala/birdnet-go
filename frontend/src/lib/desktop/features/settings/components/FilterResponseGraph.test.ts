/**
 * FilterResponseGraph plots the curve the backend computes. These tests cover
 * the request it sends and the states it shows around the canvas (loading,
 * updating, sample rate caption, skipped filters, error with Retry); the
 * canvas pixels themselves are not checked.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { tick } from 'svelte';
import { cleanup, fireEvent, render, screen, within } from '@testing-library/svelte';
import { t } from '$lib/i18n';
import { isPlainObject } from '$lib/utils/security';
import { renderTyped } from '../../../../../test/render-helpers';
import FilterResponseGraph, {
  AUDIBLE_MAX_HZ,
  AUDIBLE_GRID_HZ,
  BASE_MAX_DB,
  DB_GRID_STEP,
  MAX_TOP_DB,
  MIN_DB,
  MIN_FREQ_HZ,
  axisMaxHz,
  frequencyGridLines,
  frequencyToFraction,
  fractionToFrequency,
  gainAxis,
  gainGridLines,
} from './FilterResponseGraph.svelte';
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
    minHz: 20,
    maxHz: 20000,
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
  const ctx = {
    font: '',
    /** Each fillText call with the font that was set when it ran. */
    textFonts: [] as Array<{ text: unknown; font: string }>,
    clearRect: vi.fn(),
    fillRect: vi.fn(),
    beginPath: vi.fn(),
    moveTo: vi.fn(),
    lineTo: vi.fn(),
    stroke: vi.fn(),
    rect: vi.fn(),
    fillText: vi.fn((text: unknown) => {
      ctx.textFonts.push({ text, font: ctx.font });
    }),
    save: vi.fn(),
    restore: vi.fn(),
    translate: vi.fn(),
    rotate: vi.fn(),
    setLineDash: vi.fn(),
    createLinearGradient: vi.fn(() => ({ addColorStop: vi.fn() })),
  };
  return ctx;
}

/** Font the most recent draw used for a frequency axis label. */
function axisLabelFont(ctx: ReturnType<typeof createContextMock>): string | undefined {
  return ctx.textFonts.filter(entry => entry.text === '1k').at(-1)?.font;
}

/** The graph draws 16 frequency grid lines, the dB grid lines for its axis top and one 0 dB reference line. */
const FREQUENCY_GRID_LINES = 16;
const DB_GRID_LINES = gainGridLines(BASE_MAX_DB).length;
const REFERENCE_LINES = 1;
const PLOT_BORDER_STROKES = 1;

/** Plot geometry in jsdom: the canvas falls back to its 600 px minimum width and the 400 px default height. */
const PLOT_LEFT = 100;
const PLOT_RIGHT = 540;
const PLOT_BOTTOM = 300;
const PLOT_HEIGHT = 260;

/** The y position the graph maps a gain to when the axis tops out at maxDb. */
function gainToY(gainDb: number, maxDb: number): number {
  return PLOT_BOTTOM - ((gainDb - MIN_DB) / (maxDb - MIN_DB)) * PLOT_HEIGHT;
}

const GRID_LINE_COUNT = FREQUENCY_GRID_LINES + DB_GRID_LINES + REFERENCE_LINES;

/** lineTo calls made by the most recent draw (after its clearRect). */
function lineToInLastDraw(ctx: ReturnType<typeof createContextMock>): number {
  const lastClear = Math.max(0, ...ctx.clearRect.mock.invocationCallOrder);
  return ctx.lineTo.mock.invocationCallOrder.filter(order => order > lastClear).length;
}

/** Strokes made by the most recent draw: the plot border, the grid lines and the reference line, plus one for a curve. */
const GRID_STROKE_COUNT = PLOT_BORDER_STROKES + GRID_LINE_COUNT;

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
const CLIPPED_KEY = 'settings.audio.audioFilters.graph.clipped';

/** A response whose only drawn points are the given gains, spread over the audible range. */
function peakBody(...gains: number[]): Record<string, unknown> {
  return responseBody({
    points: gains.map((gainDb, i) => ({ hz: 100 * (i + 1), gainDb })),
  });
}

/** A 256 kHz response swept over the full band, with points past 20 kHz. */
function fullBandBody(): Record<string, unknown> {
  return responseBody({
    sampleRate: 256000,
    maxHz: 128000,
    points: [
      { hz: 1000, gainDb: 0 },
      { hz: 60000, gainDb: -20 },
      { hz: 128000, gainDb: -3 },
    ],
  });
}

/** dB tick labels drawn by the most recent draw. */
function dbLabelsInLastDraw(ctx: ReturnType<typeof createContextMock>): string[] {
  return labelsInLastDraw(ctx).filter(
    (label): label is string => typeof label === 'string' && label.endsWith('dB')
  );
}

/** The y of the highest curve vertex (smallest y) in the most recent draw. */
function curveTopY(ctx: ReturnType<typeof createContextMock>): number | undefined {
  const ys = [...callsInLastDraw(ctx, 'moveTo'), ...callsInLastDraw(ctx, 'lineTo')]
    .map(call => call[1])
    .filter((y): y is number => typeof y === 'number');
  return ys.length > 0 ? Math.min(...ys.slice(-3)) : undefined;
}

describe('gainAxis', () => {
  const MIN_HZ = 20;
  const MAX_HZ = 20000;
  const at = (gainDb: number, hz = 1000) => [{ hz, gainDb }];

  it.each([
    ['no points', [], BASE_MAX_DB, false],
    ['a flat 0 dB response', at(0), BASE_MAX_DB, false],
    ['a 11.9 dB peak', at(11.9), BASE_MAX_DB, false],
    ['a 12 dB peak', at(12), BASE_MAX_DB, false],
    ['a 12.000000000000002 dB peak (float noise)', at(12.000000000000002), BASE_MAX_DB, false],
    ['a 12.1 dB peak', at(12.1), 24, false],
    ['a 40 dB peak', at(40), 48, false],
    ['a 48 dB peak', at(48), MAX_TOP_DB, false],
    ['a 48.00000000000001 dB peak (float noise)', at(48.00000000000001), MAX_TOP_DB, false],
    ['a 48.1 dB peak', at(48.1), MAX_TOP_DB, true],
    ['a 200 dB peak', at(200), MAX_TOP_DB, true],
    ['a 30 dB point outside the drawn range', at(30, 22000), BASE_MAX_DB, false],
  ])('gives the axis for %s', (_name, points, maxDb, clipped) => {
    const axis = gainAxis(points, MIN_HZ, MAX_HZ);
    expect(axis.maxDb).toBe(maxDb);
    expect(axis.clipped).toBe(clipped);
  });

  it('reports the rounded peak of the drawn points only', () => {
    expect(gainAxis([...at(30, 22000), ...at(7.26)], MIN_HZ, MAX_HZ).peakDb).toBe(7.3);
    expect(gainAxis([], MIN_HZ, MAX_HZ).peakDb).toBeUndefined();
  });
});

describe('gainGridLines', () => {
  it.each([BASE_MAX_DB, 24, 36, MAX_TOP_DB])(
    'gives ascending multiples of the step from the bottom to %s dB',
    maxDb => {
      const lines = gainGridLines(maxDb);
      expect(lines[0]).toBe(MIN_DB);
      expect(lines.at(-1)).toBe(maxDb);
      expect(lines.every((db, i) => i === 0 || db - (lines.at(i - 1) ?? 0) === DB_GRID_STEP)).toBe(
        true
      );
    }
  );

  it('has ten lines for the default axis', () => {
    expect(gainGridLines(BASE_MAX_DB)).toHaveLength(10);
  });
});

describe('frequency axis', () => {
  const FULL_BAND_MAX_HZ = 128000;
  const ROUND_TRIP_TOLERANCE = 1e-9;

  /** The audible scale the graph used before the axis followed the response. */
  function audibleFraction(hz: number): number {
    if (hz <= 1000)
      return ((Math.log10(hz) - Math.log10(20)) / (Math.log10(1000) - Math.log10(20))) * 0.3;
    return 0.3 + 0.7 * Math.log10(1 + ((hz - 1000) / 19000) * 9);
  }

  it('keeps the audible scale for an axis that ends at 20 kHz', () => {
    for (const hz of [20, 100, 1000, 3000, 10000, 20000]) {
      expect(frequencyToFraction(hz, AUDIBLE_MAX_HZ)).toBeCloseTo(audibleFraction(hz), 12);
    }
  });

  it.each([AUDIBLE_MAX_HZ, FULL_BAND_MAX_HZ])(
    'inverts the scale for an axis ending at %d Hz',
    maxHz => {
      for (const hz of [100, 1000, 10000]) {
        expect(fractionToFrequency(frequencyToFraction(hz, maxHz), maxHz)).toBeCloseTo(hz, 6);
        expect(
          Math.abs(frequencyToFraction(fractionToFrequency(0.5, maxHz), maxHz) - 0.5)
        ).toBeLessThan(ROUND_TRIP_TOLERANCE);
      }
    }
  );

  it.each([AUDIBLE_MAX_HZ, FULL_BAND_MAX_HZ])(
    'puts the edges of a %d Hz axis at 0 and 1',
    maxHz => {
      expect(frequencyToFraction(MIN_FREQ_HZ, maxHz)).toBeCloseTo(0, 12);
      expect(frequencyToFraction(maxHz, maxHz)).toBeCloseTo(1, 12);
    }
  );

  it('uses a plain log scale past 20 kHz', () => {
    // 20 Hz to 128 kHz: 1 kHz is log(50) of log(6400) of the way.
    expect(frequencyToFraction(1000, FULL_BAND_MAX_HZ)).toBeCloseTo(
      Math.log(1000 / 20) / Math.log(FULL_BAND_MAX_HZ / 20),
      12
    );
  });

  it('gives the audible axis its 16 grid lines', () => {
    expect(frequencyGridLines(AUDIBLE_MAX_HZ)).toEqual(AUDIBLE_GRID_HZ);
    expect(AUDIBLE_GRID_HZ).toHaveLength(16);
  });

  it('adds an end label when the last grid line is far below the axis end', () => {
    const lines = frequencyGridLines(48000);
    expect(lines.slice(-2)).toEqual([20000, 48000]);
  });

  it.each([128000, 192000])('ends the grid of a %d Hz axis at 100 kHz', maxHz => {
    const lines = frequencyGridLines(maxHz);
    expect(lines.at(-1)).toBe(100000);
    expect(lines.every(hz => hz <= maxHz)).toBe(true);
  });

  it.each([
    ['below the audible band', 8000, AUDIBLE_MAX_HZ],
    ['at the audible limit', 20000, AUDIBLE_MAX_HZ],
    ['above the audible limit', 128000, 128000],
    ['not yet known', undefined, AUDIBLE_MAX_HZ],
  ])('sizes the axis for a response swept %s', (_name, responseMaxHz, expected) => {
    expect(axisMaxHz(responseMaxHz)).toBe(expected);
  });
});

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
    expect(screen.getByRole('alert').textContent).toBe('');
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
    // The curve starts at the first point (20 Hz, -12 dB) and ends at the
    // last one (20 kHz, -6 dB).
    const start = callsInLastDraw(ctx, 'moveTo').at(-1);
    const end = callsInLastDraw(ctx, 'lineTo').at(-1);
    expect(start?.[0]).toBeCloseTo(PLOT_LEFT);
    expect(start?.[1]).toBeCloseTo(gainToY(-12, BASE_MAX_DB));
    expect(end?.[0]).toBeCloseTo(PLOT_RIGHT);
    expect(end?.[1]).toBeCloseTo(gainToY(-6, BASE_MAX_DB));
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
    // The flat label uses a larger font; the axis labels drawn after it keep
    // the axis font.
    expect(axisLabelFont(ctx)).toMatch(/^11px /);

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

  it('draws the flat label when every filter has passes 0 and none is skipped', async () => {
    const ctx = useFullCanvas();
    apiPost.mockResolvedValueOnce(
      responseBody({
        points: [
          { hz: 20, gainDb: 0 },
          { hz: 1000, gainDb: 0 },
          { hz: 20000, gainDb: 0 },
        ],
      })
    );
    const view = render(FilterResponseGraph, {
      props: {
        equalizer: {
          enabled: true,
          filters: [
            { ...HIGH_PASS, passes: 0 },
            { ...LOW_PASS, passes: 0 },
          ],
        },
      },
    });
    await settle();
    expect(screen.queryByTestId('eq-graph-skipped')).not.toBeInTheDocument();
    expect(labelsInLastDraw(ctx)).toContain(FLAT_LABEL);

    // One filter switched back on, and the label goes away.
    apiPost.mockResolvedValueOnce(responseBody());
    await view.rerender({
      equalizer: { enabled: true, filters: [{ ...HIGH_PASS, passes: 0 }, LOW_PASS] },
    });
    await settle();
    expect(labelsInLastDraw(ctx)).not.toContain(FLAT_LABEL);
  });

  it('lists each skipped filter with its translated reason and the posted type and frequency', async () => {
    const reason = 'WidthHz must be > 0 for a peaking filter';
    apiPost.mockResolvedValue(
      responseBody({
        skipped: [{ index: 1, type: 'LowPass', reason: 'invalid_parameters', message: reason }],
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
    // The backend's specific reason is shown with the filter it belongs to.
    expect(within(notice).getByText(reason)).toBeInTheDocument();
    // The list is block content, so no inline element may contain it.
    expect(notice.querySelector('span ul')).toBeNull();
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
      responseBody({
        skipped: [
          {
            index: 0,
            type: 'Unrecognised',
            reason: 'unknown_type',
            message: 'unknown filter type Unrecognised',
          },
        ],
      })
    );
    renderTyped(FilterResponseGraph, {
      props: { equalizer: { enabled: true, filters: [HIGH_PASS, LOW_PASS] } },
    });
    await settle();
    expect(t).toHaveBeenCalledWith(
      'settings.audio.audioFilters.graph.skippedReasons.unknown_type',
      expect.objectContaining({ number: 1, type: 'HighPass', frequency: 100 })
    );
    // The translated unknown-type text already says why; the English backend
    // message is not repeated under it.
    expect(screen.queryByText('unknown filter type Unrecognised')).not.toBeInTheDocument();
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
    expect(screen.getByRole('alert').textContent).toBe('');
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
    // coordinates: the left edge of the plot is 20 Hz.
    await fireEvent.mouseMove(canvas, { clientX: PLOT_LEFT, clientY: PLOT_BOTTOM });
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
    expect(alert.querySelector('span button')).toBeNull();
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
    expect(screen.getByRole('alert').textContent).toBe('');
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

  it('scales the axis to a +40 dB peak and back to +12 for the next flat response', async () => {
    const ctx = useFullCanvas();
    apiPost.mockResolvedValueOnce(peakBody(0, 40, 0));
    const view = render(FilterResponseGraph, {
      props: { equalizer: { enabled: true, filters: [LOW_PASS] } },
    });
    await settle();
    expect(dbLabelsInLastDraw(ctx)).toContain('48dB');
    expect(dbLabelsInLastDraw(ctx)).not.toContain('60dB');
    expect(curveTopY(ctx)).toBeCloseTo(gainToY(40, 48));
    expect(screen.queryByTestId('eq-graph-clipped')).not.toBeInTheDocument();

    apiPost.mockResolvedValueOnce(responseBody());
    await view.rerender({ equalizer: { enabled: true, filters: [HIGH_PASS] } });
    await settle();
    expect(dbLabelsInLastDraw(ctx)).toContain('12dB');
    expect(dbLabelsInLastDraw(ctx)).not.toContain('48dB');
    expect(screen.queryByTestId('eq-graph-clipped')).not.toBeInTheDocument();
  });

  it('keeps the previous response axis and caption while a new request is pending', async () => {
    const ctx = useFullCanvas();
    apiPost.mockResolvedValueOnce(peakBody(0, 80, 0));
    const view = render(FilterResponseGraph, {
      props: { equalizer: { enabled: true, filters: [LOW_PASS] } },
    });
    await settle();
    expect(screen.getByTestId('eq-graph-clipped')).toBeInTheDocument();

    apiPost.mockImplementation(() => new Promise(() => {}));
    await view.rerender({ equalizer: { enabled: true, filters: [HIGH_PASS] } });
    await settle();
    expect(screen.getByTestId('eq-graph-updating')).toBeInTheDocument();
    expect(screen.getByTestId('eq-graph-clipped')).toBeInTheDocument();
    expect(dbLabelsInLastDraw(ctx)).toContain('48dB');
  });

  it('draws every point of a full-band response on an axis that follows the response', async () => {
    const ctx = useFullCanvas();
    apiPost.mockResolvedValueOnce(fullBandBody());
    renderTyped(FilterResponseGraph, {
      props: { equalizer: { enabled: true, filters: [LOW_PASS] }, sampleRate: 256000 },
    });
    await settle();
    const fullBandGridLines = frequencyGridLines(128000).length;
    // Three vertices: one moveTo and two lineTo calls beyond the grid lines.
    expect(lineToInLastDraw(ctx)).toBe(fullBandGridLines + DB_GRID_LINES + REFERENCE_LINES + 2);
    expect(labelsInLastDraw(ctx)).toContain('100k');
  });

  it('keeps the previous response axis while a new request is pending', async () => {
    const ctx = useFullCanvas();
    apiPost.mockResolvedValueOnce(fullBandBody());
    const view = render(FilterResponseGraph, {
      props: { equalizer: { enabled: true, filters: [LOW_PASS] }, sampleRate: 256000 },
    });
    await settle();
    expect(labelsInLastDraw(ctx)).toContain('100k');

    apiPost.mockImplementation(() => new Promise(() => {}));
    await view.rerender({ equalizer: { enabled: true, filters: [HIGH_PASS] }, sampleRate: 48000 });
    await settle();
    expect(screen.getByTestId('eq-graph-updating')).toBeInTheDocument();
    expect(labelsInLastDraw(ctx)).toContain('100k');
  });

  it('clamps a peak above the cap to the top edge and shows the caption with the peak', async () => {
    const ctx = useFullCanvas();
    apiPost.mockResolvedValueOnce(peakBody(0, 80.04, 0));
    const view = render(FilterResponseGraph, {
      props: { equalizer: { enabled: true, filters: [LOW_PASS] } },
    });
    await settle();
    expect(curveTopY(ctx)).toBeCloseTo(PLOT_BOTTOM - PLOT_HEIGHT);
    expect(dbLabelsInLastDraw(ctx).at(-1)).toBe(`${MAX_TOP_DB}dB`);
    expect(screen.getByTestId('eq-graph-clipped')).toHaveTextContent(CLIPPED_KEY);
    expect(t).toHaveBeenCalledWith(CLIPPED_KEY, { peak: 80 });
    // The caption row is always rendered so the editor below does not shift, and
    // the status region announces it.
    expect(screen.getByTestId('eq-graph-clipped').parentElement).toHaveClass('min-h-5');
    expect(screen.getByRole('status')).toHaveTextContent(CLIPPED_KEY);

    // The caption goes on a later response below the cap.
    apiPost.mockResolvedValueOnce(peakBody(0, 20, 0));
    await view.rerender({ equalizer: { enabled: true, filters: [HIGH_PASS] } });
    await settle();
    expect(screen.queryByTestId('eq-graph-clipped')).not.toBeInTheDocument();
  });

  it('returns to the +12 axis without a caption when a request fails after a clipped response', async () => {
    const ctx = useFullCanvas();
    apiPost.mockResolvedValueOnce(peakBody(0, 80.04, 0));
    const view = render(FilterResponseGraph, {
      props: { equalizer: { enabled: true, filters: [LOW_PASS] } },
    });
    await settle();
    expect(screen.getByTestId('eq-graph-clipped')).toBeInTheDocument();
    expect(dbLabelsInLastDraw(ctx)).toContain(`${MAX_TOP_DB}dB`);

    apiPost.mockRejectedValueOnce(new Error('server error'));
    await view.rerender({ equalizer: { enabled: true, filters: [HIGH_PASS] } });
    await settle();
    expect(screen.getByTestId('eq-graph-error')).toBeInTheDocument();
    expect(screen.queryByTestId('eq-graph-clipped')).not.toBeInTheDocument();
    expect(dbLabelsInLastDraw(ctx)).toContain(`${BASE_MAX_DB}dB`);
    expect(dbLabelsInLastDraw(ctx)).not.toContain(`${MAX_TOP_DB}dB`);
  });

  it('shows the true gain in the tooltip for a peak above the top of the graph', async () => {
    useFullCanvas();
    apiPost.mockResolvedValueOnce(
      responseBody({
        points: [
          { hz: 20, gainDb: 80.04 },
          { hz: 1000, gainDb: 0 },
          { hz: 20000, gainDb: -6 },
        ],
      })
    );
    const view = render(FilterResponseGraph, {
      props: { equalizer: { enabled: true, filters: [LOW_PASS] } },
    });
    await settle();
    const canvas = view.container.querySelector('canvas');
    if (!canvas) throw new Error('canvas not rendered');
    // The left edge of the plot is 20 Hz, where the curve is clamped to the top edge.
    await fireEvent.mouseMove(canvas, { clientX: PLOT_LEFT, clientY: PLOT_BOTTOM });
    expect(screen.getByText('20 Hz')).toBeInTheDocument();
    expect(screen.getByText('+80 dB')).toBeInTheDocument();
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
