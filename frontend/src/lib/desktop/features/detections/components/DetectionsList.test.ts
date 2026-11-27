import { describe, it, expect, vi } from 'vitest';
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

// Fresh callbacks and the resolved sort and page size the page always passes
function baseProps() {
  return {
    sortBy: 'date_desc' as const,
    numResults: 25,
    onPageChange: vi.fn(),
    onDetailsClick: vi.fn(),
    onRefresh: vi.fn(),
    onNumResultsChange: vi.fn(),
    onSortChange: vi.fn(),
  };
}

describe('DetectionsList accessibility', () => {
  it('names the results-per-page dropdown independently of the selected page size', () => {
    render(DetectionsList, { props: { ...baseProps(), data: emptyData } });

    const trigger = screen.getByRole('combobox', { name: 'detections.aria.resultsPerPage' });
    expect(trigger).toHaveTextContent('25');
  });
});

describe('DetectionsList sort indicator and page size', () => {
  it('shows the sort and page size resolved by the parent once data arrives', async () => {
    const props = baseProps();
    const { rerender } = render(DetectionsList, { props: { ...props, data: null, loading: true } });

    const trigger = screen.getByRole('combobox', { name: 'detections.aria.resultsPerPage' });
    expect(trigger).toHaveTextContent('25');

    // A saved preference restored by the parent, as on returning to the page
    await rerender({
      ...props,
      data: { ...dataWithRow, numResults: 50, itemsPerPage: 50 },
      loading: false,
      sortBy: 'confidence_asc',
      numResults: 50,
    });

    expect(
      screen.getByRole('columnheader', { name: 'detections.headers.confidence' })
    ).toHaveAttribute('aria-sort', 'ascending');
    expect(
      screen.getByRole('columnheader', { name: 'detections.headers.dateTime' })
    ).not.toHaveAttribute('aria-sort');
    expect(trigger).toHaveTextContent('50');
  });

  it('reports a header click and shows the sort the parent passes back', async () => {
    const props = baseProps();
    const { rerender } = render(DetectionsList, {
      props: { ...props, data: dataWithRow, sortBy: 'species_asc' },
    });
    const speciesHeader = screen.getByRole('columnheader', {
      name: 'detections.headers.species',
    });
    expect(speciesHeader).toHaveAttribute('aria-sort', 'ascending');

    await fireEvent.click(screen.getByTestId('sort-species'));
    expect(props.onSortChange).toHaveBeenLastCalledWith('species_desc');

    await rerender({ ...props, data: dataWithRow, sortBy: 'species_desc' });
    expect(speciesHeader).toHaveAttribute('aria-sort', 'descending');

    // A second click on the active descending column asks for ascending
    await fireEvent.click(screen.getByTestId('sort-species'));
    expect(props.onSortChange).toHaveBeenLastCalledWith('species_asc');

    await fireEvent.click(screen.getByTestId('sort-dateTime'));
    expect(props.onSortChange).toHaveBeenLastCalledWith('date_desc');
  });

  it('asks for ascending on a first click on Species or Confidence', async () => {
    const props = baseProps();
    render(DetectionsList, { props: { ...props, data: dataWithRow } });

    await fireEvent.click(screen.getByTestId('sort-species'));
    expect(props.onSortChange).toHaveBeenLastCalledWith('species_asc');

    await fireEvent.click(screen.getByTestId('sort-confidence'));
    expect(props.onSortChange).toHaveBeenLastCalledWith('confidence_asc');
  });

  it('keeps the status indicator ascending since status sorts one way only', async () => {
    const props = baseProps();
    render(DetectionsList, { props: { ...props, data: dataWithRow, sortBy: 'status' } });
    const statusHeader = screen.getByRole('columnheader', { name: 'detections.headers.status' });
    expect(statusHeader).toHaveAttribute('aria-sort', 'ascending');

    await fireEvent.click(screen.getByTestId('sort-status'));
    expect(props.onSortChange).toHaveBeenLastCalledWith('status');
    expect(statusHeader).toHaveAttribute('aria-sort', 'ascending');
  });

  it('reports a page-size pick and follows the page size the parent passes after it', async () => {
    const props = baseProps();
    const { rerender } = render(DetectionsList, { props: { ...props, data: dataWithRow } });

    const trigger = screen.getByRole('combobox', { name: 'detections.aria.resultsPerPage' });
    await fireEvent.click(trigger);
    await fireEvent.click(screen.getByRole('option', { name: '50' }));
    expect(props.onNumResultsChange).toHaveBeenCalledWith(50);

    // The select shows the parent's value, not the pick it made itself
    await rerender({ ...props, data: dataWithRow, numResults: 10 });
    expect(trigger).toHaveTextContent(/^\s*10\s*$/);
  });
});
