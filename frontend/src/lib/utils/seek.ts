/**
 * seek.ts
 *
 * Pure helpers for seeking an audio clip with a slider: the keyboard model of the
 * WAI-ARIA slider pattern and the conversion of a pointer position to a time. The
 * audio player's progress bars use them so they seek by time, not by pointer
 * geometry, and clamp to the clip in one place.
 */

/** Seconds a seek moves for an arrow key. */
export const SEEK_STEP_SECONDS = 1;

/** Seconds a seek moves for PageUp and PageDown. */
export const SEEK_PAGE_STEP_SECONDS = 5;

/** Clamp a time in seconds to the clip, from 0 to max. */
export function clampSeconds(seconds: number, max: number): number {
  return Math.min(Math.max(seconds, 0), max);
}

/**
 * Time a seek key moves to, clamped to the clip, or null when the key is not a
 * seek key or the clip has no usable length. Right and Up step forward, Left and
 * Down step back, PageUp and PageDown move by the larger step, Home and End go to
 * the ends.
 */
export function seekTargetForKey(key: string, current: number, max: number): number | null {
  if (!Number.isFinite(max) || max <= 0) return null;
  switch (key) {
    case 'ArrowRight':
    case 'ArrowUp':
      return clampSeconds(current + SEEK_STEP_SECONDS, max);
    case 'ArrowLeft':
    case 'ArrowDown':
      return clampSeconds(current - SEEK_STEP_SECONDS, max);
    case 'PageUp':
      return clampSeconds(current + SEEK_PAGE_STEP_SECONDS, max);
    case 'PageDown':
      return clampSeconds(current - SEEK_PAGE_STEP_SECONDS, max);
    case 'Home':
      return 0;
    case 'End':
      return max;
    default:
      return null;
  }
}

/**
 * Time a pointer position on a progress bar maps to, clamped to the clip, or null
 * when the clip has no usable length or the bar has no width.
 */
export function seekTimeFromPointer(
  clientX: number,
  bar: Pick<DOMRect, 'left' | 'width'>,
  max: number
): number | null {
  if (!Number.isFinite(max) || max <= 0 || bar.width <= 0) return null;
  return clampSeconds(((clientX - bar.left) / bar.width) * max, max);
}
