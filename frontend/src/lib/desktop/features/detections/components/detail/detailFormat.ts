/**
 * Number formatting shared by the detection detail panels.
 */

const PERCENT = 100;
// Below this share a score is shown as "less than", since one decimal would read 0.0.
const SMALLEST_SHOWN_PERCENT = 0.1;
// Below this share a score keeps one decimal, so low scores still differ from each other.
const ONE_DECIMAL_BELOW_PERCENT = 10;

/**
 * A model score from 0 to 1 as a percentage: whole numbers from 10% up, one
 * decimal below that, and "<0.1%" for anything smaller that is not zero.
 */
export function formatScore(score: number): string {
  if (!Number.isFinite(score) || score <= 0) return '0%';
  const percent = score * PERCENT;
  if (percent < SMALLEST_SHOWN_PERCENT) return `<${SMALLEST_SHOWN_PERCENT}%`;
  if (percent < ONE_DECIMAL_BELOW_PERCENT) return `${percent.toFixed(1)}%`;
  return `${Math.round(percent)}%`;
}
