import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/svelte';
import type { PaginatedDetectionResponse } from '$lib/types/detection.types';
import DetectionsPage from './DetectionsPage.svelte';
import { deferred } from '../../../../test/async-helpers';

// Keys mirror the preference keys in DetectionsPage.svelte
const SORT_BY_KEY = 'birdnet-detections-sort-by';
const RESULTS_PER_PAGE_KEY = 'birdnet-detections-results-per-page';

const mocks = vi.hoisted(() => ({
  fetchWithCSRF: vi.fn(),
}));

vi.mock('$lib/utils/api', async importOriginal => ({
  ...(await importOriginal<typeof import('$lib/utils/api')>()),
  fetchWithCSRF: mocks.fetchWithCSRF,
}));

// The test setup replaces window.location with a plain object, so a history write does
// not reach it; write the query string the way the browser would after pushState.
vi.mock('$lib/stores/navigation.svelte', () => ({
  navigation: {
    writeUrl: (url: string) => {
      window.location.search = new URL(url, window.location.origin).search;
    },
    navigate: vi.fn(),
  },
}));

const oneRow: PaginatedDetectionResponse = {
  data: [
    {
      id: 1,
      date: '2026-01-01',
      time: '08:15:00',
      beginTime: '2026-01-01T08:15:00',
      endTime: '2026-01-01T08:15:03',
      speciesCode: 'amerob',
      commonName: 'American Robin',
      scientificName: 'Turdus migratorius',
      confidence: 0.92,
      verified: 'unverified',
      locked: false,
    },
  ],
  total: 1,
  limit: 50,
  offset: 0,
  current_page: 1,
  total_pages: 1,
};

afterEach(() => {
  vi.useRealTimers();
  vi.clearAllMocks();
  localStorage.clear();
  window.location.search = '';
});

describe('DetectionsPage saved preferences', () => {
  it('shows the saved sort and page size when the page opens without URL parameters', async () => {
    localStorage.setItem(SORT_BY_KEY, 'confidence_asc');
    localStorage.setItem(RESULTS_PER_PAGE_KEY, '50');
    mocks.fetchWithCSRF.mockResolvedValue(oneRow);

    render(DetectionsPage);

    const confidenceHeader = await screen.findByRole('columnheader', {
      name: 'detections.headers.confidence',
    });
    expect(mocks.fetchWithCSRF).toHaveBeenCalledWith(
      expect.stringContaining('sortBy=confidence_asc')
    );
    expect(confidenceHeader).toHaveAttribute('aria-sort', 'ascending');
    expect(
      screen.getByRole('columnheader', { name: 'detections.headers.dateTime' })
    ).not.toHaveAttribute('aria-sort');
    expect(
      screen.getByRole('combobox', { name: 'detections.aria.resultsPerPage' })
    ).toHaveTextContent('50');
  });

  it('shows Date and Time descending and 25 rows when nothing is saved and the URL is empty', async () => {
    mocks.fetchWithCSRF.mockResolvedValue(oneRow);
    render(DetectionsPage);

    const dateTimeHeader = await screen.findByRole('columnheader', {
      name: 'detections.headers.dateTime',
    });
    expect(dateTimeHeader).toHaveAttribute('aria-sort', 'descending');
    expect(
      screen.getByRole('combobox', { name: 'detections.aria.resultsPerPage' })
    ).toHaveTextContent('25');
  });

  it('prefers the sort and page size in the URL over the saved ones', async () => {
    localStorage.setItem(SORT_BY_KEY, 'confidence_asc');
    localStorage.setItem(RESULTS_PER_PAGE_KEY, '50');
    window.location.search = '?sortBy=species_desc&numResults=10';
    mocks.fetchWithCSRF.mockResolvedValue(oneRow);
    render(DetectionsPage);

    const speciesHeader = await screen.findByRole('columnheader', {
      name: 'detections.headers.species',
    });
    expect(speciesHeader).toHaveAttribute('aria-sort', 'descending');
    expect(
      screen.getByRole('combobox', { name: 'detections.aria.resultsPerPage' })
    ).toHaveTextContent(/^\s*10\s*$/);
  });

  it('follows the sort and page size of the history entry on back or forward', async () => {
    window.location.search = '?sortBy=species_asc&numResults=25';
    mocks.fetchWithCSRF.mockResolvedValue(oneRow);
    render(DetectionsPage);
    const speciesHeader = await screen.findByRole('columnheader', {
      name: 'detections.headers.species',
    });
    expect(speciesHeader).toHaveAttribute('aria-sort', 'ascending');

    window.location.search = '?sortBy=confidence_desc&numResults=50';
    window.dispatchEvent(new PopStateEvent('popstate'));

    const confidenceHeader = screen.getByRole('columnheader', {
      name: 'detections.headers.confidence',
    });
    await waitFor(() => expect(confidenceHeader).toHaveAttribute('aria-sort', 'descending'));
    expect(speciesHeader).not.toHaveAttribute('aria-sort');
    expect(
      screen.getByRole('combobox', { name: 'detections.aria.resultsPerPage' })
    ).toHaveTextContent('50');
  });

  it('keeps a picked page size when a sort click fetches before the debounce fires', async () => {
    window.location.search = '?numResults=25';
    mocks.fetchWithCSRF.mockResolvedValue(oneRow);
    render(DetectionsPage);
    await screen.findByRole('columnheader', { name: 'detections.headers.species' });

    // Hold the page-size debounce so the sort click lands inside it
    vi.useFakeTimers();
    const trigger = screen.getByRole('combobox', { name: 'detections.aria.resultsPerPage' });
    await fireEvent.click(trigger);
    await fireEvent.click(screen.getByRole('option', { name: '50' }));
    await fireEvent.click(screen.getByTestId('sort-species'));

    expect(mocks.fetchWithCSRF).toHaveBeenLastCalledWith(expect.stringContaining('numResults=50'));
    expect(trigger).toHaveTextContent('50');
  });

  it('moves to the next page at the current size', async () => {
    window.location.search = '?numResults=25';
    mocks.fetchWithCSRF.mockResolvedValue({ ...oneRow, total: 200, limit: 25, total_pages: 8 });
    render(DetectionsPage);
    await screen.findByRole('columnheader', { name: 'detections.headers.species' });

    await fireEvent.click(screen.getByRole('button', { name: /goToNextPage/i }));

    const lastUrl = String(mocks.fetchWithCSRF.mock.lastCall?.[0]);
    expect(lastUrl).toContain('numResults=25');
    expect(lastUrl).toContain('offset=25');
  });

  it('lands on a page of the picked size when the page changes before the debounce fires', async () => {
    window.location.search = '?numResults=25&offset=125';
    mocks.fetchWithCSRF.mockResolvedValue({
      ...oneRow,
      total: 300,
      limit: 25,
      offset: 125,
      current_page: 6,
      total_pages: 12,
    });
    render(DetectionsPage);
    await screen.findByRole('columnheader', { name: 'detections.headers.species' });

    // Page 6 of 25 rows; pick 100 and press Next before the debounced fetch runs
    vi.useFakeTimers();
    await fireEvent.click(screen.getByRole('combobox', { name: 'detections.aria.resultsPerPage' }));
    await fireEvent.click(screen.getByRole('option', { name: '100' }));
    await fireEvent.click(screen.getByRole('button', { name: /goToNextPage/i }));

    // Row 151 was asked for, which is on the second page of 100
    const lastUrl = String(mocks.fetchWithCSRF.mock.lastCall?.[0]);
    expect(lastUrl).toContain('numResults=100');
    expect(lastUrl).toContain('offset=100');
  });

  it('shows the rows of the latest request when an older response arrives last', async () => {
    const rowsFor = (commonName: string, scientificName: string): PaginatedDetectionResponse => ({
      ...oneRow,
      data: [{ ...oneRow.data[0], commonName, scientificName }],
    });
    mocks.fetchWithCSRF.mockResolvedValueOnce(oneRow);
    render(DetectionsPage);
    await screen.findByRole('columnheader', { name: 'detections.headers.species' });

    const older = deferred<PaginatedDetectionResponse>();
    const newer = deferred<PaginatedDetectionResponse>();
    mocks.fetchWithCSRF.mockReturnValueOnce(older.promise).mockReturnValueOnce(newer.promise);
    await fireEvent.click(screen.getByTestId('sort-species'));
    await fireEvent.click(screen.getByTestId('sort-confidence'));

    newer.resolve(rowsFor('Newer Bird', 'Testus newerus'));
    await screen.findAllByText('Newer Bird');
    older.resolve(rowsFor('Older Bird', 'Testus olderus'));
    await older.promise;
    await new Promise(resolve => setTimeout(resolve, 0));

    expect(screen.getAllByText('Newer Bird').length).toBeGreaterThan(0);
    expect(screen.queryAllByText('Older Bird')).toHaveLength(0);
    expect(
      screen.getByRole('columnheader', { name: 'detections.headers.confidence' })
    ).toHaveAttribute('aria-sort', 'ascending');
  });

  it('drops a response for the old page size that arrives while a new size is debounced', async () => {
    mocks.fetchWithCSRF.mockResolvedValueOnce(oneRow);
    render(DetectionsPage);
    await screen.findByRole('columnheader', { name: 'detections.headers.species' });

    // A sort fetch is still pending when the page size changes
    const pending = deferred<PaginatedDetectionResponse>();
    mocks.fetchWithCSRF.mockReturnValueOnce(pending.promise);
    await fireEvent.click(screen.getByTestId('sort-species'));

    vi.useFakeTimers();
    await fireEvent.click(screen.getByRole('combobox', { name: 'detections.aria.resultsPerPage' }));
    await fireEvent.click(screen.getByRole('option', { name: '50' }));

    pending.resolve({
      ...oneRow,
      data: [{ ...oneRow.data[0], commonName: 'Older Bird', scientificName: 'Testus olderus' }],
    });
    await pending.promise;
    await vi.advanceTimersByTimeAsync(0);

    expect(screen.queryAllByText('Older Bird')).toHaveLength(0);
  });
});
