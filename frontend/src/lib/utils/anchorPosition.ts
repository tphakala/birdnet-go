/**
 * Shared anchor/flip positioning helper for portaled popups and dropdowns.
 *
 * Viewport-aware "flip above/below the trigger" placement was hand-rolled
 * independently across many components, which let the same class of bug drift
 * in and out of each copy (most visibly a hardcoded height estimate misaligning
 * the "above" placement). This helper centralises the geometry so every consumer
 * shares one implementation with the following invariants baked in:
 *
 * - A. The "above" placement anchors the element's BOTTOM edge to a viewport
 *      coordinate, never a `top` derived from the element height, so alignment is
 *      independent of the rendered height: a wrong or not-yet-measured height
 *      only changes how far the element extends upward. The single exception is
 *      when the element is taller than the space above the trigger, where the
 *      bottom is clamped so the top edge stays within the viewport (otherwise the
 *      top would be pushed off-screen); that is the only case where height affects
 *      the above anchor.
 * - B. The element height is measured (`offsetHeight`); the estimate is used only
 *      when the measured height is not yet available (`<= 0`).
 * - C. One flip formula, with an optional early-flip buffer.
 * - D. Exactly one of `top` / `bottom` is non-null in the result; the consumer
 *      sets that edge and clears the other.
 *
 * The helper computes geometry only. Portal ownership, z-index, transitions, and
 * listener lifecycle stay with each consumer (see {@link applyAnchorPosition} for
 * a small applier for components that write inline styles imperatively).
 */

/** Vertical placement relative to the trigger. */
export type Placement = 'above' | 'below';

/**
 * Horizontal alignment of the floating element relative to the trigger.
 * - `start`: align the floating element's left edge to the trigger's left edge.
 * - `end`: align the floating element's right edge to the trigger's right edge.
 * - `center`: centre the floating element on the trigger.
 *
 * The result is always clamped into the viewport regardless of alignment.
 */
export type HorizontalAlign = 'start' | 'end' | 'center';

/** Minimal trigger rectangle (a subset of DOMRect, so it is easy to construct in tests). */
export interface TriggerRect {
  top: number;
  bottom: number;
  left: number;
  right: number;
  width: number;
}

export interface AnchorPositionInput {
  /** Trigger bounding rect in viewport coordinates (from `getBoundingClientRect()`). */
  triggerRect: TriggerRect;
  /**
   * Measured height of the floating element (`offsetHeight`). When `<= 0` (not yet
   * laid out) the helper falls back to {@link AnchorPositionInput.estimatedHeight}.
   * `offsetHeight` is used rather than `getBoundingClientRect().height` because it
   * ignores enter-transition `scale()` transforms.
   */
  floatingHeight: number;
  /** Measured width of the floating element, used for clamping and `end`/`center` alignment. */
  floatingWidth: number;
  /** Fallback height used only when `floatingHeight <= 0`. Defaults to 0. */
  estimatedHeight?: number;
  /** Gap in px between the trigger and the floating element. Defaults to 8. */
  offset?: number;
  /** Minimum gap in px kept from the viewport edges. Defaults to 8. */
  viewportMargin?: number;
  /**
   * Extra space (px) required below the trigger before staying below; flips above
   * earlier when set. Defaults to 0.
   */
  earlyFlipBuffer?: number;
  /** Horizontal alignment relative to the trigger. Defaults to `start`. */
  align?: HorizontalAlign;
  /**
   * Viewport size in px. Defaults to the current `window` dimensions. Pass an
   * explicit value in tests (or for non-window viewports).
   */
  viewport?: { width: number; height: number };
}

export interface AnchorPosition {
  placement: Placement;
  /** Set (px from viewport top) when placement is `below`; null otherwise (Invariant A/D). */
  top: number | null;
  /** Set (px from viewport bottom) when placement is `above`; null otherwise (Invariant A/D). */
  bottom: number | null;
  /** Left edge in px, clamped into the viewport. */
  left: number;
}

const DEFAULT_OFFSET = 8;
const DEFAULT_VIEWPORT_MARGIN = 8;

function resolveViewport(viewport: AnchorPositionInput['viewport']): {
  width: number;
  height: number;
} {
  if (viewport) return viewport;
  // Browser-only fallback: every consumer is a client-side popup that positions from
  // an event handler, rAF, or an open-gated effect, never during SSR. Tests always
  // pass an explicit viewport. A future SSR consumer must pass `viewport` explicitly.
  return { width: globalThis.innerWidth, height: globalThis.innerHeight };
}

/**
 * Compute a viewport-aware flip position for a floating element anchored to a trigger.
 *
 * Returns a placement plus exactly one vertical anchor edge (`top` for `below`,
 * `bottom` for `above`) and a clamped `left`. See the module docblock for the
 * invariants this enforces.
 */
export function computeAnchorPosition(input: AnchorPositionInput): AnchorPosition {
  const {
    triggerRect,
    floatingHeight,
    floatingWidth,
    estimatedHeight = 0,
    offset = DEFAULT_OFFSET,
    viewportMargin = DEFAULT_VIEWPORT_MARGIN,
    earlyFlipBuffer = 0,
    align = 'start',
  } = input;

  const viewport = resolveViewport(input.viewport);

  // Invariant B: prefer the measured height; the estimate is only a pre-layout fallback.
  const height = floatingHeight > 0 ? floatingHeight : estimatedHeight;

  const spaceBelow = viewport.height - triggerRect.bottom;
  const spaceAbove = triggerRect.top;

  // Invariant C: a single flip formula. Flip above only when there is not enough
  // room below for the element plus its gap (and optional buffer) AND there is
  // genuinely more room above than below.
  const flipAbove = spaceBelow < height + offset + earlyFlipBuffer && spaceAbove > spaceBelow;

  // Invariant A: "above" anchors the bottom edge; "below" anchors the top edge.
  let placement: Placement;
  let top: number | null;
  let bottom: number | null;
  if (flipAbove) {
    placement = 'above';
    bottom = viewport.height - triggerRect.top + offset;
    // Invariant A clamp: if the element is taller than the space above the trigger,
    // anchoring its bottom would push the top edge off-screen. Cap the bottom so the
    // top edge stays at least `viewportMargin` from the viewport top.
    const maxBottom = viewport.height - height - viewportMargin;
    if (bottom > maxBottom) {
      // Pin the top edge at the margin so the header / first content stays visible;
      // an element taller than the available space lets its bottom overflow instead.
      // (maxBottom can go below viewportMargin for such elements, which is intended.)
      bottom = maxBottom;
    }
    top = null;
  } else {
    placement = 'below';
    top = triggerRect.bottom + offset;
    bottom = null;
  }

  // Horizontal alignment, then clamp into the viewport so the element can never
  // overflow either edge regardless of which edge it aligns to.
  let idealLeft: number;
  switch (align) {
    case 'end':
      idealLeft = triggerRect.right - floatingWidth;
      break;
    case 'center':
      idealLeft = triggerRect.left + triggerRect.width / 2 - floatingWidth / 2;
      break;
    case 'start':
    default:
      idealLeft = triggerRect.left;
      break;
  }
  const maxLeft = Math.max(viewportMargin, viewport.width - floatingWidth - viewportMargin);
  const left = Math.min(Math.max(idealLeft, viewportMargin), maxLeft);

  return { placement, top, bottom, left };
}

/**
 * Apply a computed {@link AnchorPosition} to an element's inline styles.
 *
 * For consumers that position imperatively (rather than via Svelte `style:`
 * bindings). Sets `position: fixed`, the single active vertical edge, and `left`,
 * and clears the inactive edges. Does NOT touch `z-index`; the consumer owns that.
 */
export function applyAnchorPosition(element: HTMLElement, position: AnchorPosition): void {
  element.style.position = 'fixed';
  element.style.left = `${position.left}px`;
  element.style.right = 'auto';
  if (position.top !== null) {
    element.style.top = `${position.top}px`;
    element.style.bottom = 'auto';
  } else {
    element.style.bottom = `${position.bottom}px`;
    element.style.top = 'auto';
  }
}
