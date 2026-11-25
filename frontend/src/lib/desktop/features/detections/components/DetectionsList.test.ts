import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/svelte';
import type { Detection, DetectionsListData } from '$lib/types/detection.types';
import DetectionsList from './DetectionsList.svelte';

const emptyData: DetectionsListData = {
  notes: [],
  queryType: 'all',
  date: '2026-01-01',
  numResults: 25,
  offset: 0,
  totalResults: 0,
  itemsPerPage: 25,
  currentPage: 1,
  totalPages: 1,
  showingFrom: 0,
  showingTo: 0,
};

const sampleDetection: Detection = {
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
};

const dataWithRow: DetectionsListData = {
  ...emptyData,
  notes: [sampleDetection],
  totalResults: 1,
  showingFrom: 1,
  showingTo: 1,
};

afterEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  window.location.search = '';
});

describe('DetectionsList accessibility', () => {
  it('names the results-per-page dropdown independently of the selected page size', () => {
    render(DetectionsList, {
      props: {
        data: emptyData,
        onPageChange: vi.fn(),
        onDetailsClick: vi.fn(),
        onRefresh: vi.fn(),
        onNumResultsChange: vi.fn(),
        onSortChange: vi.fn(),
      },
    });

    const trigger = screen.getByRole('combobox', { name: 'detections.aria.resultsPerPage' });
    expect(trigger).toHaveTextContent('25');
  });
});

describe('DetectionsList sort indicator and page-size sync', () => {
  it('syncs sort indicator and results-per-page dropdown when resolved sortBy and data arrive after initial loading', async () => {
    const { rerender } = render(DetectionsList, {
      props: {
        data: null,
        loading: true,
        onPageChange: vi.fn(),
        onDetailsClick: vi.fn(),
        onRefresh: vi.fn(),
        onNumResultsChange: vi.fn(),
        onSortChange: vi.fn(),
      },
    });

    const trigger = screen.getByRole('combobox', { name: 'detections.aria.resultsPerPage' });
    expect(trigger).toHaveTextContent('25');

    await rerender({
      data: { ...dataWithRow, numResults: 50, itemsPerPage: 50 },
      loading: false,
      sortBy: 'confidence_asc',
      onPageChange: vi.fn(),
      onDetailsClick: vi.fn(),
      onRefresh: vi.fn(),
      onNumResultsChange: vi.fn(),
      onSortChange: vi.fn(),
    });

    const confidenceHeader = screen.getByRole('columnheader', {
      name: 'detections.headers.confidence',
    });
    const dateTimeHeader = screen.getByRole('columnheader', {
      name: 'detections.headers.dateTime',
    });
    expect(confidenceHeader).toHaveAttribute('aria-sort', 'ascending');
    expect(dateTimeHeader).not.toHaveAttribute('aria-sort');
    expect(trigger).toHaveTextContent('50');
  });

  it('toggles restored sort column direction on click and resets on subsequent sortBy prop updates', async () => {
    const onSortChange = vi.fn();
    const { rerender } = render(DetectionsList, {
      props: {
        data: dataWithRow,
        sortBy: 'species_asc',
        onPageChange: vi.fn(),
        onDetailsClick: vi.fn(),
        onRefresh: vi.fn(),
        onNumResultsChange: vi.fn(),
        onSortChange,
      },
    });

    const speciesHeader = screen.getByRole('columnheader', {
      name: 'detections.headers.species',
    });
    expect(speciesHeader).toHaveAttribute('aria-sort', 'ascending');

    await fireEvent.click(screen.getByTestId('sort-species'));
    expect(onSortChange).toHaveBeenCalledWith('species_desc');
    expect(speciesHeader).toHaveAttribute('aria-sort', 'descending');

    await rerender({
      data: dataWithRow,
      sortBy: 'date_desc',
      onPageChange: vi.fn(),
      onDetailsClick: vi.fn(),
      onRefresh: vi.fn(),
      onNumResultsChange: vi.fn(),
      onSortChange,
    });

    const dateTimeHeader = screen.getByRole('columnheader', {
      name: 'detections.headers.dateTime',
    });
    expect(dateTimeHeader).toHaveAttribute('aria-sort', 'descending');
    expect(speciesHeader).not.toHaveAttribute('aria-sort');
  });

  it('falls back to URL sortBy when prop is omitted and prefers sortBy prop over conflicting URL param', async () => {
    window.location.search = '?sortBy=confidence_desc';

    const { rerender } = render(DetectionsList, {
      props: {
        data: dataWithRow,
        onPageChange: vi.fn(),
        onDetailsClick: vi.fn(),
        onRefresh: vi.fn(),
        onNumResultsChange: vi.fn(),
        onSortChange: vi.fn(),
      },
    });

    const confidenceHeader = screen.getByRole('columnheader', {
      name: 'detections.headers.confidence',
    });
    expect(confidenceHeader).toHaveAttribute('aria-sort', 'descending');

    await rerender({
      data: dataWithRow,
      sortBy: 'species_asc',
      onPageChange: vi.fn(),
      onDetailsClick: vi.fn(),
      onRefresh: vi.fn(),
      onNumResultsChange: vi.fn(),
      onSortChange: vi.fn(),
    });

    const speciesHeader = screen.getByRole('columnheader', {
      name: 'detections.headers.species',
    });
    expect(speciesHeader).toHaveAttribute('aria-sort', 'ascending');
    expect(confidenceHeader).not.toHaveAttribute('aria-sort');
  });
});
