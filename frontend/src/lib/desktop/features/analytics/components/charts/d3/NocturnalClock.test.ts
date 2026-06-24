import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup } from '@testing-library/svelte';
import NocturnalClock from './NocturnalClock.svelte';
import type { NocturnalClockData } from './utils/nocturnal';

// jsdom has no layout engine; assert on element counts/attributes only. The radial vs linear mode
// is driven by the width prop (BaseChart falls back to it because ResizeObserver never fires here).

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  cleanup();
});

// 24 hourly counts with a clear nocturnal peak, plus sun times for the day/night shading.
const sample: NocturnalClockData = {
  hourly: [12, 9, 7, 5, 3, 2, 4, 8, 6, 3, 1, 0, 0, 1, 2, 3, 5, 7, 9, 11, 14, 16, 15, 13],
  sun: {
    date: '2026-03-16',
    sunrise: 6 * 60 + 12,
    sunset: 18 * 60 + 30,
    civilDawn: 5 * 60 + 44,
    civilDusk: 19 * 60,
    available: true,
  },
};

const noSun: NocturnalClockData = { ...sample, sun: null };

const empty: NocturnalClockData = { hourly: new Array<number>(24).fill(0), sun: null };

describe('NocturnalClock', () => {
  it('renders without throwing for empty data', () => {
    expect(() => render(NocturnalClock, { props: { data: empty } })).not.toThrow();
  });

  it('renders the radial dial with one bar per hour at desktop width', async () => {
    const { container } = render(NocturnalClock, { props: { data: sample, width: 800 } });
    await Promise.resolve();
    expect(container.querySelector('.clock-radial')).toBeTruthy();
    expect(container.querySelector('.clock-linear')).toBeNull();
    expect(container.querySelectorAll('.clock-radial .hour-bar')).toHaveLength(24);
  });

  it('shades the daytime arc when sun times are available', async () => {
    const { container } = render(NocturnalClock, { props: { data: sample, width: 800 } });
    await Promise.resolve();
    expect(container.querySelector('.day-arc')).toBeTruthy();
    // Civil dawn and civil dusk both present -> two twilight bands.
    expect(container.querySelectorAll('.twilight-arc')).toHaveLength(2);
  });

  it('omits the day shading when sun times are unavailable', async () => {
    const { container } = render(NocturnalClock, { props: { data: noSun, width: 800 } });
    await Promise.resolve();
    expect(container.querySelector('.day-arc')).toBeNull();
    expect(container.querySelector('.night-ring')).toBeNull();
    // The hourly bars still render without shading.
    expect(container.querySelectorAll('.clock-radial .hour-bar')).toHaveLength(24);
  });

  it('falls back to a linear bar layout on a narrow viewport', async () => {
    const { container } = render(NocturnalClock, { props: { data: sample, width: 300 } });
    await Promise.resolve();
    expect(container.querySelector('.clock-linear')).toBeTruthy();
    expect(container.querySelector('.clock-radial')).toBeNull();
    expect(container.querySelectorAll('.clock-linear .hour-bar')).toHaveLength(24);
    expect(container.querySelector('.x-axis')).toBeTruthy();
    expect(container.querySelector('.y-axis')).toBeTruthy();
  });

  it('shades the daytime region in the linear fallback when sun is available', async () => {
    const { container } = render(NocturnalClock, { props: { data: sample, width: 300 } });
    await Promise.resolve();
    expect(container.querySelector('.day-region')).toBeTruthy();
  });

  it('omits the day region in the linear fallback when sun is unavailable', async () => {
    const { container } = render(NocturnalClock, { props: { data: noSun, width: 300 } });
    await Promise.resolve();
    expect(container.querySelector('.clock-linear')).toBeTruthy();
    expect(container.querySelector('.day-region')).toBeNull();
    expect(container.querySelectorAll('.clock-linear .hour-bar')).toHaveLength(24);
  });

  it('renders a day/twilight/night legend when sun is available', async () => {
    const { getByTestId } = render(NocturnalClock, { props: { data: sample, width: 800 } });
    await Promise.resolve();
    const legend = getByTestId('nocturnal-legend');
    expect(legend).toBeTruthy();
    // Day + Twilight + Night = three entries (sample has genuine civil dawn and dusk).
    expect(legend.querySelectorAll('li')).toHaveLength(3);
  });

  it('omits the twilight legend entry when no genuine civil twilight exists', async () => {
    // White-night style: sun rises/sets but civil dawn/dusk are null.
    const whiteNight: NocturnalClockData = {
      hourly: sample.hourly,
      sun: {
        date: '2026-06-21',
        sunrise: 3 * 60 + 48,
        sunset: 23 * 60 + 3,
        civilDawn: null,
        civilDusk: null,
        available: true,
      },
    };
    const { getByTestId } = render(NocturnalClock, { props: { data: whiteNight, width: 800 } });
    await Promise.resolve();
    expect(getByTestId('nocturnal-legend').querySelectorAll('li')).toHaveLength(2);
  });

  it('omits the legend entirely when sun is unavailable', async () => {
    const { queryByTestId } = render(NocturnalClock, { props: { data: noSun, width: 800 } });
    await Promise.resolve();
    expect(queryByTestId('nocturnal-legend')).toBeNull();
  });

  it('sets an accessible label on the chart container', () => {
    const { container } = render(NocturnalClock, {
      props: { data: sample, ariaLabel: 'Nocturnal clock' },
    });
    expect(container.querySelector('[aria-label="Nocturnal clock"]')).toBeTruthy();
  });

  it('renders a screen-reader summary when there is activity', async () => {
    const { getByTestId } = render(NocturnalClock, { props: { data: sample } });
    await Promise.resolve();
    expect(getByTestId('nocturnal-summary')).toBeTruthy();
  });

  it('omits the summary when there is no activity', async () => {
    const { queryByTestId } = render(NocturnalClock, { props: { data: empty } });
    await Promise.resolve();
    expect(queryByTestId('nocturnal-summary')).toBeNull();
  });
});
