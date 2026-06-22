import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup } from '@testing-library/svelte';
import AcousticSuccessionChart from './AcousticSuccessionChart.svelte';
import type { SuccessionSeries } from './utils/succession';

// jsdom has no layout engine; assert on element counts/attributes only.

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  cleanup();
});

/** Builds a 24-length hourly counts array from (hour, value) pairs. */
function hourly(pairs: [number, number][]): number[] {
  const byHour = new Map(pairs);
  return Array.from({ length: 24 }, (_, i) => byHour.get(i) ?? 0);
}

const sample: SuccessionSeries[] = [
  {
    scientificName: 'Turdus merula',
    commonName: 'Eurasian Blackbird',
    counts: hourly([
      [6, 30],
      [18, 10],
    ]),
    total: 40,
  },
  {
    scientificName: 'Erithacus rubecula',
    commonName: 'European Robin',
    counts: hourly([[12, 12]]),
    total: 12,
  },
];

describe('AcousticSuccessionChart', () => {
  it('renders without throwing for an empty series', () => {
    expect(() => render(AcousticSuccessionChart, { props: { series: [] } })).not.toThrow();
  });

  it('renders one stacked band area path per species', async () => {
    const { container } = render(AcousticSuccessionChart, {
      props: { series: sample, width: 800 },
    });
    await Promise.resolve();
    expect(container.querySelectorAll('path.stream-area')).toHaveLength(2);
  });

  it('keys each band by the stable scientific name', async () => {
    const { container } = render(AcousticSuccessionChart, {
      props: { series: sample, width: 800 },
    });
    await Promise.resolve();
    expect(container.querySelector('.stream-band[data-species="Turdus merula"]')).toBeTruthy();
    expect(container.querySelector('.stream-band[data-species="Erithacus rubecula"]')).toBeTruthy();
  });

  it('renders an x axis group and no y axis', async () => {
    const { container } = render(AcousticSuccessionChart, { props: { series: sample } });
    await Promise.resolve();
    expect(container.querySelector('.x-axis')).toBeTruthy();
    expect(container.querySelector('.y-axis')).toBeNull();
  });

  it('sets an accessible label on the chart', () => {
    const { container } = render(AcousticSuccessionChart, {
      props: { series: sample, ariaLabelKey: 'my.aria.key' },
    });
    // t is mocked to return the key, so the aria-label is the key itself.
    expect(container.querySelector('[aria-label="my.aria.key"]')).toBeTruthy();
  });

  it('renders a screen-reader summary when there is data', async () => {
    const { getByTestId } = render(AcousticSuccessionChart, { props: { series: sample } });
    await Promise.resolve();
    expect(getByTestId('succession-summary')).toBeTruthy();
  });

  it('renders the optional note caption when a noteKey is given', async () => {
    const { container } = render(AcousticSuccessionChart, {
      props: { series: sample, noteKey: 'my.note.key' },
    });
    await Promise.resolve();
    expect(container.querySelector('.succession-note')).toBeTruthy();
  });

  it('omits the note caption when the series is empty', () => {
    const { container } = render(AcousticSuccessionChart, {
      props: { series: [], noteKey: 'my.note.key' },
    });
    expect(container.querySelector('.succession-note')).toBeNull();
  });
});
