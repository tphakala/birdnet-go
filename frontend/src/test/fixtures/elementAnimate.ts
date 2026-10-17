/**
 * jsdom has no Element.prototype.animate, which Svelte calls when a
 * `transition:` directive plays (for example a `slide` on an expanding
 * section). These helpers install a stub whose animation finishes on the next
 * microtask, and remove it again.
 */
import { vi } from 'vitest';

/** Install a finishing Element.prototype.animate stub. */
export function stubElementAnimate(): void {
  Element.prototype.animate = vi.fn(() => {
    const animation: {
      onfinish: null | (() => void);
      cancel: () => void;
      playState: string;
      currentTime: number;
    } = {
      onfinish: null,
      cancel: vi.fn(),
      playState: 'finished',
      currentTime: 0,
    };
    queueMicrotask(() => animation.onfinish?.());
    // A partial Animation: Svelte only sets onfinish and reads playState here
    return animation as unknown as Animation;
  });
}

/** Remove the stub installed by stubElementAnimate. */
export function restoreElementAnimate(): void {
  Reflect.deleteProperty(Element.prototype, 'animate');
}
