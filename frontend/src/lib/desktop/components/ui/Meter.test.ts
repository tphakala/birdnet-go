import { describe, it, expect } from 'vitest';
import { renderTyped, screen } from '../../../../test/render-helpers';
import Meter, { type MeterSegment, type MeterThreshold, type MeterTone } from './Meter.svelte';

/** The fills drawn inside the meter, in order. */
function fillsOf(meter: HTMLElement): HTMLElement[] {
  return Array.from(meter.querySelectorAll<HTMLElement>('[data-tone]'));
}

describe('Meter', () => {
  it('exposes value, range and name as a meter', () => {
    renderTyped(Meter, {
      props: { value: 3, max: 8, label: 'Disk usage of /', valueText: '3 GB of 8 GB' },
    });

    const meter = screen.getByRole('meter', { name: 'Disk usage of /' });
    expect(meter).toHaveAttribute('aria-valuenow', '3');
    expect(meter).toHaveAttribute('aria-valuemin', '0');
    expect(meter).toHaveAttribute('aria-valuemax', '8');
    expect(meter).toHaveAttribute('aria-valuetext', '3 GB of 8 GB');
    expect(fillsOf(meter).map(fill => fill.style.flexBasis)).toEqual(['37.5%']);
  });

  const thresholds: MeterThreshold[] = [
    // Out of order on purpose: the highest threshold reached wins, not the last listed
    { value: 90, tone: 'error' },
    { value: 70, tone: 'warning' },
  ];

  it.each([
    [0, 'success'],
    [69.99, 'success'],
    [70, 'warning'],
    [89.99, 'warning'],
    [90, 'error'],
    [100, 'error'],
  ] as const)('picks the tone of the highest threshold reached (%s -> %s)', (value, tone) => {
    renderTyped(Meter, { props: { value, tone: 'success', thresholds, label: 'Usage' } });

    expect(fillsOf(screen.getByRole('meter')).map(fill => fill.dataset.tone)).toEqual([tone]);
  });

  it.each([
    [150, '100', '100%'],
    [-5, '0', '0%'],
    [Number.NaN, '0', '0%'],
    [Number.POSITIVE_INFINITY, '0', '0%'],
  ])('clamps out-of-range and non-finite values to the range (%s)', (value, now, basis) => {
    renderTyped(Meter, { props: { value, label: 'CPU' } });

    const meter = screen.getByRole('meter');
    expect(meter).toHaveAttribute('aria-valuenow', now);
    expect(fillsOf(meter).map(fill => fill.style.flexBasis)).toEqual([basis]);
  });

  it('renders one fill per segment, skipping empty ones and stopping at max', () => {
    const segments: MeterSegment[] = [
      { value: 40, tone: 'series-2' },
      { value: 0, tone: 'series-3' },
      { value: 30, tone: 'series-2', pattern: 'hatched' },
      { value: 50, tone: 'series-4' },
    ];
    renderTyped(Meter, { props: { value: 40, segments, label: 'RAM' } });

    const fills = fillsOf(screen.getByRole('meter'));
    expect(
      fills.map(fill => [fill.dataset.tone, fill.dataset.pattern, fill.style.flexBasis])
    ).toEqual([
      ['series-2', 'solid', '40%'],
      ['series-2', 'hatched', '30%'],
      ['series-4', 'solid', '30%'],
    ]);
  });

  it.each([
    ['series-1', '--viz-1'],
    ['series-5', '--viz-5'],
    ['series-8', '--viz-8'],
    ['success', '--viz-success'],
    ['warning', '--viz-warning'],
    ['error', '--viz-error'],
  ] as [MeterTone, string][])('fills the %s tone from the %s token', (tone, token) => {
    renderTyped(Meter, { props: { value: 50, tone, label: 'Usage' } });

    const [fill] = fillsOf(screen.getByRole('meter'));
    expect(fill.style.getPropertyValue('--meter-fill')).toBe(`var(${token})`);
  });

  it.each([0, -10, Number.NaN, Number.POSITIVE_INFINITY])(
    'draws an empty meter with a zero range when max is %s',
    max => {
      renderTyped(Meter, { props: { value: 5, max, label: 'Usage' } });

      const meter = screen.getByRole('meter');
      expect(meter).toHaveAttribute('aria-valuemax', '0');
      expect(meter).toHaveAttribute('aria-valuenow', '0');
      expect(fillsOf(meter).map(fill => fill.style.flexBasis)).toEqual(['0%']);
    }
  );

  it('uses the first series and the card size by default, and keeps className', () => {
    renderTyped(Meter, { props: { value: 50, label: 'Usage', className: 'w-12' } });

    const meter = screen.getByRole('meter');
    const [fill] = fillsOf(meter);
    expect(fill.dataset.tone).toBe('series-1');
    expect(meter).toHaveClass('h-2', 'w-12');
  });

  it('uses the compact height for the xs size', () => {
    renderTyped(Meter, { props: { value: 50, size: 'xs', label: 'Usage' } });

    expect(screen.getByRole('meter')).toHaveClass('h-1.5');
  });
});
