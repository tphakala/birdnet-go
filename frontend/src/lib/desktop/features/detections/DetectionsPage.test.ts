import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import type { PaginatedDetectionResponse } from '$lib/types/detection.types';
import DetectionsPage from './DetectionsPage.svelte';

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
  vi.clearAllMocks();
  localStorage.clear();
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
});
