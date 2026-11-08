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

/** Length of a clip a slider can seek in, or 0 while the duration is unknown or unusable. */
export function usableDuration(duration: number): number {
  return Number.isFinite(duration) && duration > 0 ? duration : 0;
}

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

/** What a slider does with one keydown: the time to seek to, and whether to cancel the event. */
export interface SeekKeyAction {
  target: number | null;
  preventDefault: boolean;
}

/**
 * Decide what a seek slider does for a keydown. Seek keys held with no Alt, Ctrl or
 * Meta move to their target and cancel the event so the page does not scroll; Shift
 * does not change the step. Space does not seek, but is cancelled for the same
 * reason, whether or not the clip length is known. Everything else, including keys
 * held with Alt, Ctrl or Meta, is left to the browser.
 */
export function seekKeyAction(
  event: Pick<KeyboardEvent, 'key' | 'altKey' | 'ctrlKey' | 'metaKey'>,
  current: number,
  max: number
): SeekKeyAction {
  if (event.altKey || event.ctrlKey || event.metaKey)
    return { target: null, preventDefault: false };
  if (event.key === ' ') return { target: null, preventDefault: true };
  const target = seekTargetForKey(event.key, current, max);
  return { target, preventDefault: target !== null };
}
