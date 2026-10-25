import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { cleanup, fireEvent, waitFor } from '@testing-library/svelte';
import { createComponentTestFactory } from '../../../../../test/render-helpers';
import { setBasePath, resetBasePath } from '$lib/utils/urlHelpers';
import Species from './Species.svelte';

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
  // `th button` only exists on sortable headers, so it still lines up with
  // SORTABLE_COLUMNS. The non-sortable "Links" <th> (external reference-site
  // icons) sits right after the Species header in the full `th` list, shifting
  // every later plain-`th` index by one.
  const COUNT_BUTTON_INDEX = 1;
  const COUNT_TH_INDEX = 2;
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
    // Scoped to .sp-species-name (not the more generic .font-bold) so the bold
    // "W" Wikipedia-link badge in the row's separate links cell isn't picked up too.
    return Array.from(container.querySelectorAll('table tbody tr td .sp-species-name')).map(el =>
      el.textContent.trim()
    );
  }

  it('defaults to sorting by detection count descending', async () => {
    const { container } = await renderListView();

    expect(rowNames(container)).toEqual(['Blue Jay', 'Zebra Finch', 'American Robin']);
    expect(
      container.querySelectorAll('table thead th')[COUNT_TH_INDEX].getAttribute('aria-sort')
    ).toBe('descending');
  });

  it('toggles detection count to ascending on first header click and back on second', async () => {
    const { container } = await renderListView();
    const countButton = container.querySelectorAll('table thead th button')[COUNT_BUTTON_INDEX];

    await fireEvent.click(countButton);
    expect(rowNames(container)).toEqual(['American Robin', 'Zebra Finch', 'Blue Jay']);
    expect(
      container.querySelectorAll('table thead th')[COUNT_TH_INDEX].getAttribute('aria-sort')
    ).toBe('ascending');

    await fireEvent.click(countButton);
    expect(rowNames(container)).toEqual(['Blue Jay', 'Zebra Finch', 'American Robin']);
    expect(
      container.querySelectorAll('table thead th')[COUNT_TH_INDEX].getAttribute('aria-sort')
    ).toBe('descending');
  });

  it('persists the chosen sort order to localStorage', async () => {
    const { container } = await renderListView();

    await fireEvent.click(container.querySelectorAll('table thead th button')[COUNT_BUTTON_INDEX]);
    expect(window.localStorage.getItem(SORT_STORAGE_KEY)).toBe(COUNT_ASC_STORED);
  });

  it('restores a persisted sort order on a fresh render', async () => {
    window.localStorage.setItem(SORT_STORAGE_KEY, COUNT_ASC_STORED);

    const { container } = await renderListView();

    expect(rowNames(container)).toEqual(['American Robin', 'Zebra Finch', 'Blue Jay']);
    expect(
      container.querySelectorAll('table thead th')[COUNT_TH_INDEX].getAttribute('aria-sort')
    ).toBe('ascending');
  });
});
