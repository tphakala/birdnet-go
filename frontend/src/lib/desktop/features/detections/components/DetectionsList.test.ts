import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/svelte';
import type { DetectionsListData } from '$lib/types/detection.types';
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

    const trigger = screen.getByRole('button', { name: 'detections.aria.resultsPerPage' });
    expect(trigger).toHaveTextContent('25');
  });
});
