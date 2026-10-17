/**
 * FilterResponseGraph plots the curve the backend computes. These tests cover
 * the request it sends and the states it shows around the canvas (loading,
 * updating, sample rate caption, skipped filters, error with Retry); the
 * canvas pixels themselves are not checked.
 */
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/svelte';
import { t } from '$lib/i18n';
import { renderTyped } from '../../../../../test/render-helpers';
import FilterResponseGraph from './FilterResponseGraph.svelte';
import { EQUALIZER_RESPONSE_DEBOUNCE_MS, EQUALIZER_RESPONSE_URL } from '$lib/api/equalizer';
import type { EqualizerResponseRequest } from '$lib/api/equalizer';

type ApiPost = (
  url: string,
  data?: unknown,
  options?: { signal?: AbortSignal }
) => Promise<unknown>;

const { apiPost, realPost } = vi.hoisted(() => ({
  apiPost: vi.fn<ApiPost>(),
  realPost: { current: undefined as ApiPost | undefined },
}));

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

/** Wait past the debounce so the request has been sent. */
async function waitForRequest(count = 1): Promise<void> {
  await waitFor(() => expect(apiPost).toHaveBeenCalledTimes(count), {
    timeout: EQUALIZER_RESPONSE_DEBOUNCE_MS * 4,
  });
}

function lastBody(): EqualizerResponseRequest | undefined {
  const call = apiPost.mock.calls.at(-1);
  const body = call?.[1];
  return body !== undefined && typeof body === 'object' && body !== null && 'equalizer' in body
    ? // Test-only narrowing of a recorded mock argument
      (body as EqualizerResponseRequest)
    : undefined;
}

describe('FilterResponseGraph', () => {
  beforeEach(() => {
    apiPost.mockReset();
    vi.mocked(t).mockClear();
  });

  afterEach(() => {
    cleanup();
    apiPost.mockReset();
  });

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
    await waitFor(() =>
      expect(region).toHaveTextContent('settings.audio.audioFilters.graph.loading')
    );
  });

  it('shows the sample rate caption from the response', async () => {
    apiPost.mockResolvedValue(responseBody({ sampleRate: 96000 }));
    renderTyped(FilterResponseGraph, {
      props: { equalizer: { enabled: true, filters: [LOW_PASS] }, sampleRate: 96000 },
    });
    await waitFor(() => expect(screen.getByTestId('eq-graph-sample-rate')).toBeInTheDocument(), {
      timeout: 1000,
    });
    expect(t).toHaveBeenCalledWith('settings.audio.audioFilters.graph.sampleRate', {
      rate: 96000,
    });
    expect(screen.queryByTestId('eq-graph-loading')).not.toBeInTheDocument();
  });

  it('keeps the previous curve and shows the updating text while a new request is pending', async () => {
    apiPost.mockResolvedValueOnce(responseBody());
    const view = render(FilterResponseGraph, {
      props: { equalizer: { enabled: true, filters: [LOW_PASS] } },
    });
    await waitFor(() => expect(screen.getByTestId('eq-graph-sample-rate')).toBeInTheDocument(), {
      timeout: 1000,
    });
    apiPost.mockImplementation(() => new Promise(() => {}));
    await view.rerender({
      equalizer: { enabled: true, filters: [{ ...LOW_PASS, frequency: 12000 }] },
    });
    expect(screen.getByTestId('eq-graph-updating')).toHaveTextContent(
      'settings.audio.audioFilters.graph.updating'
    );
    expect(screen.queryByTestId('eq-graph-loading')).not.toBeInTheDocument();
    expect(screen.queryByTestId('eq-graph-sample-rate')).not.toBeInTheDocument();
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
    const notice = await screen.findByTestId('eq-graph-skipped', undefined, { timeout: 1000 });
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
  });

  it('shows the error alert and retries on Retry', async () => {
    apiPost.mockRejectedValueOnce(new Error('server error'));
    apiPost.mockResolvedValueOnce(responseBody());
    renderTyped(FilterResponseGraph, {
      props: { equalizer: { enabled: true, filters: [LOW_PASS] } },
    });
    const alert = await screen.findByTestId('eq-graph-error', undefined, { timeout: 1000 });
    expect(alert).toHaveTextContent('settings.audio.audioFilters.graph.error');
    // The always-rendered alert region announces the error; the visible box
    // is not a second live region.
    expect(alert).toHaveAttribute('role', 'note');
    expect(screen.getByRole('alert')).toHaveTextContent('settings.audio.audioFilters.graph.error');
    await fireEvent.click(screen.getByRole('button', { name: 'common.retry' }));
    expect(apiPost).toHaveBeenCalledTimes(2);
    // The alert and its button are gone, so focus moves to the graph, not the body.
    expect(document.activeElement).not.toBe(document.body);
    expect(document.activeElement).toHaveAttribute('tabindex', '-1');
    await waitFor(() => expect(screen.getByTestId('eq-graph-sample-rate')).toBeInTheDocument());
    expect(screen.queryByTestId('eq-graph-error')).not.toBeInTheDocument();
  });

  it('shows the error state, not a curve, for the default {data: []} response', async () => {
    const post = realPost.current;
    if (!post) throw new Error('real api.post not captured');
    apiPost.mockImplementation(post);
    renderTyped(FilterResponseGraph, {
      props: { equalizer: { enabled: true, filters: [LOW_PASS] } },
    });
    await screen.findByTestId('eq-graph-error', undefined, { timeout: 1000 });
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
    await waitForRequest(1);
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
    await waitForRequest(2);
    expect(lastBody()).not.toHaveProperty('sampleRate');
  });

  it('stops requesting after unmount', async () => {
    apiPost.mockImplementation(() => new Promise(() => {}));
    const view = render(FilterResponseGraph, {
      props: { equalizer: { enabled: true, filters: [LOW_PASS] } },
    });
    view.unmount();
    await new Promise(resolve => setTimeout(resolve, EQUALIZER_RESPONSE_DEBOUNCE_MS * 2));
    expect(apiPost).not.toHaveBeenCalled();
  });
});
