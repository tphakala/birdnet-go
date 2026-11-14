/**
 * Number and label formatting shared by the detection detail panels. Numbers
 * follow the UI language chosen in the app, not the browser's.
 */
import { getLocale } from '$lib/i18n';
import type { OriginalSpecies } from '$lib/types/detection.types';
import type { DetectionDetailData } from './detailTypes';

/** A share from 0 to 1 times this is a percentage. */
export const PERCENT = 100;
// Below this share a score is shown as "less than", since one decimal would read 0.0.
const SMALLEST_SHOWN_PERCENT = 0.1;
// Below this share a score keeps one decimal, so low scores still differ from each other.
const ONE_DECIMAL_BELOW_PERCENT = 10;
// Shortest bar drawn for a count above zero, in percent, so a small count stays visible.
const MIN_BAR_PERCENT = 8;
// Model variant that is left out of a model's label.
const DEFAULT_MODEL_VARIANT = 'default';

/**
 * A model score from 0 to 1 as a percentage: whole numbers from 10% up, one
 * decimal below that, and "<0.1%" for anything smaller that is not zero.
 */
export function formatScore(score: number): string {
  if (!Number.isFinite(score) || score <= 0) return '0%';
  const percent = score * PERCENT;
  if (percent < SMALLEST_SHOWN_PERCENT) return `<${formatDecimal(SMALLEST_SHOWN_PERCENT)}%`;
  if (percent < ONE_DECIMAL_BELOW_PERCENT) return `${formatDecimal(percent)}%`;
  return `${Math.round(percent)}%`;
}

// Number formats by locale and options, built once: charts format many values per update.
const numberFormats = new Map<string, Intl.NumberFormat>();

function numberFormat(minimumFractionDigits?: number): Intl.NumberFormat {
  const locale = getLocale();
  const key = `${locale}|${minimumFractionDigits ?? ''}`;
  let format = numberFormats.get(key);
  if (!format) {
    format = new Intl.NumberFormat(
      locale,
      minimumFractionDigits === undefined
        ? undefined
        : { minimumFractionDigits, maximumFractionDigits: 1 }
    );
    numberFormats.set(key, format);
  }
  return format;
}

/**
 * A number with one decimal in the UI language ("4.5", "4,5"). With
 * `minimumDigits` 0 a whole number has none ("48", "44.1").
 */
export function formatDecimal(value: number, minimumDigits = 1): string {
  return numberFormat(minimumDigits).format(value);
}

/** A whole number in the UI language, with its digit grouping ("12,345", "12 345"). */
export function formatCount(value: number): string {
  return numberFormat().format(value);
}

/** A share from 0 to 1 as a whole percentage number. */
export function toPercent(share: number): number {
  return Math.round(share * PERCENT);
}

/** A share from 0 to 1 as "42%", or the fallback when there is no value. */
export function formatPercent(share: number | undefined | null, fallback = ''): string {
  return share === undefined || share === null ? fallback : `${toPercent(share)}%`;
}

/**
 * Height of a bar in a small bar chart, in percent of the chart: its count's
 * share of the tallest bar (`peak`) scaled to `scale`, and never shorter than a
 * sliver when the count is above zero.
 */
export function barHeight(count: number, peak: number, scale = PERCENT): number {
  return Math.max((count / peak) * scale, count > 0 ? MIN_BAR_PERCENT : 0);
}

/**
 * The model's own species when a reviewer changed the detection to another one;
 * null when it was never changed or was changed back.
 */
export function replacedModelPick(
  detection: Pick<DetectionDetailData, 'scientificName' | 'originalSpecies'>
): OriginalSpecies | null {
  const original = detection.originalSpecies;
  return original && original.scientificName !== detection.scientificName ? original : null;
}

/** "BirdNET 2.4", with the variant in parentheses unless it is the default one. */
export function formatModelLabel(name: string, version: string, variant?: string): string {
  const base = [name, version].filter(Boolean).join(' ');
  return variant && variant !== DEFAULT_MODEL_VARIANT ? `${base} (${variant})` : base;
}
