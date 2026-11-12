/**
 * Browser Mode Test: a stacked Meter ends where its value says
 *
 * Each part's flex-basis is its share of the track, so the joint drawn between
 * parts must take no layout space. jsdom has no layout, so only a real browser
 * shows whether the joints push a partially filled stack past its value (a flex
 * gap added 2 px per joint, and a 2 px border made a narrower part at least
 * 2 px wide).
 *
 * Usage:
 *   npm run test:browser
 */

import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { render } from 'vitest-browser-svelte';
import type { ComponentProps } from 'svelte';

import Meter from '$lib/desktop/components/ui/Meter.svelte';

type MeterSegment = NonNullable<ComponentProps<typeof Meter>['segments']>[number];

const TRACK_WIDTH_PX = 200;
const JOINT_PX = 2;
// Sub-pixel rounding of percentage widths
const TOLERANCE_PX = 0.5;
// The theme stylesheet is not loaded here; the joint paints in the track token
const TRACK_COLOR = 'rgb(10, 20, 30)';

const pxOf = (percent: number): number => (TRACK_WIDTH_PX * percent) / 100;

async function renderStack(
  segments: MeterSegment[]
): Promise<{ track: Element; fills: Element[] }> {
  await render(Meter, {
    props: {
      label: 'Memory',
      value: segments.reduce((sum, segment) => sum + segment.value, 0),
      max: 100,
      style: `width: ${TRACK_WIDTH_PX}px`,
      segments,
    },
  });
  const track = document.querySelector('[role="meter"]');
  if (!track) throw new Error('meter not rendered');
  return { track, fills: [...track.querySelectorAll('.meter-fill')] };
}

describe('Meter stacked segments in a real browser', () => {
  let noTransitions: HTMLStyleElement;

  beforeEach(() => {
    // The fills transition flex-basis; a read right after render would see a mid-transition width
    noTransitions = document.createElement('style');
    noTransitions.textContent = '* { transition: none !important; }';
    document.head.appendChild(noTransitions);
    document.documentElement.style.setProperty('--viz-track', TRACK_COLOR);
  });

  afterEach(() => {
    noTransitions.remove();
    document.documentElement.style.removeProperty('--viz-track');
    document.body.replaceChildren();
  });

  it('keeps a partially filled stack and its joints within the share of the track it reports', async () => {
    const partPercent = 20;
    const { track, fills } = await renderStack([
      { value: partPercent, tone: 'series-1' },
      { value: partPercent, tone: 'series-2', pattern: 'hatched' },
      { value: partPercent, tone: 'series-3' },
    ]);
    expect(fills).toHaveLength(3);

    const trackBox = track.getBoundingClientRect();
    expect(trackBox.width).toBeCloseTo(TRACK_WIDTH_PX, 0);

    // The stack ends at 60% of the track, not 60% plus the joints
    const stackEnd = fills[fills.length - 1].getBoundingClientRect().right - trackBox.left;
    expect(Math.abs(stackEnd - pxOf(partPercent * 3))).toBeLessThan(TOLERANCE_PX);

    // Each part keeps its own share with its joint inside it
    for (const fill of fills) {
      expect(Math.abs(fill.getBoundingClientRect().width - pxOf(partPercent))).toBeLessThan(
        TOLERANCE_PX
      );
    }

    // Neighbouring parts touch: no space is laid out between them
    for (const [previous, next] of fills.slice(0, -1).map((fill, i) => [fill, fills[i + 1]])) {
      expect(
        Math.abs(next.getBoundingClientRect().left - previous.getBoundingClientRect().right)
      ).toBeLessThan(TOLERANCE_PX);
    }

    // The joint is still drawn: each later part paints its start edge in the track color
    expect(getComputedStyle(fills[0]).boxShadow).toBe('none');
    for (const fill of fills.slice(1)) {
      expect(getComputedStyle(fill).boxShadow).toBe(
        `${TRACK_COLOR} ${JOINT_PX}px 0px 0px 0px inset`
      );
    }
  });

  it('keeps a part narrower than the joint at its own width', async () => {
    const widePercent = 20;
    const narrowPercent = 0.5;
    const { track, fills } = await renderStack([
      { value: widePercent, tone: 'series-1' },
      { value: narrowPercent, tone: 'series-2' },
    ]);
    expect(fills).toHaveLength(2);

    const trackBox = track.getBoundingClientRect();
    // 1 px for the narrow part, not the 2 px a joint with layout width would force
    expect(Math.abs(fills[1].getBoundingClientRect().width - pxOf(narrowPercent))).toBeLessThan(
      TOLERANCE_PX
    );
    const stackEnd = fills[1].getBoundingClientRect().right - trackBox.left;
    expect(Math.abs(stackEnd - pxOf(widePercent + narrowPercent))).toBeLessThan(TOLERANCE_PX);
  });
});
