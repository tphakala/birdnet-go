// confidenceColors.ts - Shared confidence-level color classes.
//
// Single source of truth for the threshold -> Tailwind color-class mapping used by
// confidence badges and pills (ConfidenceBadge, NewSpeciesHighlightsCard). Keeping
// this in one place stops the indicators from drifting apart when thresholds or
// theme colors change.

// Confidence percentage thresholds (0-100) that select each color band.
const CONFIDENCE_THRESHOLDS = {
  EXCELLENT: 90,
  GOOD: 70,
  MODERATE: 50,
  LOW: 30,
} as const;

/**
 * Returns the background/text Tailwind classes for a confidence percentage (0-100).
 *
 * Thresholds: >=90 success, >=70 success/warning blend, >=50 warning,
 * >=30 warning/error blend, <30 error.
 */
export function confidenceColorClasses(percent: number): string {
  if (percent >= CONFIDENCE_THRESHOLDS.EXCELLENT)
    return 'bg-[var(--color-success)] text-[var(--color-success-content)]';
  if (percent >= CONFIDENCE_THRESHOLDS.GOOD)
    return 'bg-[color-mix(in_srgb,var(--color-success)_80%,var(--color-warning))] text-white';
  if (percent >= CONFIDENCE_THRESHOLDS.MODERATE)
    return 'bg-[var(--color-warning)] text-[var(--color-warning-content)]';
  if (percent >= CONFIDENCE_THRESHOLDS.LOW)
    return 'bg-[color-mix(in_srgb,var(--color-warning)_60%,var(--color-error))] text-white';
  return 'bg-[var(--color-error)] text-[var(--color-error-content)]';
}
