/**
 * Layout stubs for jsdom, which does no layout: scrollHeight and clientHeight are 0.
 */

import { vi } from 'vitest';

/** clientHeight of a clamped paragraph. */
export const CLAMPED_HEIGHT = 50;
/** scrollHeight of a paragraph whose text is cut off by the clamp. */
export const FULL_HEIGHT = 84;

/**
 * Stub the layout so a paragraph whose text is `clippedText` reports more content than it
 * shows; any other text fits. Undo it with `vi.restoreAllMocks()`.
 */
export function stubLayout(clippedText: string): void {
  vi.spyOn(Element.prototype, 'clientHeight', 'get').mockReturnValue(CLAMPED_HEIGHT);
  vi.spyOn(Element.prototype, 'scrollHeight', 'get').mockImplementation(function (this: Element) {
    return this.textContent === clippedText ? FULL_HEIGHT : CLAMPED_HEIGHT;
  });
}
