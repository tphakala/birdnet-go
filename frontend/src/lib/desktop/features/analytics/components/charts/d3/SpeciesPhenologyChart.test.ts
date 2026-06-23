import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup } from '@testing-library/svelte';
import SpeciesPhenologyChart from './SpeciesPhenologyChart.svelte';
import type { PhenologyData } from './utils/phenology';

// jsdom has no layout engine; assert on element counts/attributes only.

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  cleanup();
});

// Three species with overlapping residency spans, in arrival order (as the server returns them).
const sample: PhenologyData = {
  rows: [
    {
      scientificName: 'Apus apus',
      commonName: 'Common Swift',
      firstSeen: '2026-03-01',
      lastSeen: '2026-03-20',
      count: 40,
    },
    {
      scientificName: 'Hirundo rustica',
      commonName: 'Barn Swallow',
      firstSeen: '2026-03-05',
      lastSeen: '2026-03-28',
      count: 25,
    },
    {
      scientificName: 'Delichon urbicum',
      commonName: 'House Martin',
      firstSeen: '2026-03-10',
      lastSeen: '2026-03-25',
      count: 12,
    },
  ],
};

const empty: PhenologyData = { rows: [] };

describe('SpeciesPhenologyChart', () => {
  it('renders without throwing for empty data', () => {
    expect(() => render(SpeciesPhenologyChart, { props: { data: empty } })).not.toThrow();
  });

  it('renders the axes and one residency bar per species', async () => {
    const { container } = render(SpeciesPhenologyChart, { props: { data: sample, width: 800 } });
    await Promise.resolve();
    expect(container.querySelector('.x-axis')).toBeTruthy();
    expect(container.querySelector('.y-axis')).toBeTruthy();
    expect(container.querySelectorAll('.phenology-bars rect')).toHaveLength(3);
  });

  it('exposes the full common name as a y-axis tick <title> (recoverable when truncated)', async () => {
    // A name longer than LABEL_MAX_CHARS (20) is truncated on the axis; the full name must still be
    // reachable via a native <title> for touch long-press and assistive tech.
    const longName: PhenologyData = {
      rows: [
        {
          scientificName: 'Nycticorax nycticorax',
          commonName: 'Black-crowned Night Heron',
          firstSeen: '2026-03-01',
          lastSeen: '2026-03-20',
          count: 9,
        },
        {
          scientificName: 'Setophaga coronata',
          commonName: 'Yellow-rumped Warbler',
          firstSeen: '2026-03-05',
          lastSeen: '2026-03-28',
          count: 7,
        },
      ],
    };
    const { container } = render(SpeciesPhenologyChart, { props: { data: longName, width: 800 } });
    await Promise.resolve();
    const titles = Array.from(container.querySelectorAll('.y-axis .tick title')).map(
      n => n.textContent
    );
    expect(titles).toContain('Black-crowned Night Heron');
    expect(titles).toContain('Yellow-rumped Warbler');
  });

  it('renders single-day species (collapsed x-domain) with a visible, non-NaN bar', async () => {
    // Every species seen on exactly one (identical) day: first === last for all rows. The +1-day
    // inclusive end keeps the x-domain positive-width, so bars must render with finite x/width and a
    // minimum width rather than a zero-width (invisible) or NaN rect.
    const singleDay: PhenologyData = {
      rows: [
        {
          scientificName: 'Apus apus',
          commonName: 'Common Swift',
          firstSeen: '2026-03-01',
          lastSeen: '2026-03-01',
          count: 5,
        },
        {
          scientificName: 'Hirundo rustica',
          commonName: 'Barn Swallow',
          firstSeen: '2026-03-01',
          lastSeen: '2026-03-01',
          count: 3,
        },
      ],
    };
    const { container } = render(SpeciesPhenologyChart, { props: { data: singleDay, width: 800 } });
    await Promise.resolve();
    const rects = container.querySelectorAll('.phenology-bars rect');
    expect(rects).toHaveLength(2);
    for (const rect of rects) {
      const x = rect.getAttribute('x') ?? '';
      const width = rect.getAttribute('width') ?? '';
      expect(x).not.toContain('NaN');
      expect(width).not.toContain('NaN');
      expect(Number(width)).toBeGreaterThan(0);
    }
  });

  it('does not draw bars for empty data', async () => {
    const { container } = render(SpeciesPhenologyChart, { props: { data: empty, width: 800 } });
    await Promise.resolve();
    expect(container.querySelectorAll('.phenology-bars rect')).toHaveLength(0);
  });

  it('sets an accessible label on the chart container', () => {
    const { container } = render(SpeciesPhenologyChart, {
      props: { data: sample, ariaLabel: 'Species phenology' },
    });
    expect(container.querySelector('[aria-label="Species phenology"]')).toBeTruthy();
  });

  it('renders a screen-reader summary when there is data', async () => {
    const { getByTestId } = render(SpeciesPhenologyChart, { props: { data: sample } });
    await Promise.resolve();
    expect(getByTestId('phenology-summary')).toBeTruthy();
  });
});
