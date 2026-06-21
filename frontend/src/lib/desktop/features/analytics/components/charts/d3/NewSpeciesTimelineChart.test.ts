import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup } from '@testing-library/svelte';
import NewSpeciesTimelineChart from './NewSpeciesTimelineChart.svelte';

// jsdom has no layout engine; assert on element counts/attributes only.

beforeEach(() => {
  vi.clearAllMocks();
});

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

  // Localization: the y band scale is keyed on the scientific name (stable across
  // locales) while the axis renders the display name via tickFormat. Two species
  // that share a common name must still get distinct rows; a naive scale keyed on
  // the common name would dedupe the band and collapse them onto one row.
  it('keeps two species sharing a common name on distinct rows', async () => {
    const sameCommonName = [
      { commonName: 'Owl', scientificName: 'Strix aluco', firstHeard: new Date(2024, 2, 1) },
      { commonName: 'Owl', scientificName: 'Bubo bubo', firstHeard: new Date(2024, 2, 5) },
    ];
    const { container } = render(NewSpeciesTimelineChart, { props: { data: sameCommonName } });
    await Promise.resolve();
    const markers = container.querySelectorAll('rect.timeline-marker');
    expect(markers).toHaveLength(2);
    const ys = Array.from(markers, m => m.getAttribute('y'));
    // Distinct band positions: a common-name-keyed scale would place both at the
    // same y (the regression this decoupling prevents).
    expect(ys[0]).not.toBe(ys[1]);
  });

  it('renders display names, not the scientific-name band keys, on the y axis', async () => {
    const { container } = render(NewSpeciesTimelineChart, { props: { data: sampleData } });
    await Promise.resolve();
    const tickText = Array.from(
      container.querySelectorAll('.y-axis .tick text'),
      node => node.textContent
    );
    // No dictionary is loaded in the test, so localizeSpeciesName falls back to the
    // common name. The tickFormat must map the scientific-name keys back to that
    // display name rather than leaving the raw scientific key on the axis.
    expect(tickText).toContain('Robin');
    expect(tickText).not.toContain('Erithacus rubecula');
  });
});
