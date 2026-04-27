import type { LowNoiseAutoSuspendSettings } from '$lib/stores/settings';

/**
 * Returns true when low-noise auto-suspend thresholds are valid.
 * Resume threshold must be strictly higher than suspend threshold
 * to maintain a hysteresis window and avoid rapid toggling.
 */
export function hasValidLowNoiseAutoSuspendThresholds(
  settings: LowNoiseAutoSuspendSettings
): boolean {
  if (!settings.enabled) {
    return true;
  }

  return settings.resumeThreshold > settings.suspendThreshold;
}
