import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup } from '@testing-library/svelte';
import DawnChorusOnset from './DawnChorusOnset.svelte';
import type { DawnOnsetData, DawnOnsetPoint } from './utils/dawnOnset';

// jsdom has no layout engine; assert on element counts/attributes only.

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  cleanup();
});

const sample: DawnOnsetData = {
  points: [
    { date: '2026-03-01', onsetRelMinutes: 20, detectionCount: 40 },
    { date: '2026-03-02', onsetRelMinutes: null, detectionCount: 2 }, // gap day
    { date: '2026-03-03', onsetRelMinutes: 5, detectionCount: 33 },
    { date: '2026-03-04', onsetRelMinutes: -10, detectionCount: 50 },
  ],
};

const empty: DawnOnsetData = { points: [] };

const allGaps: DawnOnsetData = {
  points: [{ date: '2026-03-01', onsetRelMinutes: null, detectionCount: 1 }],
};

// Two clusters of measurable days separated by a long null run, long enough that the centered
// 7-day trend window goes empty in the middle (so the trend must break, not interpolate across).
const gappy: DawnOnsetData = (() => {
  const points: DawnOnsetPoint[] = [];
  [10, 12, 8, 11].forEach((v, i) =>
    points.push({ date: `2026-03-0${i + 1}`, onsetRelMinutes: v, detectionCount: 20 })
  );
  for (let day = 5; day <= 12; day++) {
    points.push({
      date: `2026-03-${String(day).padStart(2, '0')}`,
      onsetRelMinutes: null,
      detectionCount: 1,
    });
  }
  [9, 7, 10, 12].forEach((v, i) =>
    points.push({ date: `2026-03-${13 + i}`, onsetRelMinutes: v, detectionCount: 20 })
  );
  return { points };
})();

describe('DawnChorusOnset', () => {
  it('renders without throwing for empty data', () => {
    expect(() => render(DawnChorusOnset, { props: { data: empty } })).not.toThrow();
  });

  it('renders without throwing when every day is a gap', () => {
    expect(() => render(DawnChorusOnset, { props: { data: allGaps } })).not.toThrow();
  });

  it('draws one dot per day with a measurable onset (nulls are gaps)', async () => {
    const { container } = render(DawnChorusOnset, { props: { data: sample, width: 800 } });
    await Promise.resolve();
    // 3 non-null points -> 3 dots; the null day is skipped.
    expect(container.querySelectorAll('.onset-dots circle')).toHaveLength(3);
  });

  it('breaks the trend line over a sustained gap instead of interpolating across it', async () => {
    const { container } = render(DawnChorusOnset, { props: { data: gappy, width: 800 } });
    await Promise.resolve();
    const path = container.querySelector('.onset-trend');
    expect(path).toBeTruthy();
    // A segmented path starts each run with a move-to ('M'); interpolation across the gap would
    // yield a single continuous run with exactly one 'M'.
    const moveCommands = (path?.getAttribute('d')?.match(/M/g) ?? []).length;
    expect(moveCommands).toBeGreaterThanOrEqual(2);
  });

  it('renders the axes, the civil-dawn reference line, and a trend line', async () => {
    const { container } = render(DawnChorusOnset, { props: { data: sample, width: 800 } });
    await Promise.resolve();
    expect(container.querySelector('.x-axis')).toBeTruthy();
    expect(container.querySelector('.y-axis')).toBeTruthy();
    expect(container.querySelector('.civil-dawn-line')).toBeTruthy();
    expect(container.querySelector('.onset-trend')).toBeTruthy();
  });

  it('sets an accessible label on the chart container', () => {
    const { container } = render(DawnChorusOnset, {
      props: { data: sample, ariaLabel: 'Dawn onset' },
    });
    expect(container.querySelector('[aria-label="Dawn onset"]')).toBeTruthy();
  });

  it('renders a screen-reader summary when there is data', async () => {
    const { getByTestId } = render(DawnChorusOnset, { props: { data: sample } });
    await Promise.resolve();
    expect(getByTestId('dawn-onset-summary')).toBeTruthy();
  });
});
