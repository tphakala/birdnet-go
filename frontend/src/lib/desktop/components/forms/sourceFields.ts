import type { EqualizerSettings } from '$lib/stores/settings';

/** A source equalizer as it is saved: a disabled one without filters is stored as undefined. */
export function savedEqualizer(updated: EqualizerSettings): EqualizerSettings | undefined {
  return updated.enabled || updated.filters.length > 0
    ? { enabled: updated.enabled, filters: updated.filters }
    : undefined;
}
