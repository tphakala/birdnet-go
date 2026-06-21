import { describe, it, expect } from 'vitest';
import {
  computeAnchorPosition,
  applyAnchorPosition,
  type TriggerRect,
  type AnchorPosition,
} from './anchorPosition';

const VIEWPORT = { width: 1000, height: 800 };

/** Build a trigger rect; defaults to a small trigger near the top-left. */
function rect(overrides: Partial<TriggerRect> = {}): TriggerRect {
  return {
    top: 100,
    bottom: 130,
    left: 200,
    right: 300,
    width: 100,
    ...overrides,
  };
}

describe('computeAnchorPosition', () => {
  describe('placement decision', () => {
    it('places below when there is room below the trigger', () => {
      const pos = computeAnchorPosition({
        triggerRect: rect(),
        floatingHeight: 200,
        floatingWidth: 100,
        viewport: VIEWPORT,
      });
      expect(pos.placement).toBe('below');
      expect(pos.top).toBe(130 + 8); // triggerRect.bottom + default offset
      expect(pos.bottom).toBeNull();
    });

    it('flips above when there is not enough room below and more room above', () => {
      // Trigger low in the viewport: bottom at 760, only 40px below, plenty above.
      const pos = computeAnchorPosition({
        triggerRect: rect({ top: 730, bottom: 760 }),
        floatingHeight: 200,
        floatingWidth: 100,
        viewport: VIEWPORT,
      });
      expect(pos.placement).toBe('above');
      expect(pos.bottom).toBe(800 - 730 + 8); // viewport.height - triggerRect.top + offset
      expect(pos.top).toBeNull();
    });

    it('stays below when room below is tight but there is even less room above', () => {
      // Trigger high: top at 30 (little above), bottom at 700 (100px below).
      const pos = computeAnchorPosition({
        triggerRect: rect({ top: 30, bottom: 700 }),
        floatingHeight: 200,
        floatingWidth: 100,
        viewport: VIEWPORT,
      });
      // spaceBelow (100) < height+offset (208) would suggest flipping, but
      // spaceAbove (30) is not greater than spaceBelow (100), so it stays below.
      expect(pos.placement).toBe('below');
    });

    it('flips earlier when an earlyFlipBuffer is provided', () => {
      const base = {
        triggerRect: rect({ top: 400, bottom: 430 }),
        floatingHeight: 200,
        floatingWidth: 100,
        viewport: VIEWPORT,
      };
      // spaceBelow = 370, height+offset = 208 -> stays below without a buffer.
      expect(computeAnchorPosition(base).placement).toBe('below');
      // With a large buffer the required room below exceeds the available space,
      // and there is more room above (spaceAbove 400 > spaceBelow 370) -> flips.
      expect(computeAnchorPosition({ ...base, earlyFlipBuffer: 200 }).placement).toBe('above');
    });
  });

  describe('Invariant A: "above" anchors the bottom edge (height-independent)', () => {
    it('produces the same bottom value regardless of element height', () => {
      const triggerRect = rect({ top: 730, bottom: 760 });
      const shortPopup = computeAnchorPosition({
        triggerRect,
        floatingHeight: 200,
        floatingWidth: 100,
        viewport: VIEWPORT,
      });
      const tallPopup = computeAnchorPosition({
        triggerRect,
        floatingHeight: 340,
        floatingWidth: 100,
        viewport: VIEWPORT,
      });
      expect(shortPopup.placement).toBe('above');
      expect(tallPopup.placement).toBe('above');
      // The bottom edge meets the trigger identically; only the upward extent differs.
      expect(shortPopup.bottom).toBe(tallPopup.bottom);
    });

    it('regression (PR #3498): an estimate that differs from the real height does not misalign the above edge', () => {
      const triggerRect = rect({ top: 730, bottom: 760 });
      // First pass uses an estimate (real height not yet measured).
      const estimated = computeAnchorPosition({
        triggerRect,
        floatingHeight: 0,
        estimatedHeight: 280,
        floatingWidth: 100,
        viewport: VIEWPORT,
      });
      // After layout, the real height (340) is larger than the estimate (280).
      const measured = computeAnchorPosition({
        triggerRect,
        floatingHeight: 340,
        estimatedHeight: 280,
        floatingWidth: 100,
        viewport: VIEWPORT,
      });
      expect(estimated.placement).toBe('above');
      expect(measured.placement).toBe('above');
      // Because "above" anchors the bottom edge, the wrong estimate cannot shift it.
      expect(estimated.bottom).toBe(measured.bottom);
    });

    it('clamps the bottom so a tall element flipped above keeps its top edge on-screen', () => {
      // Short viewport, mid trigger: flips above (more room above than below), but the
      // element is taller than the space above, so an unclamped bottom anchor would
      // push the top edge off the top of the viewport.
      const pos = computeAnchorPosition({
        triggerRect: rect({ top: 300, bottom: 330 }),
        floatingHeight: 400,
        floatingWidth: 100,
        viewport: { width: 1000, height: 600 },
      });
      expect(pos.placement).toBe('above');
      // bottom clamped to viewport.height - height - viewportMargin = 600 - 400 - 8 = 192,
      // which puts the top edge at 600 - 192 - 400 = 8 (== viewportMargin), still on-screen.
      expect(pos.bottom).toBe(192);
      expect(pos.top).toBeNull();
    });

    it('keeps the top edge at the margin (bottom overflows) for an element taller than the viewport', () => {
      // Element (400) taller than the whole viewport (300). The clamp pins the top edge
      // at viewportMargin and lets the bottom overflow, rather than hiding the top content.
      const viewport = { width: 1000, height: 300 };
      const pos = computeAnchorPosition({
        triggerRect: rect({ top: 150, bottom: 180 }),
        floatingHeight: 400,
        floatingWidth: 100,
        viewport,
      });
      expect(pos.placement).toBe('above');
      // maxBottom = 300 - 400 - 8 = -108 (negative is intended: bottom edge below the viewport)
      expect(pos.bottom).toBe(-108);
      // top edge = viewport.height - bottom - height = 300 - (-108) - 400 = 8 == viewportMargin
      const topEdge = viewport.height - (pos.bottom as number) - 400;
      expect(topEdge).toBe(8);
    });
  });

  describe('Invariant B: measured height with estimate fallback', () => {
    it('uses the estimate for the flip decision when the height is not yet measured (offsetHeight 0)', () => {
      // DatePicker first-open case: low trigger, height still 0 at first call.
      const triggerRect = rect({ top: 730, bottom: 760 });
      const withoutEstimate = computeAnchorPosition({
        triggerRect,
        floatingHeight: 0,
        floatingWidth: 100,
        viewport: VIEWPORT,
      });
      const withEstimate = computeAnchorPosition({
        triggerRect,
        floatingHeight: 0,
        estimatedHeight: 200,
        floatingWidth: 100,
        viewport: VIEWPORT,
      });
      // Height 0 with no estimate -> spaceBelow(40) < offset(8) is false -> stays below
      // (the pre-fix DatePicker bug: a 0 height always "fits below").
      expect(withoutEstimate.placement).toBe('below');
      // With an estimate the helper correctly flips above on the first pass.
      expect(withEstimate.placement).toBe('above');
    });

    it('prefers the measured height over the estimate when both are present', () => {
      const triggerRect = rect({ top: 730, bottom: 760 });
      // Measured height is small enough to fit below would-be... but here it is large.
      const pos = computeAnchorPosition({
        triggerRect,
        floatingHeight: 300,
        estimatedHeight: 10, // would wrongly keep it below if used
        floatingWidth: 100,
        viewport: VIEWPORT,
      });
      expect(pos.placement).toBe('above');
    });
  });

  describe('horizontal alignment and clamping', () => {
    it('aligns the left edge to the trigger for align=start', () => {
      const pos = computeAnchorPosition({
        triggerRect: rect({ left: 200, right: 300 }),
        floatingHeight: 100,
        floatingWidth: 100,
        align: 'start',
        viewport: VIEWPORT,
      });
      expect(pos.left).toBe(200);
    });

    it('aligns the right edge to the trigger for align=end', () => {
      const pos = computeAnchorPosition({
        triggerRect: rect({ left: 600, right: 700 }),
        floatingHeight: 100,
        floatingWidth: 250,
        align: 'end',
        viewport: VIEWPORT,
      });
      expect(pos.left).toBe(700 - 250); // right edge aligns with trigger right
    });

    it('centres on the trigger for align=center', () => {
      const pos = computeAnchorPosition({
        triggerRect: rect({ left: 400, right: 500, width: 100 }),
        floatingHeight: 100,
        floatingWidth: 200,
        align: 'center',
        viewport: VIEWPORT,
      });
      expect(pos.left).toBe(400 + 50 - 100); // trigger centre minus half the element width
    });

    it('regression (#1086): clamps the left edge when an end-aligned element would overflow the left viewport edge', () => {
      // Narrow trigger near the left edge with a wide menu pinned to its right edge.
      const pos = computeAnchorPosition({
        triggerRect: rect({ left: 10, right: 40, width: 30 }),
        floatingHeight: 100,
        floatingWidth: 250, // 40 - 250 = -210 would overflow off-screen left
        align: 'end',
        viewportMargin: 8,
        viewport: VIEWPORT,
      });
      expect(pos.left).toBe(8); // clamped to the viewport margin
    });

    it('clamps the right edge so the element never overflows the right viewport edge', () => {
      const pos = computeAnchorPosition({
        triggerRect: rect({ left: 950, right: 990, width: 40 }),
        floatingHeight: 100,
        floatingWidth: 200,
        align: 'start',
        viewportMargin: 8,
        viewport: VIEWPORT,
      });
      // maxLeft = 1000 - 200 - 8 = 792
      expect(pos.left).toBe(792);
    });
  });

  describe('Invariant D: exactly one vertical edge is set', () => {
    it('sets top and clears bottom when below', () => {
      const pos = computeAnchorPosition({
        triggerRect: rect(),
        floatingHeight: 100,
        floatingWidth: 100,
        viewport: VIEWPORT,
      });
      expect(pos.top).not.toBeNull();
      expect(pos.bottom).toBeNull();
    });

    it('sets bottom and clears top when above', () => {
      const pos = computeAnchorPosition({
        triggerRect: rect({ top: 730, bottom: 760 }),
        floatingHeight: 200,
        floatingWidth: 100,
        viewport: VIEWPORT,
      });
      expect(pos.bottom).not.toBeNull();
      expect(pos.top).toBeNull();
    });
  });

  it('honours a custom offset', () => {
    const pos = computeAnchorPosition({
      triggerRect: rect(),
      floatingHeight: 100,
      floatingWidth: 100,
      offset: 4,
      viewport: VIEWPORT,
    });
    expect(pos.top).toBe(130 + 4);
  });

  it('applies the default viewport margin (8) when none is provided', () => {
    // Right-edge clamp with the default margin: maxLeft = 1000 - 200 - 8 = 792.
    const pos = computeAnchorPosition({
      triggerRect: rect({ left: 950, right: 990, width: 40 }),
      floatingHeight: 100,
      floatingWidth: 200,
      align: 'start',
      viewport: VIEWPORT,
    });
    expect(pos.left).toBe(792);
  });

  it('uses a strict flip comparison at the boundary (does not flip when room below exactly equals need)', () => {
    // spaceBelow exactly equals height + offset -> the `<` test is false -> stays below.
    // trigger bottom 592 -> spaceBelow = 800 - 592 = 208 == 200 + 8.
    const pos = computeAnchorPosition({
      triggerRect: rect({ top: 560, bottom: 592 }),
      floatingHeight: 200,
      floatingWidth: 100,
      offset: 8,
      viewport: VIEWPORT,
    });
    expect(pos.placement).toBe('below');
  });
});

describe('applyAnchorPosition', () => {
  it('sets position fixed, the active vertical edge, and left; clears the inactive edges (below)', () => {
    const el = document.createElement('div');
    const position: AnchorPosition = { placement: 'below', top: 138, bottom: null, left: 200 };
    applyAnchorPosition(el, position);
    expect(el.style.position).toBe('fixed');
    expect(el.style.top).toBe('138px');
    expect(el.style.bottom).toBe('auto');
    expect(el.style.left).toBe('200px');
    expect(el.style.right).toBe('auto');
  });

  it('anchors the bottom edge and clears top when above', () => {
    const el = document.createElement('div');
    const position: AnchorPosition = { placement: 'above', top: null, bottom: 78, left: 200 };
    applyAnchorPosition(el, position);
    expect(el.style.bottom).toBe('78px');
    expect(el.style.top).toBe('auto');
    expect(el.style.left).toBe('200px');
  });

  it('does not set z-index (the consumer owns stacking)', () => {
    const el = document.createElement('div');
    applyAnchorPosition(el, { placement: 'below', top: 10, bottom: null, left: 20 });
    expect(el.style.zIndex).toBe('');
  });
});
