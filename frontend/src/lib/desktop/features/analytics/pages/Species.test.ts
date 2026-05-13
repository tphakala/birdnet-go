import { describe, it, expect, afterEach, vi } from 'vitest';
import { cleanup, waitFor } from '@testing-library/svelte';
import { createComponentTestFactory } from '../../../../../test/render-helpers';
import { setBasePath, resetBasePath } from '$lib/utils/urlHelpers';
import Species from './Species.svelte';

interface SpeciesSummary {
  common_name: string;
  scientific_name: string;
  count: number;
  avg_confidence: number;
  max_confidence: number;
  first_heard: string;
  last_heard: string;
  thumbnail_url?: string;
}

function mockFetchSequence(handlers: Record<string, () => unknown>) {
  return vi.fn().mockImplementation((input: RequestInfo | URL) => {
    const url = typeof input === 'string' ? input : input.toString();
    for (const [pattern, body] of Object.entries(handlers)) {
      if (url.includes(pattern)) {
        return Promise.resolve({
          ok: true,
          status: 200,
          statusText: 'OK',
          headers: new Headers({ 'content-type': 'application/json' }),
          json: () => Promise.resolve(body()),
          text: () => Promise.resolve(JSON.stringify(body())),
        });
      }
    }
    return Promise.reject(new Error(`Unexpected fetch in test: ${url}`));
  });
}

const speciesTest = createComponentTestFactory(Species);

describe('Species (analytics page)', () => {
  const originalFetch = globalThis.fetch;

  afterEach(() => {
    cleanup();
    resetBasePath();
    globalThis.fetch = originalFetch;
  });

  it('prefixes thumbnail URLs with the configured base path (regression)', async () => {
    // Reproduces the bug reported on /ui/analytics/species: the backend
    // returns a relative thumbnail_url like /api/v2/media/image/<name>, and
    // when configured behind a reverse proxy (e.g. /birdnet), the frontend
    // must prepend the base path before using it as <img src>.
    setBasePath('/birdnet');

    const summary: SpeciesSummary[] = [
      {
        common_name: "Wilson's Warbler",
        scientific_name: 'Cardellina pusilla',
        count: 42,
        avg_confidence: 0.85,
        max_confidence: 0.95,
        first_heard: '2026-04-01',
        last_heard: '2026-04-27',
        thumbnail_url: '/api/v2/media/image/Cardellina%20pusilla',
      },
    ];

    globalThis.fetch = mockFetchSequence({
      '/api/v2/analytics/species/summary': () => summary,
      '/api/v2/analytics/species/thumbnails': () => ({}),
      // Page mounts fetchAudioSources(); without a mock the catch handler
      // calls loggers.analytics.error(), which is undefined in this test
      // env and throws an unhandled TypeError.
      '/api/v2/analytics/sources': () => ({ sources: [] }),
    });

    const { container } = speciesTest.render({});

    // Wait for the async fetch + render to complete and an <img> to appear
    // in either the grid card or the table list view.
    const img = await waitFor(
      () => {
        const found = container.querySelector('img');
        if (!found) throw new Error('image not yet rendered');
        return found;
      },
      { timeout: 2000 }
    );

    expect(img.getAttribute('src')).toBe('/birdnet/api/v2/media/image/Cardellina%20pusilla');
  });

  it('also prefixes URLs returned by the batched thumbnails endpoint', async () => {
    setBasePath('/birdnet');

    const summary: SpeciesSummary[] = [
      {
        common_name: 'Northern Cardinal',
        scientific_name: 'Cardinalis cardinalis',
        count: 7,
        avg_confidence: 0.91,
        max_confidence: 0.99,
        first_heard: '2026-04-10',
        last_heard: '2026-04-26',
        // No thumbnail_url here — the page's loadThumbnailsAsync() should
        // populate it from the batch endpoint.
      },
    ];

    globalThis.fetch = mockFetchSequence({
      '/api/v2/analytics/species/summary': () => summary,
      '/api/v2/analytics/species/thumbnails': () => ({
        'Cardinalis cardinalis': '/api/v2/media/image/Cardinalis%20cardinalis',
      }),
      // Same reason as the first test — fetchAudioSources fires on mount.
      '/api/v2/analytics/sources': () => ({ sources: [] }),
    });

    const { container } = speciesTest.render({});

    const img = await waitFor(
      () => {
        const found = container.querySelector('img');
        if (!found?.getAttribute('src')?.includes('Cardinalis')) {
          throw new Error('thumbnail not yet rendered');
        }
        return found;
      },
      { timeout: 2000 }
    );

    expect(img.getAttribute('src')).toBe('/birdnet/api/v2/media/image/Cardinalis%20cardinalis');
  });
});
