/**
 * Browser Mode Test: a stacked Meter ends where its value says
 *
 * Each part's flex-basis is its share of the track, so anything drawn between
 * parts must sit inside those widths. jsdom has no layout, so only a real
 * browser shows whether the joints push a partially filled stack past its
 * value (a flex gap added 2 px per joint on top of the parts).
 *
 * Usage:
 *   npm run test:browser
 */

import { describe, it, expect, beforeEach, afterEach } from 'vitest';
import { render } from 'vitest-browser-svelte';

import Meter from '$lib/desktop/components/ui/Meter.svelte';

const TRACK_WIDTH_PX = 200;
const PART_PERCENT = 20;
const PART_COUNT = 3;
const JOINT_PX = 2;
// Sub-pixel rounding of percentage widths
const TOLERANCE_PX = 0.5;

describe('Meter stacked segments in a real browser', () => {
  let noTransitions: HTMLStyleElement;

  beforeEach(() => {
    // The fills transition flex-basis; a read right after render would see a mid-transition width
    noTransitions = document.createElement('style');
    noTransitions.textContent = '* { transition: none !important; }';
    document.head.appendChild(noTransitions);
  });

  afterEach(() => {
    noTransitions.remove();
  });

  it('keeps a partially filled stack and its joints within the share of the track it reports', async () => {
    await render(Meter, {
      props: {
        label: 'Memory',
        value: PART_PERCENT * PART_COUNT,
        max: 100,
        style: `width: ${TRACK_WIDTH_PX}px`,
        segments: [
          { value: PART_PERCENT, tone: 'series-1' },
          { value: PART_PERCENT, tone: 'series-2', pattern: 'hatched' },
          { value: PART_PERCENT, tone: 'series-3' },
        ],
      },
    });

    const track = document.querySelector('[role="meter"]');
    if (!track) throw new Error('meter not rendered');
    const fills = [...track.querySelectorAll('.meter-fill')];
    expect(fills).toHaveLength(PART_COUNT);

    const trackBox = track.getBoundingClientRect();
    expect(trackBox.width).toBeCloseTo(TRACK_WIDTH_PX, 0);

    // The stack ends at 60% of the track, not 60% plus the joints
    const stackEnd = fills[fills.length - 1].getBoundingClientRect().right - trackBox.left;
    expect(Math.abs(stackEnd - (TRACK_WIDTH_PX * PART_PERCENT * PART_COUNT) / 100)).toBeLessThan(
      TOLERANCE_PX
    );

    // Each part keeps its own share with its joint inside it
    for (const fill of fills) {
      expect(
        Math.abs(fill.getBoundingClientRect().width - (TRACK_WIDTH_PX * PART_PERCENT) / 100)
      ).toBeLessThan(TOLERANCE_PX);
    }

    // Neighbouring parts touch: no space is laid out between them
    for (const [previous, next] of fills.slice(0, -1).map((fill, i) => [fill, fills[i + 1]])) {
      expect(
        Math.abs(next.getBoundingClientRect().left - previous.getBoundingClientRect().right)
      ).toBeLessThan(TOLERANCE_PX);
    }

    // The joint is still drawn: each later part leaves its start edge unpainted
    for (const fill of fills.slice(1)) {
      const style = getComputedStyle(fill);
      expect(style.borderInlineStartWidth).toBe(`${JOINT_PX}px`);
      expect(style.borderInlineStartColor).toBe('rgba(0, 0, 0, 0)');
      expect(style.backgroundClip).toBe('padding-box');
    }
  });
});
