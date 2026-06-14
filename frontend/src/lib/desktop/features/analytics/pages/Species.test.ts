import { describe, it, expect, afterEach, beforeEach, vi } from 'vitest';
import { cleanup, fireEvent, waitFor } from '@testing-library/svelte';
import { createComponentTestFactory } from '../../../../../test/render-helpers';
import { setBasePath, resetBasePath } from '$lib/utils/urlHelpers';
import { settingsActions } from '$lib/stores/settings';
import { initAuthContext } from '$lib/utils/auth';
import Species from './Species.svelte';

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
      '/api/v2/analytics/species/thumbnails': () => ({}),
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
      '/api/v2/analytics/species/thumbnails': () => ({}),
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

describe('Species (analytics page) — Manage view', () => {
  const originalFetch = globalThis.fetch;

  // View toggle order: grid(0), list(1), manage(2 — only when authenticated).
  const MANAGE_VIEW_TOGGLE_INDEX = 2;
  // Manage table body cell order: name(0), count(1), maxConf(2), lastSeen(3),
  // excluded(4), included(5), correct(6), range(7), confirmed(8), delete(9).
  const CORRECT_CELL_INDEX = 6;
  const SORT_STORAGE_KEY = 'analytics.species.sortOrder';

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
  ];

  function mockManageFetch(reviewStats: unknown) {
    globalThis.fetch = mockFetchSequence({
      '/api/v2/analytics/species/review-stats': () => reviewStats,
      '/api/v2/analytics/species/summary': () => summary,
      '/api/v2/analytics/species/thumbnails': () => ({}),
      '/api/v2/detections/included': () => ({ species: [] }),
      '/api/v2/detections/confirmed': () => ({ species: [] }),
      '/api/v2/detections/ignored': () => ({ species: [] }),
    });
  }

  beforeEach(() => {
    vi.clearAllMocks();
    initAuthContext(false); // security disabled => authenticated
    vi.spyOn(settingsActions, 'loadRangeFilterSpecies').mockResolvedValue({
      count: 0,
      species: [],
    });
    mockManageFetch([
      { scientificName: 'Turdus migratorius', total: 10, verified: 7, rejected: 3 },
    ]);
    window.localStorage.clear();
  });

  afterEach(() => {
    cleanup();
    resetBasePath();
    globalThis.fetch = originalFetch;
    window.localStorage.clear();
    initAuthContext(false);
    vi.restoreAllMocks();
  });

  const speciesTest = createComponentTestFactory(Species);

  async function renderManageView() {
    const { container } = speciesTest.render({});
    await waitFor(() => {
      if (container.querySelectorAll('.join button').length < 3)
        throw new Error('toggle not ready');
    });
    await fireEvent.click(container.querySelectorAll('.join button')[MANAGE_VIEW_TOGGLE_INDEX]);
    await waitFor(
      () => {
        if (!container.querySelector('table tbody tr'))
          throw new Error('manage table not yet rendered');
      },
      { timeout: 2000 }
    );
    return { container };
  }

  it('renders the Manage toggle only when authenticated', async () => {
    const { container } = speciesTest.render({});
    await waitFor(() => {
      if (container.querySelectorAll('.join button').length === 0)
        throw new Error('toggle not ready');
    });
    expect(container.querySelectorAll('.join button')).toHaveLength(3);
    cleanup();

    initAuthContext(true, false); // security enabled, access denied => not authenticated
    const { container: guest } = speciesTest.render({});
    await waitFor(() => {
      if (guest.querySelectorAll('.join button').length === 0) throw new Error('toggle not ready');
    });
    expect(guest.querySelectorAll('.join button')).toHaveLength(2);
  });

  it('hides average confidence and first detected columns', async () => {
    const { container } = await renderManageView();
    const headText = container.querySelector('table thead')?.textContent ?? '';
    expect(headText).not.toContain('avgConfidence');
    expect(headText).not.toContain('firstDetected');
    // Sanity: the shared maxConfidence/lastDetected columns remain.
    expect(headText).toContain('maxConfidence');
    expect(headText).toContain('lastDetected');
  });

  it('shows the correct rate as an integer percentage', async () => {
    // verified 7 of 10 reviewed -> round(7 / (7 + 3) * 100) = 70%.
    const { container } = await renderManageView();
    await waitFor(() => {
      const cell = container.querySelectorAll('table tbody tr td')[CORRECT_CELL_INDEX];
      expect(cell.textContent.trim()).toBe('70%');
    });
  });

  it('shows — in the correct column when a species has no reviews', async () => {
    mockManageFetch([{ scientificName: 'Turdus migratorius', total: 5, verified: 0, rejected: 0 }]);
    const { container } = await renderManageView();
    await waitFor(() => {
      const cell = container.querySelectorAll('table tbody tr td')[CORRECT_CELL_INDEX];
      expect(cell.textContent.trim()).toBe('—');
    });
  });

  it('makes the membership columns sortable and does not persist a Manage-only sort', async () => {
    const { container } = await renderManageView();
    // Manage sortable header buttons: name(0), count(1), maxConf(2), lastSeen(3),
    // excluded(4), included(5), correct(6), range(7), confirmed(8). The excluded,
    // included, and confirmed columns are now SortableHeaders (9 buttons total).
    const sortButtons = container.querySelectorAll('table thead th button');
    expect(sortButtons).toHaveLength(9);
    // fetchData persists the default grid/list sort on mount.
    const persistedBefore = window.localStorage.getItem(SORT_STORAGE_KEY);
    await fireEvent.click(sortButtons[4]); // excluded — a Manage-only column
    // Sorting by a Manage-only column must not change the grid/list persisted sort.
    expect(window.localStorage.getItem(SORT_STORAGE_KEY)).toBe(persistedBefore);
  });

  it('sorts by the Included column, grouping included species first', async () => {
    // Two rows: Turdus (summary) and Corvus (review-stats only). Only Corvus is on
    // the included list, so sorting Included (default desc) must put it first.
    globalThis.fetch = mockFetchSequence({
      '/api/v2/analytics/species/review-stats': () => [
        { scientificName: 'Turdus migratorius', total: 10, verified: 7, rejected: 3 },
        {
          scientificName: 'Corvus corax',
          commonName: 'Common Raven',
          total: 4,
          verified: 0,
          rejected: 4,
        },
      ],
      '/api/v2/analytics/species/summary': () => summary,
      '/api/v2/analytics/species/thumbnails': () => ({}),
      '/api/v2/detections/included': () => ({ species: ['Corvus corax'] }),
      '/api/v2/detections/confirmed': () => ({ species: [] }),
      '/api/v2/detections/ignored': () => ({ species: [] }),
    });
    const { container } = await renderManageView();
    await waitFor(() => {
      if (container.querySelectorAll('table tbody tr').length < 2)
        throw new Error('both rows not yet rendered');
    });
    // Included is the 6th sortable header (index 5).
    const sortButtons = container.querySelectorAll('table thead th button');
    await fireEvent.click(sortButtons[5]);
    await waitFor(() => {
      const rows = container.querySelectorAll('table tbody tr');
      expect(rows[0].textContent).toContain('Common Raven');
    });
  });

  it('surfaces a fully-rejected species that is absent from the period summary', async () => {
    // Corvus corax has detections but every one was rejected, so the summary
    // (which excludes false positives) omits it. review-stats still reports it and
    // now carries the common name, so Manage can render a deletable row for it.
    mockManageFetch([
      { scientificName: 'Turdus migratorius', total: 10, verified: 7, rejected: 3 },
      {
        scientificName: 'Corvus corax',
        commonName: 'Common Raven',
        total: 4,
        verified: 0,
        rejected: 4,
      },
    ]);
    const { container } = await renderManageView();
    await waitFor(() => {
      if (container.querySelectorAll('table tbody tr').length < 2)
        throw new Error('synthesized row not yet rendered');
    });
    const rows = Array.from(container.querySelectorAll('table tbody tr'));
    const ravenRow = rows.find(r => r.textContent.includes('Common Raven'));
    if (!ravenRow) throw new Error('Common Raven row not rendered');
    const cells = ravenRow.querySelectorAll('td');
    expect(cells[1].textContent.trim()).toBe('4'); // all-time detection count
    expect(cells[2].textContent.trim()).toBe('—'); // no max-confidence data survives
    expect(cells[3].textContent.trim()).toBe('—'); // no last-detected data survives
    expect(cells[CORRECT_CELL_INDEX].textContent.trim()).toBe('0%'); // 0 of 4 reviewed correct
  });

  it('checks the included toggle when the list stores the scientific-name alias', async () => {
    // The included list holds the scientific name; the row is American Robin /
    // Turdus migratorius, so the alias match must render the checkbox checked.
    globalThis.fetch = mockFetchSequence({
      '/api/v2/analytics/species/review-stats': () => [
        { scientificName: 'Turdus migratorius', total: 10, verified: 7, rejected: 3 },
      ],
      '/api/v2/analytics/species/summary': () => summary,
      '/api/v2/analytics/species/thumbnails': () => ({}),
      '/api/v2/detections/included': () => ({ species: ['Turdus migratorius'] }),
      '/api/v2/detections/confirmed': () => ({ species: [] }),
      '/api/v2/detections/ignored': () => ({ species: [] }),
    });
    const INCLUDED_CELL_INDEX = 5;
    const { container } = await renderManageView();
    await waitFor(() => {
      const cell = container.querySelectorAll('table tbody tr td')[INCLUDED_CELL_INDEX];
      const checkbox = cell.querySelector<HTMLInputElement>('input[type="checkbox"]');
      expect(checkbox?.checked).toBe(true);
    });
  });
});
