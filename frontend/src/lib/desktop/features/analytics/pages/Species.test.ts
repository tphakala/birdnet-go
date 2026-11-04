import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { cleanup, fireEvent, waitFor } from '@testing-library/svelte';
import { createComponentTestFactory } from '../../../../../test/render-helpers';
import { setBasePath, resetBasePath } from '$lib/utils/urlHelpers';
import Species from './Species.svelte';
import { t } from '$lib/i18n';
import { tick } from 'svelte';
import { deferred } from '../../../../../test/async-helpers';

// The shared logger mock omits analytics; error-path tests need that category.
vi.mock('$lib/utils/logger', async importOriginal => {
  const actual = await importOriginal<typeof import('$lib/utils/logger')>();
  return {
    ...actual,
    loggers: {
      ...actual.loggers,
      analytics: { ...actual.loggers.analytics, error: vi.fn() },
    },
  };
});

/* eslint-disable security/detect-object-injection -- bracket access in this file is constant numeric indexing into NodeLists (querySelectorAll(...)[CONSTANT]) in assertions, not user-controlled keys. */

/** Must match SORT_STORAGE_KEY in Species.svelte. */
const SORT_STORAGE_KEY = 'analytics.species.sortOrder';

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
});

describe('Species (analytics page) — sortable column headers', () => {
  const originalFetch = globalThis.fetch;

  // Column order in SORTABLE_COLUMNS: species(0), count(1), avgConfidence(2), …
  const COUNT_COLUMN_INDEX = 1;
  // The grid/list view toggle renders two `.join` buttons; index 1 is the list/table view.
  const TABLE_VIEW_TOGGLE_INDEX = 1;
  // localStorage persists the sort order JSON-encoded.
  const COUNT_ASC_SORT_ORDER = 'count_asc';
  const COUNT_ASC_STORED = JSON.stringify(COUNT_ASC_SORT_ORDER);

  // Common names sort A→Z differently from counts, so a wrong sort is visible.
  const summary = [
    {
      common_name: 'American Robin',
      scientific_name: 'Turdus migratorius',
      count: 5,
      avg_confidence: 0.8,
      max_confidence: 0.9,
      first_heard: '2026-04-01',
      last_heard: '2026-04-20',
    },
    {
      common_name: 'Blue Jay',
      scientific_name: 'Cyanocitta cristata',
      count: 99,
      avg_confidence: 0.7,
      max_confidence: 0.95,
      first_heard: '2026-04-05',
      last_heard: '2026-04-25',
    },
    {
      common_name: 'Zebra Finch',
      scientific_name: 'Taeniopygia guttata',
      count: 50,
      avg_confidence: 0.6,
      max_confidence: 0.85,
      first_heard: '2026-04-10',
      last_heard: '2026-04-15',
    },
  ];

  beforeEach(() => {
    vi.clearAllMocks();
    globalThis.fetch = mockFetchSequence({
      '/api/v2/analytics/species/summary': () => summary,
    });
    window.localStorage.clear();
  });

  afterEach(() => {
    cleanup();
    resetBasePath();
    globalThis.fetch = originalFetch;
    window.localStorage.clear();
    vi.restoreAllMocks();
  });

  const speciesTest = createComponentTestFactory(Species);

  /** Render, switch to the list/table view, and wait for its rows. */
  async function renderListView() {
    const { container } = speciesTest.render({});
    // Switch from the default grid view to the list/table view.
    await fireEvent.click(container.querySelectorAll('.join button')[TABLE_VIEW_TOGGLE_INDEX]);
    await waitFor(
      () => {
        if (!container.querySelector('table tbody tr')) throw new Error('table not yet rendered');
      },
      { timeout: 2000 }
    );
    return { container };
  }

  function rowNames(container: HTMLElement): string[] {
    return Array.from(container.querySelectorAll('table tbody tr td .font-bold')).map(el =>
      el.textContent.trim()
    );
  }

  it('defaults to sorting by detection count descending', async () => {
    const { container } = await renderListView();

    expect(rowNames(container)).toEqual(['Blue Jay', 'Zebra Finch', 'American Robin']);
    expect(
      container.querySelectorAll('table thead th')[COUNT_COLUMN_INDEX].getAttribute('aria-sort')
    ).toBe('descending');
  });

  it('toggles detection count to ascending on first header click and back on second', async () => {
    const { container } = await renderListView();
    const countButton = container.querySelectorAll('table thead th button')[COUNT_COLUMN_INDEX];

    await fireEvent.click(countButton);
    expect(rowNames(container)).toEqual(['American Robin', 'Zebra Finch', 'Blue Jay']);
    expect(
      container.querySelectorAll('table thead th')[COUNT_COLUMN_INDEX].getAttribute('aria-sort')
    ).toBe('ascending');

    await fireEvent.click(countButton);
    expect(rowNames(container)).toEqual(['Blue Jay', 'Zebra Finch', 'American Robin']);
    expect(
      container.querySelectorAll('table thead th')[COUNT_COLUMN_INDEX].getAttribute('aria-sort')
    ).toBe('descending');
  });

  it('persists the chosen sort order to localStorage', async () => {
    const { container } = await renderListView();

    await fireEvent.click(container.querySelectorAll('table thead th button')[COUNT_COLUMN_INDEX]);
    expect(window.localStorage.getItem(SORT_STORAGE_KEY)).toBe(COUNT_ASC_STORED);
  });

  it('restores a persisted sort order on a fresh render', async () => {
    window.localStorage.setItem(SORT_STORAGE_KEY, COUNT_ASC_STORED);

    const { container } = await renderListView();

    expect(rowNames(container)).toEqual(['American Robin', 'Zebra Finch', 'Blue Jay']);
    expect(
      container.querySelectorAll('table thead th')[COUNT_COLUMN_INDEX].getAttribute('aria-sort')
    ).toBe('ascending');
  });
});

describe('Species (analytics page) — loading errors', () => {
  const originalFetch = globalThis.fetch;
  const summary: SpeciesSummary[] = [
    {
      common_name: 'Blue Jay',
      scientific_name: 'Cyanocitta cristata',
      count: 5,
      avg_confidence: 0.8,
      max_confidence: 0.9,
      first_heard: '2026-04-01',
      last_heard: '2026-04-20',
    },
  ];

  beforeEach(() => {
    window.localStorage.clear();
  });

  afterEach(() => {
    cleanup();
    globalThis.fetch = originalFetch;
    window.localStorage.clear();
  });

  it.each([
    ['HTTP error', () => Promise.resolve(new Response('', { status: 500 }))],
    ['network error', () => Promise.reject(new TypeError('Failed to fetch'))],
    ['invalid JSON', () => Promise.resolve(new Response('not JSON'))],
  ])(
    'shows an accessible error and retry instead of empty results for %s',
    async (_name, response) => {
      globalThis.fetch = vi.fn(response);
      const view = speciesTest.render({});

      expect(await view.findByRole('alert')).toHaveTextContent(t('analytics.errors.loadFailed'));
      expect(view.getByRole('button', { name: t('common.retry') })).toBeEnabled();
      expect(view.queryByText(t('analytics.species.noSpeciesFound'))).not.toBeInTheDocument();

      await fireEvent.click(
        view.getByRole('button', { name: t('analytics.species.switchToList') })
      );
      expect(view.queryByRole('table')).not.toBeInTheDocument();
      expect(view.queryByText(t('analytics.species.noSpeciesFound'))).not.toBeInTheDocument();
    }
  );

  it.each([
    ['species', summary, 'Blue Jay'],
    ['an empty result', [], t('analytics.species.noSpeciesFound')],
  ])(
    'clears the error while retrying and can recover with %s',
    async (_name, result, expectedText) => {
      const retry = deferred<Response>();
      const fetchMock = vi
        .fn<typeof fetch>()
        .mockResolvedValueOnce(new Response('', { status: 500 }))
        .mockReturnValueOnce(retry.promise);
      globalThis.fetch = fetchMock;
      const view = speciesTest.render({});
      await view.findByRole('alert');

      await fireEvent.click(view.getByRole('button', { name: t('common.retry') }));
      expect(fetchMock).toHaveBeenCalledTimes(2);
      expect(fetchMock.mock.calls[1]).toEqual(fetchMock.mock.calls[0]);
      expect(view.queryByRole('alert')).not.toBeInTheDocument();
      expect(view.queryByRole('button', { name: t('common.retry') })).not.toBeInTheDocument();
      expect(view.queryByText(t('analytics.species.noSpeciesFound'))).not.toBeInTheDocument();
      expect(view.getByRole('status')).toHaveTextContent(t('common.loading'));

      retry.resolve(Response.json(result));
      await waitFor(() => expect(view.queryByText(t('common.loading'))).not.toBeInTheDocument());
      expect(view.queryByRole('alert')).not.toBeInTheDocument();
      expect(view.getAllByText(expectedText).length).toBeGreaterThan(0);
    }
  );

  it('shows the normal empty state for a successful empty response', async () => {
    globalThis.fetch = vi.fn<typeof fetch>().mockResolvedValue(Response.json([]));
    const view = speciesTest.render({});
    expect(await view.findByText(t('analytics.species.noSpeciesFound'))).toBeInTheDocument();
    expect(view.queryByRole('alert')).not.toBeInTheDocument();
    expect(view.queryByRole('button', { name: t('common.retry') })).not.toBeInTheDocument();
  });

  it('ignores an older request failing after a newer request succeeds', async () => {
    const older = deferred<Response>();
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockReturnValueOnce(older.promise)
      .mockResolvedValueOnce(Response.json(summary));
    globalThis.fetch = fetchMock;
    const view = speciesTest.render({});
    const form = view.container.querySelector('form');
    if (!form) throw new Error('Species filter form not found');

    // Submitting the form models a filter update while the first request is pending.
    await fireEvent.submit(form);
    await view.findAllByText('Blue Jay');
    older.reject(new TypeError('Failed to fetch'));
    await tick();

    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(view.queryByRole('alert')).not.toBeInTheDocument();
    expect(view.queryByText(t('analytics.species.noSpeciesFound'))).not.toBeInTheDocument();
    expect(view.getAllByText('Blue Jay').length).toBeGreaterThan(0);
  });
});
