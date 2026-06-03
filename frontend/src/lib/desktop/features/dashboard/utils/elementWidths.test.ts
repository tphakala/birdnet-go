import { describe, expect, it } from 'vitest';
import type { DashboardElement } from '$lib/stores/settings';
import { getEffectiveWidth, getResponsiveGridSpanClass } from './elementWidths';

function element(
  type: DashboardElement['type'],
  width?: DashboardElement['width']
): DashboardElement {
  return {
    id: `${type}-test`,
    type,
    enabled: true,
    ...(width ? { width } : {}),
  };
}

const TYPE_DAILY_SUMMARY: DashboardElement['type'] = 'daily-summary';
const TYPE_LIVE_SPECTROGRAM: DashboardElement['type'] = 'live-spectrogram';
const TYPE_CURRENTLY_HEARING: DashboardElement['type'] = 'currently-hearing';
const TYPE_DETECTIONS_GRID: DashboardElement['type'] = 'detections-grid';
const WIDTH_HALF: DashboardElement['width'] = 'half';
const EFFECTIVE_WIDTH_FULL: ReturnType<typeof getEffectiveWidth> = 'full';
const SPAN_HALF = 'col-span-1';
const SPAN_FULL_RESPONSIVE = 'col-span-1 md:col-span-2';

describe('dashboard element widths', () => {
  it('keeps full-width-only elements full width even when half is configured', () => {
    expect(getEffectiveWidth(element(TYPE_DAILY_SUMMARY, WIDTH_HALF))).toBe(EFFECTIVE_WIDTH_FULL);
    expect(getEffectiveWidth(element(TYPE_LIVE_SPECTROGRAM, WIDTH_HALF))).toBe(
      EFFECTIVE_WIDTH_FULL
    );
  });

  it('returns responsive span classes for mobile and wider dashboard grids', () => {
    expect(getResponsiveGridSpanClass(element(TYPE_CURRENTLY_HEARING, WIDTH_HALF))).toBe(SPAN_HALF);
    expect(getResponsiveGridSpanClass(element(TYPE_DETECTIONS_GRID))).toBe(SPAN_FULL_RESPONSIVE);
  });
});
