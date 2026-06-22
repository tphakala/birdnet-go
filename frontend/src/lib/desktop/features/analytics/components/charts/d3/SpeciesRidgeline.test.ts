import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup } from '@testing-library/svelte';
import SpeciesRidgeline from './SpeciesRidgeline.svelte';
import type { RidgelineSeries } from './utils/ridgeline';

// jsdom has no layout engine; assert on element counts/attributes only.

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  cleanup();
});

/** Builds a 24-length hourly density array from (hour, value) pairs. */
function hourly(pairs: [number, number][]): number[] {
  const byHour = new Map(pairs);
  return Array.from({ length: 24 }, (_, i) => byHour.get(i) ?? 0);
}

const sample: RidgelineSeries[] = [
  {
    scientificName: 'Turdus merula',
    commonName: 'Eurasian Blackbird',
    density: hourly([
      [6, 0.6],
      [18, 0.4],
    ]),
    total: 40,
  },
  {
    scientificName: 'Erithacus rubecula',
    commonName: 'European Robin',
    density: hourly([[12, 1]]),
    total: 12,
  },
];

describe('SpeciesRidgeline', () => {
  it('renders without throwing for an empty series', () => {
    expect(() => render(SpeciesRidgeline, { props: { series: [] } })).not.toThrow();
  });

  it('renders one ridge area path per species', async () => {
    const { container } = render(SpeciesRidgeline, { props: { series: sample, width: 800 } });
    await Promise.resolve();
    expect(container.querySelectorAll('path.ridge-area')).toHaveLength(2);
  });

  it('renders one row label per species', async () => {
    const { container } = render(SpeciesRidgeline, { props: { series: sample, width: 800 } });
    await Promise.resolve();
    expect(container.querySelectorAll('text.ridge-label')).toHaveLength(2);
  });

  it('keys each ridge by the stable scientific name', async () => {
    const { container } = render(SpeciesRidgeline, { props: { series: sample, width: 800 } });
    await Promise.resolve();
    expect(container.querySelector('.ridge[data-species="Turdus merula"]')).toBeTruthy();
    expect(container.querySelector('.ridge[data-species="Erithacus rubecula"]')).toBeTruthy();
  });

  it('renders an x axis group', async () => {
    const { container } = render(SpeciesRidgeline, { props: { series: sample } });
    await Promise.resolve();
    expect(container.querySelector('.x-axis')).toBeTruthy();
  });

  it('sets an accessible label on the chart', () => {
    const { container } = render(SpeciesRidgeline, {
      props: { series: sample, ariaLabelKey: 'my.aria.key' },
    });
    // t is mocked to return the key, so the aria-label is the key itself.
    expect(container.querySelector('[aria-label="my.aria.key"]')).toBeTruthy();
  });

  it('renders a screen-reader summary when there is data', async () => {
    const { getByTestId } = render(SpeciesRidgeline, { props: { series: sample } });
    await Promise.resolve();
    expect(getByTestId('ridgeline-summary')).toBeTruthy();
  });

  it('renders the optional note caption when a noteKey is given', async () => {
    const { container } = render(SpeciesRidgeline, {
      props: { series: sample, noteKey: 'my.note.key' },
    });
    await Promise.resolve();
    expect(container.querySelector('.ridgeline-note')).toBeTruthy();
  });

  it('omits the note caption when the series is empty', () => {
    const { container } = render(SpeciesRidgeline, {
      props: { series: [], noteKey: 'my.note.key' },
    });
    expect(container.querySelector('.ridgeline-note')).toBeNull();
  });
});
