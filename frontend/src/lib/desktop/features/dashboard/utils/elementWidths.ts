import type { DashboardElement } from '$lib/stores/settings';

/** Element types that always require full width (no half-width toggle). */
export const FULL_WIDTH_ONLY: ReadonlySet<string> = new Set(['daily-summary', 'live-spectrogram']);

/** Element types that support half width (show width toggle in edit mode). */
export const SUPPORTS_HALF: ReadonlySet<string> = new Set([
  'banner',
  'video-embed',
  'currently-hearing',
  'detections-grid',
]);

/**
 * Returns the effective display width for a dashboard element.
 * Full-width-only types always return 'full'.
 * Other types return 'half' only when explicitly set; any other value defaults to 'full'.
 */
export function getEffectiveWidth(el: DashboardElement): 'full' | 'half' {
  if (FULL_WIDTH_ONLY.has(el.type)) return 'full';
  return el.width === 'half' ? 'half' : 'full';
}
