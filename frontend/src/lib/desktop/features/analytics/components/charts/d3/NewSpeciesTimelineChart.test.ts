import { describe, it, expect, afterEach } from 'vitest';
import { render, cleanup } from '@testing-library/svelte';
import NewSpeciesTimelineChart from './NewSpeciesTimelineChart.svelte';

// jsdom has no layout engine; assert on element counts/attributes only.

afterEach(() => {
  cleanup();
});

describe('NewSpeciesTimelineChart', () => {
  const sampleData = [
    { commonName: 'Robin', scientificName: 'Erithacus rubecula', firstHeard: new Date(2024, 2, 1) },
    { commonName: 'Crow', scientificName: 'Corvus corone', firstHeard: new Date(2024, 2, 5) },
    {
      commonName: 'Wren',
      scientificName: 'Troglodytes troglodytes',
      firstHeard: new Date(2024, 2, 9),
    },
  ];

  it('renders without throwing for empty data', () => {
    expect(() => render(NewSpeciesTimelineChart, { props: { data: [] } })).not.toThrow();
  });

  it('renders one marker per species', async () => {
    const { container } = render(NewSpeciesTimelineChart, { props: { data: sampleData } });
    await Promise.resolve();
    const markers = container.querySelectorAll('rect.timeline-marker');
    expect(markers).toHaveLength(sampleData.length);
  });

  it('renders an x axis (time) group', async () => {
    const { container } = render(NewSpeciesTimelineChart, { props: { data: sampleData } });
    await Promise.resolve();
    expect(container.querySelector('.x-axis')).toBeTruthy();
  });

  it('respects an explicit dateRange without throwing', async () => {
    const { container } = render(NewSpeciesTimelineChart, {
      props: {
        data: sampleData,
        dateRange: [new Date(2024, 1, 25), new Date(2024, 2, 15)],
      },
    });
    await Promise.resolve();
    const markers = container.querySelectorAll('rect.timeline-marker');
    expect(markers).toHaveLength(sampleData.length);
  });

  it('sets an accessible label on the chart container', () => {
    const { container } = render(NewSpeciesTimelineChart, {
      props: { data: sampleData, ariaLabel: 'New species timeline' },
    });
    const labelled = container.querySelector('[aria-label="New species timeline"]');
    expect(labelled).toBeTruthy();
  });
});
