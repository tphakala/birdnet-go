/**
 * Pixel-accurate label fitting for chart text drawn into a fixed-size margin or legend slot.
 *
 * Charts place species names into a fixed pixel budget (a left margin, a legend column). Truncating
 * by character count cannot honor that budget: a proportional font renders "Willow Warbler" and
 * "WWWWWWWWWWWWWW" at very different widths, so a char-capped label still overflows and is clipped
 * by the SVG viewport. These helpers cap by measured width instead.
 */

const ELLIPSIS = '…';

/**
 * Longest prefix of `text` that fits `maxPx`, with an ellipsis appended when anything was dropped.
 *
 * `measure` returns the rendered width of a candidate string. Assumes width grows monotonically
 * with prefix length (true for the LTR, non-combining species names these charts draw), which lets
 * the search bisect instead of walking every prefix.
 *
 * Returns the full text when it already fits, and an empty string when the budget cannot even hold
 * a lone ellipsis — callers get "no label" rather than a stray glyph over the plot.
 */
export function truncateToWidth(
  text: string,
  maxPx: number,
  measure: (_candidate: string) => number
): string {
  if (text === '' || maxPx <= 0) return '';
  if (measure(text) <= maxPx) return text;
  if (measure(ELLIPSIS) > maxPx) return '';

  // Bisect on prefix length: the widest k where prefix(k) + ellipsis still fits. lo always fits
  // (the ellipsis alone was checked above), hi never does, so the loop converges on lo = answer.
  let lo = 0;
  let hi = text.length;
  while (lo < hi) {
    const mid = Math.ceil((lo + hi) / 2);
    if (measure(text.slice(0, mid) + ELLIPSIS) <= maxPx) {
      lo = mid;
    } else {
      hi = mid - 1;
    }
  }
  return text.slice(0, lo) + ELLIPSIS;
}

/**
 * Fits an already-rendered SVG <text> to `maxPx` in place, ellipsizing it if needed.
 *
 * Sets the node's text content, so call it before attaching any <title> child. Attaching the full
 * name is the caller's job: axis ticks hang it on the enclosing `<g class="tick">` rather than the
 * <text>, which keeps the tick's own textContent equal to the visible label.
 *
 * Measurement needs a real layout engine. jsdom (unit tests) does not implement
 * getComputedTextLength, so there the node keeps its full text and this is a no-op by design — the
 * truncation logic is covered by truncateToWidth's tests, and the pixel fit is verified in a
 * browser.
 */
export function fitTextNode(node: SVGTextElement | null, full: string, maxPx: number): void {
  if (!node) return;

  node.textContent = full;
  if (typeof node.getComputedTextLength !== 'function') return;

  node.textContent = truncateToWidth(full, maxPx, candidate => {
    node.textContent = candidate;
    return node.getComputedTextLength();
  });
}
