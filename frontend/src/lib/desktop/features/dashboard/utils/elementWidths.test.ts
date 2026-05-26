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

describe('dashboard element widths', () => {
  it('keeps full-width-only elements full width even when half is configured', () => {
    expect(getEffectiveWidth(element('daily-summary', 'half'))).toBe('full');
    expect(getEffectiveWidth(element('live-spectrogram', 'half'))).toBe('full');
  });

  it('returns responsive span classes for mobile and wider dashboard grids', () => {
    expect(getResponsiveGridSpanClass(element('currently-hearing', 'half'))).toBe('col-span-1');
    expect(getResponsiveGridSpanClass(element('detections-grid'))).toBe('col-span-1 md:col-span-2');
  });
});
